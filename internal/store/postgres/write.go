package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

// Write stores a batch in one transaction.
//
// It runs in five passes so the result does not depend on the order records
// appear in: lock every id and source the batch touches, find which records
// were already captured from the same source, rewrite every reference to the
// stored ids, work out each record's requirement set, then write.
//
// Ids found by deduplication are only known after the first locks are held,
// so two batches can still wait on each other. Postgres then aborts one of
// them, and that one starts over.
func (s *Store) Write(ctx context.Context, access store.Access, given store.Batch) (store.Written, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return store.Written{}, err
	}
	for attempt := 1; ; attempt++ {
		out, err := s.write(ctx, access, given)
		var pgErr *pgconn.PgError
		if attempt < 5 && errors.As(err, &pgErr) && pgErr.Code == "40P01" {
			continue
		}
		// Deduplication only answers with records the writer may read, so a
		// source already captured into a bucket it may not read is reported
		// as taken, and nothing more about it.
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.HasSuffix(pgErr.ConstraintName, "_source") {
			return out, ErrTaken
		}
		return out, err
	}
}

func (s *Store) write(ctx context.Context, access store.Access, given store.Batch) (store.Written, error) {
	out := store.Written{Stored: map[string]string{}}
	b, err := clone(given)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	w := &writer{tx: tx, out: &out, access: access, ws: access.Workspace, canonical: map[string]string{}}

	if err := w.buckets(ctx, b); err != nil {
		return out, err
	}
	if err := w.lock(ctx, lockKeys(w.ws, b)); err != nil {
		return out, err
	}
	if err := w.resolve(ctx, b); err != nil {
		return out, err
	}
	// Records found under another id are locked under that id too, so a
	// writer using the stored id directly waits for this one.
	var found []string
	for _, stored := range w.canonical {
		found = append(found, idKey(w.ws, stored))
	}
	if err := w.lock(ctx, found); err != nil {
		return out, err
	}
	if err := tx.QueryRow(ctx, `select clock_timestamp()`).Scan(&w.now); err != nil {
		return out, err
	}
	w.rewrite(b)
	if err := rewritten(b); err != nil {
		return out, err
	}
	if err := w.plan(ctx, b); err != nil {
		return out, err
	}

	for _, e := range b.Entities {
		if err := w.entity(ctx, e); err != nil {
			return out, fmt.Errorf("entity %s: %w", e.ID, err)
		}
	}
	for i, e := range b.Events {
		lineage, refresh := store.TypeLineage(given.Lineage.Events, i, e.Type)
		if err := w.event(ctx, e, lineage, refresh); err != nil {
			return out, fmt.Errorf("event %s: %w", e.ID, err)
		}
	}
	for _, a := range b.Appraisals {
		if err := w.appraisal(ctx, a); err != nil {
			return out, fmt.Errorf("appraisal %s: %w", a.ID, err)
		}
	}
	for _, l := range b.Links {
		if err := w.link(ctx, l); err != nil {
			return out, fmt.Errorf("link %s: %w", l.ID, err)
		}
	}
	for given := range w.givenIDs {
		out.Stored[given] = w.id(given)
	}
	return out, tx.Commit(ctx)
}

func clone(b store.Batch) (*store.Batch, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	var out store.Batch
	return &out, json.Unmarshal(raw, &out)
}

type writer struct {
	tx        pgx.Tx
	out       *store.Written
	access    store.Access
	ws        string
	now       time.Time
	canonical map[string]string
	givenIDs  map[string]bool
	// requires is each batch record's requirement set, by stored id.
	requires map[string][]string
}

func (w *writer) id(given string) string {
	if stored, ok := w.canonical[given]; ok {
		return stored
	}
	return given
}

func idKey(ws, id string) string { return "id:" + ws + "\x1f" + id }

func sourceKey(ws, bucket, table string, src *record.Source) string {
	if src == nil || src.NativeID == "" {
		return ""
	}
	return "src:" + table + ":" + ws + "\x1f" + bucket + "\x1f" + src.System + "\x1f" + src.Installation + "\x1f" + src.NativeID
}

func lockKeys(ws string, b *store.Batch) []string {
	var keys []string
	add := func(id, bucket, table string, src *record.Source) {
		keys = append(keys, idKey(ws, id))
		if k := sourceKey(ws, bucket, table, src); k != "" {
			keys = append(keys, k)
		}
	}
	for _, e := range b.Entities {
		add(e.ID, e.Bucket, "entities", e.Source)
	}
	for _, e := range b.Events {
		add(e.ID, e.Bucket, "events", e.Source)
	}
	for _, a := range b.Appraisals {
		add(a.ID, a.Bucket, "appraisals", a.Source)
	}
	for _, l := range b.Links {
		add(l.ID, l.Bucket, "links", l.Source)
	}
	return keys
}

// lock takes transaction advisory locks in a fixed order, so two batches
// touching the same records wait for each other instead of deadlocking.
func (w *writer) lock(ctx context.Context, keys []string) error {
	sort.Strings(keys)
	last := ""
	for _, k := range keys {
		if k == last {
			continue
		}
		last = k
		if _, err := w.tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1, 0))`, k); err != nil {
			return err
		}
	}
	return nil
}

// resolve finds records in the batch already captured from the same source
// under another id.
func (w *writer) resolve(ctx context.Context, b *store.Batch) error {
	w.givenIDs = map[string]bool{}
	first := map[string]string{}
	find := func(table, id, bucket string, src *record.Source, current string) error {
		w.givenIDs[id] = true
		key := sourceKey(w.ws, bucket, table, src)
		if key == "" {
			return nil
		}
		// A source repeated within the batch is one record: later copies
		// take the id the first was given.
		if earlier, ok := first[key]; ok {
			if earlier != id {
				w.canonical[id] = w.id(earlier)
			}
			return nil
		}
		first[key] = id
		var stored string
		// Only a record the writer may read is answered with. One it may not
		// stays unknown to it, and writing it again is refused as taken.
		err := w.tx.QueryRow(ctx, `select id from `+table+
			` where workspace_id = $1 and source_system is not distinct from $2
			and source_installation is not distinct from nullif($3, '') and source_native_id = $4
			and bucket_id = $5 and requires <@ $6`+current,
			w.ws, src.System, src.Installation, src.NativeID, bucket, w.access.Read).Scan(&stored)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if stored != id {
			w.canonical[id] = stored
		}
		return nil
	}
	for _, e := range b.Entities {
		if err := find("entities", e.ID, e.Bucket, e.Source, ""); err != nil {
			return err
		}
	}
	for _, e := range b.Events {
		if err := find("events", e.ID, e.Bucket, e.Source, ""); err != nil {
			return err
		}
	}
	for _, a := range b.Appraisals {
		if err := find("appraisals", a.ID, a.Bucket, a.Source, ""); err != nil {
			return err
		}
	}
	for _, l := range b.Links {
		if err := find("links", l.ID, l.Bucket, l.Source, ""); err != nil {
			return err
		}
	}
	return nil
}

// rewritten checks the batch again once ids are rewritten: deduplication can
// turn a record into its own parent or reply.
func rewritten(b *store.Batch) error {
	var problems record.Problems
	add := func(prefix string, found record.Problems) {
		for _, p := range found {
			problems = append(problems, record.Problem{Field: prefix + "." + p.Field, Message: p.Message})
		}
	}
	for i, e := range b.Entities {
		add(fmt.Sprintf("entities[%d]", i), e.Validate())
	}
	for i, e := range b.Events {
		add(fmt.Sprintf("events[%d]", i), e.Validate())
	}
	for i, a := range b.Appraisals {
		add(fmt.Sprintf("appraisals[%d]", i), a.Validate())
	}
	for i, l := range b.Links {
		add(fmt.Sprintf("links[%d]", i), l.Validate())
	}
	return problems.Err()
}

func (w *writer) rewrite(b *store.Batch) {
	fix := func(id *string, refs []record.Ref) {
		*id = w.id(*id)
		for _, r := range refs {
			*r.ID = w.id(*r.ID)
		}
	}
	for i := range b.Entities {
		fix(&b.Entities[i].ID, b.Entities[i].Refs())
	}
	for i := range b.Events {
		fix(&b.Events[i].ID, b.Events[i].Refs())
	}
	for i := range b.Appraisals {
		fix(&b.Appraisals[i].ID, b.Appraisals[i].Refs())
	}
	for i := range b.Links {
		fix(&b.Links[i].ID, b.Links[i].Refs())
	}
}

var ErrRecordType = errors.New("id already belongs to another record type")

// ErrTaken is returned when an id belongs to a record the writer may not
// read. It says no more than that, so it reveals nothing about the record.
var ErrTaken = errors.New("id is already used in this workspace")

func (w *writer) claim(ctx context.Context, id, recordType string, class record.Class) error {
	var owner, ownerClass string
	err := w.tx.QueryRow(ctx, `
		insert into record_ids (workspace_id, id, record_type, class) values ($1, $2, $3, nullif($4, ''))
		on conflict (workspace_id, id) do update set id = excluded.id
		returning record_type, coalesce(class, '')`, w.ws, id, recordType, string(class)).Scan(&owner, &ownerClass)
	if err != nil {
		return err
	}
	if visible, err := w.visible(ctx, id); err != nil {
		return err
	} else if !visible {
		return fmt.Errorf("%w: %s", ErrTaken, id)
	}
	if owner != recordType {
		return fmt.Errorf("%w: %s is a %s", ErrRecordType, id, owner)
	}
	if ownerClass != string(class) {
		return fmt.Errorf("%w: %s is a %s entity, not %s", ErrRecordType, id, or(ownerClass, "classless"), or(string(class), "classless"))
	}
	return nil
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

func (w *writer) unchanged(id string) {
	w.out.Unchanged = append(w.out.Unchanged, id)
}

func source(src *record.Source) (system, installation, native *string) {
	if src == nil || src.NativeID == "" {
		return nil, nil, nil
	}
	if src.Installation != "" {
		installation = &src.Installation
	}
	return &src.System, installation, &src.NativeID
}

func types(ts []record.Type) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = string(t)
	}
	return out
}

func at(i *record.Instant) *time.Time {
	if i == nil {
		return nil
	}
	return &i.At
}

// sameAs reports whether a stored record's body equals the given one. The
// comparison is jsonb equality, which keeps numbers exact.
func (w *writer) sameAs(ctx context.Context, table, where string, body []byte, args ...any) (exists, same bool, err error) {
	args = append(args, string(body))
	err = w.tx.QueryRow(ctx, fmt.Sprintf(`select body = $%d::jsonb from %s where %s`, len(args), table, where), args...).Scan(&same)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, false, nil
	}
	return err == nil, same, err
}

func (w *writer) entity(ctx context.Context, e record.Entity) error {
	if err := w.claim(ctx, e.ID, "entity", e.Class); err != nil {
		return err
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := w.stays(ctx, "entities", e.ID, e.Bucket); err != nil {
		return err
	}
	system, installation, native := source(e.Source)
	// Entities describe things that change (a name, a picture), so the newest
	// description replaces the last.
	tag, err := w.tx.Exec(ctx, `
		insert into entities (workspace_id, id, bucket_id, requires, class, type, name, parent_id, body,
			source_system, source_installation, source_native_id, recorded_at)
		values ($1, $2, $3, $4, nullif($5, ''), $6, nullif($7, ''), nullif($8, ''), $9, $10, $11, $12, $13)
		on conflict (workspace_id, id) do update set class = excluded.class, type = excluded.type, name = excluded.name,
			parent_id = excluded.parent_id, body = excluded.body, source_system = excluded.source_system,
			source_installation = excluded.source_installation,
			source_native_id = excluded.source_native_id, recorded_at = excluded.recorded_at
		where entities.body is distinct from excluded.body`,
		w.ws, e.ID, e.Bucket, w.requires[e.ID], string(e.Class), string(e.Type), e.Name, e.Parent, body,
		system, installation, native, w.now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		w.unchanged(e.ID)
		return nil
	}
	return w.identities(ctx, e)
}

// identities makes the stored handles match the entity's description:
// handles it no longer lists are released, new ones are claimed.
func (w *writer) identities(ctx context.Context, e record.Entity) error {
	platforms := make([]string, len(e.Identities))
	handles := make([]string, len(e.Identities))
	for i, id := range e.Identities {
		platforms[i], handles[i] = id.Platform, id.Handle
	}
	if _, err := w.tx.Exec(ctx, `
		delete from identities where workspace_id = $1 and actor_id = $2
		and (platform, handle) not in (select * from unnest($3::text[], $4::text[]))`, w.ws, e.ID, platforms, handles); err != nil {
		return err
	}
	for _, id := range e.Identities {
		var owner string
		err := w.tx.QueryRow(ctx, `
			insert into identities (workspace_id, platform, handle, actor_id) values ($1, $2, $3, $4)
			on conflict (workspace_id, platform, handle) do update set platform = excluded.platform
			returning actor_id`, w.ws, id.Platform, id.Handle, e.ID).Scan(&owner)
		if err != nil {
			return err
		}
		if owner != e.ID {
			if visible, err := w.visible(ctx, owner); err != nil {
				return err
			} else if !visible {
				return fmt.Errorf("%s handle %s is already used in this workspace", id.Platform, id.Handle)
			}
			return fmt.Errorf("%s handle %s already belongs to %s", id.Platform, id.Handle, owner)
		}
	}
	return nil
}

func (w *writer) involve(ctx context.Context, recordType, recordID string, version int, place string, participants []record.Participant, subjects []record.SubjectRef, involves []record.Involvement) error {
	row := func(entity, via string, role record.Role) error {
		_, err := w.tx.Exec(ctx, `
			insert into involvements (workspace_id, record_type, record_id, version, entity_id, via, role)
			values ($1, $2, $3, $4, $5, $6, $7)
			on conflict do nothing`, w.ws, recordType, recordID, version, entity, via, string(role))
		return err
	}
	if place != "" {
		if err := row(place, "place", "place"); err != nil {
			return err
		}
	}
	for _, p := range participants {
		if err := row(p.Actor, "participant", p.Role); err != nil {
			return err
		}
	}
	for _, s := range subjects {
		if err := row(s.Subject, "subject", s.Role); err != nil {
			return err
		}
	}
	for _, v := range involves {
		if err := row(v.Entity, "involves", v.Role); err != nil {
			return err
		}
	}
	return nil
}

// A record captured again unchanged still takes the lineage capture worked
// out, when there is one: it is derived from the record's profiles, not part
// of the record, and rows stored before lineage was kept need repairing.
func (w *writer) relineage(ctx context.Context, table, id string, lineage []record.Type) error {
	_, err := w.tx.Exec(ctx, `update `+table+` set type_lineage = $3 where workspace_id = $1 and id = $2 and type_lineage is distinct from $3`, w.ws, id, types(lineage))
	return err
}

func (w *writer) event(ctx context.Context, e record.Event, lineage []record.Type, refresh bool) error {
	e.Recall = nil
	e.Appraisals = nil
	if err := w.claim(ctx, e.ID, "event", ""); err != nil {
		return err
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := w.stays(ctx, "events", e.ID, e.Bucket); err != nil {
		return err
	}
	exists, same, err := w.sameAs(ctx, "events", "workspace_id = $1 and id = $2", body, w.ws, e.ID)
	if err != nil {
		return err
	}
	if exists {
		if !same {
			return store.ErrChanged
		}
		w.unchanged(e.ID)
		if refresh {
			return w.relineage(ctx, "events", e.ID, lineage)
		}
		return nil
	}
	texts := []string{e.Why}
	for _, u := range e.Content {
		texts = append(texts, u.Text)
	}
	var precision *string
	if e.OccurredAt != nil {
		p := string(e.OccurredAt.Precision)
		precision = &p
	}
	system, installation, native := source(e.Source)
	_, err = w.tx.Exec(ctx, `
		insert into events (workspace_id, id, bucket_id, requires, type, occurred_at, occurred_precision, ended_at, place_id,
			part_of, in_reply_to, body, said, source_system, source_installation, source_native_id, recorded_at, type_lineage, why)
		values ($1, $2, $3, $4, $5, $6, $7, $8, nullif($9, ''), nullif($10, ''), nullif($11, ''), $12,
			to_tsvector('english', $13), $14, $15, $16, $17, $18, $19)`,
		w.ws, e.ID, e.Bucket, w.requires[e.ID], string(e.Type), at(e.OccurredAt), precision, at(e.EndedAt), e.Place,
		e.PartOf, e.InReplyTo, body, strings.Join(texts, "\n"), system, installation, native, w.now, types(lineage), e.Why)
	if err != nil {
		return err
	}
	return w.involve(ctx, "event", e.ID, 0, e.Place, e.Participants, e.Subjects, e.Involves)
}

func (w *writer) appraisal(ctx context.Context, a record.Appraisal) error {
	if err := w.claim(ctx, a.ID, "appraisal", ""); err != nil {
		return err
	}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if err := w.stays(ctx, "appraisals", a.ID, a.Bucket); err != nil {
		return err
	}
	exists, same, err := w.sameAs(ctx, "appraisals", "workspace_id = $1 and id = $2", body, w.ws, a.ID)
	if err != nil {
		return err
	}
	if exists {
		if !same {
			return store.ErrChanged
		}
		w.unchanged(a.ID)
		return nil
	}
	system, installation, native := source(a.Source)
	_, err = w.tx.Exec(ctx, `
		insert into appraisals (workspace_id, id, bucket_id, requires, target_id, by_id, at, valence, intensity, importance,
			surprise, confidence, body, source_system, source_installation, source_native_id, recorded_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
		w.ws, a.ID, a.Bucket, w.requires[a.ID], a.Target, a.By, at(a.At), a.Valence, a.Intensity, a.Importance,
		a.Surprise, a.Confidence, body, system, installation, native, w.now)
	return err
}

func (w *writer) link(ctx context.Context, l record.Link) error {
	if err := w.claim(ctx, l.ID, "link", ""); err != nil {
		return err
	}
	body, err := json.Marshal(l)
	if err != nil {
		return err
	}
	if err := w.stays(ctx, "links", l.ID, l.Bucket); err != nil {
		return err
	}
	exists, same, err := w.sameAs(ctx, "links", "workspace_id = $1 and id = $2", body, w.ws, l.ID)
	if err != nil {
		return err
	}
	if exists {
		if !same {
			return store.ErrChanged
		}
		w.unchanged(l.ID)
		return nil
	}
	system, installation, native := source(l.Source)
	_, err = w.tx.Exec(ctx, `
		insert into links (workspace_id, id, bucket_id, requires, from_id, type, to_id, at, body,
			source_system, source_installation, source_native_id, recorded_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		w.ws, l.ID, l.Bucket, w.requires[l.ID], l.From, string(l.Type), l.To, at(l.At), body,
		system, installation, native, w.now)
	return err
}

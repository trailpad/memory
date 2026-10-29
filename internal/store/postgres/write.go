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
// It runs in four passes so the result does not depend on the order records
// appear in: lock every id and source the batch touches, find which records
// were already captured from the same source, rewrite every reference to the
// stored ids, then write.
//
// Ids found by deduplication are only known after the first locks are held,
// so two batches can still wait on each other. Postgres then aborts one of
// them, and that one starts over.
func (s *Store) Write(ctx context.Context, given store.Batch) (store.Written, error) {
	for attempt := 1; ; attempt++ {
		out, err := s.write(ctx, given)
		var pgErr *pgconn.PgError
		if attempt < 5 && errors.As(err, &pgErr) && pgErr.Code == "40P01" {
			continue
		}
		return out, err
	}
}

func (s *Store) write(ctx context.Context, given store.Batch) (store.Written, error) {
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
	w := &writer{tx: tx, out: &out, canonical: map[string]string{}}

	if err := w.lock(ctx, lockKeys(b)); err != nil {
		return out, err
	}
	if err := w.resolve(ctx, b); err != nil {
		return out, err
	}
	// Records found under another id are locked under that id too, so a
	// writer using the stored id directly waits for this one.
	var found []string
	for _, stored := range w.canonical {
		found = append(found, "id:"+stored)
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

	for _, e := range b.Entities {
		if err := w.entity(ctx, e); err != nil {
			return out, fmt.Errorf("entity %s: %w", e.ID, err)
		}
	}
	for _, e := range b.Events {
		if err := w.event(ctx, e); err != nil {
			return out, fmt.Errorf("event %s: %w", e.ID, err)
		}
	}
	for _, m := range b.Memories {
		if err := w.memory(ctx, m); err != nil {
			return out, fmt.Errorf("memory %s: %w", m.ID, err)
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
	now       time.Time
	canonical map[string]string
	givenIDs  map[string]bool
}

func (w *writer) id(given string) string {
	if stored, ok := w.canonical[given]; ok {
		return stored
	}
	return given
}

func sourceKey(table string, src *record.Source) string {
	if src == nil || src.NativeID == "" {
		return ""
	}
	return "src:" + table + ":" + src.System + "\x1f" + src.NativeID
}

func lockKeys(b *store.Batch) []string {
	var keys []string
	add := func(id, table string, src *record.Source) {
		keys = append(keys, "id:"+id)
		if k := sourceKey(table, src); k != "" {
			keys = append(keys, k)
		}
	}
	for _, e := range b.Entities {
		add(e.ID, "entities", e.Source)
	}
	for _, e := range b.Events {
		add(e.ID, "events", e.Source)
	}
	for _, m := range b.Memories {
		add(m.ID, "memories", m.Source)
	}
	for _, a := range b.Appraisals {
		add(a.ID, "appraisals", a.Source)
	}
	for _, l := range b.Links {
		add(l.ID, "links", l.Source)
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
	find := func(table, id string, src *record.Source, current string) error {
		w.givenIDs[id] = true
		key := sourceKey(table, src)
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
		err := w.tx.QueryRow(ctx, `select id from `+table+
			` where source_system is not distinct from $1 and source_native_id = $2`+current,
			src.System, src.NativeID).Scan(&stored)
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
		if err := find("entities", e.ID, e.Source, ""); err != nil {
			return err
		}
	}
	for _, e := range b.Events {
		if err := find("events", e.ID, e.Source, ""); err != nil {
			return err
		}
	}
	for _, m := range b.Memories {
		if err := find("memories", m.ID, m.Source, " and superseded_at is null"); err != nil {
			return err
		}
	}
	for _, a := range b.Appraisals {
		if err := find("appraisals", a.ID, a.Source, ""); err != nil {
			return err
		}
	}
	for _, l := range b.Links {
		if err := find("links", l.ID, l.Source, ""); err != nil {
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
	for i, m := range b.Memories {
		add(fmt.Sprintf("memories[%d]", i), m.Validate())
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
	for i := range b.Memories {
		fix(&b.Memories[i].ID, b.Memories[i].Refs())
	}
	for i := range b.Appraisals {
		fix(&b.Appraisals[i].ID, b.Appraisals[i].Refs())
	}
	for i := range b.Links {
		fix(&b.Links[i].ID, b.Links[i].Refs())
	}
}

// ErrKind is returned when an id already belongs to a record of another
// kind, or an entity of another class. References were checked against the
// stored kind, so it never changes.
var ErrKind = errors.New("id already belongs to another kind of record")

// claim records which kind of record owns an id.
func (w *writer) claim(ctx context.Context, id, kind string, class record.Class) error {
	var owner, ownerClass string
	err := w.tx.QueryRow(ctx, `
		insert into record_ids (id, kind, class) values ($1, $2, nullif($3, ''))
		on conflict (id) do update set id = excluded.id
		returning kind, coalesce(class, '')`, id, kind, string(class)).Scan(&owner, &ownerClass)
	if err != nil {
		return err
	}
	if owner != kind {
		return fmt.Errorf("%w: %s is a %s", ErrKind, id, owner)
	}
	if ownerClass != string(class) {
		return fmt.Errorf("%w: %s is a %s entity, not %s", ErrKind, id, or(ownerClass, "classless"), or(string(class), "classless"))
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

func source(src *record.Source) (system, native *string) {
	if src == nil || src.NativeID == "" {
		return nil, nil
	}
	return &src.System, &src.NativeID
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
	system, native := source(e.Source)
	// Entities describe things that change (a name, a picture), so the newest
	// description replaces the last.
	tag, err := w.tx.Exec(ctx, `
		insert into entities (id, class, type, name, parent_id, body, source_system, source_native_id, recorded_at)
		values ($1, nullif($2, ''), $3, nullif($4, ''), nullif($5, ''), $6, $7, $8, $9)
		on conflict (id) do update set class = excluded.class, type = excluded.type, name = excluded.name,
			parent_id = excluded.parent_id, body = excluded.body, source_system = excluded.source_system,
			source_native_id = excluded.source_native_id, recorded_at = excluded.recorded_at
		where entities.body is distinct from excluded.body`,
		e.ID, string(e.Class), string(e.Type), e.Name, e.Parent, body, system, native, w.now)
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
		delete from identities where actor_id = $1
		and (platform, handle) not in (select * from unnest($2::text[], $3::text[]))`, e.ID, platforms, handles); err != nil {
		return err
	}
	for _, id := range e.Identities {
		var owner string
		err := w.tx.QueryRow(ctx, `
			insert into identities (platform, handle, actor_id) values ($1, $2, $3)
			on conflict (platform, handle) do update set platform = excluded.platform
			returning actor_id`, id.Platform, id.Handle, e.ID).Scan(&owner)
		if err != nil {
			return err
		}
		if owner != e.ID {
			return fmt.Errorf("%s handle %s already belongs to %s", id.Platform, id.Handle, owner)
		}
	}
	return nil
}

func (w *writer) involve(ctx context.Context, kind, recordID string, version int, place string, participants []record.Participant, subjects []record.SubjectRef, involves []record.Involvement) error {
	row := func(entity, via string, role record.Role) error {
		_, err := w.tx.Exec(ctx, `
			insert into involvements (record_kind, record_id, version, entity_id, via, role) values ($1, $2, $3, $4, $5, $6)
			on conflict do nothing`, kind, recordID, version, entity, via, string(role))
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

func (w *writer) event(ctx context.Context, e record.Event) error {
	if err := w.claim(ctx, e.ID, "event", ""); err != nil {
		return err
	}
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	exists, same, err := w.sameAs(ctx, "events", "id = $1", body, e.ID)
	if err != nil {
		return err
	}
	if exists {
		if !same {
			return store.ErrChanged
		}
		w.unchanged(e.ID)
		return nil
	}
	texts := make([]string, 0, len(e.Content))
	for _, u := range e.Content {
		texts = append(texts, u.Text)
	}
	var precision *string
	if e.OccurredAt != nil {
		p := string(e.OccurredAt.Precision)
		precision = &p
	}
	system, native := source(e.Source)
	_, err = w.tx.Exec(ctx, `
		insert into events (id, type, occurred_at, occurred_precision, ended_at, place_id, part_of, in_reply_to, body, said,
			source_system, source_native_id, recorded_at)
		values ($1, $2, $3, $4, $5, nullif($6, ''), nullif($7, ''), nullif($8, ''), $9, to_tsvector('english', $10), $11, $12, $13)`,
		e.ID, string(e.Type), at(e.OccurredAt), precision, at(e.EndedAt), e.Place, e.PartOf, e.InReplyTo, body,
		strings.Join(texts, "\n"), system, native, w.now)
	if err != nil {
		return err
	}
	return w.involve(ctx, "event", e.ID, 0, e.Place, e.Participants, e.Subjects, e.Involves)
}

func (w *writer) memory(ctx context.Context, m record.Memory) error {
	if err := w.claim(ctx, m.ID, "memory", ""); err != nil {
		return err
	}
	// How often a memory was used is the store's to count, never the writer's.
	m.Recall = nil
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var version int
	var same bool
	err = w.tx.QueryRow(ctx, `select version, body = $2::jsonb from memories where id = $1 and superseded_at is null`,
		m.ID, string(body)).Scan(&version, &same)
	switch {
	case err == nil && same:
		w.unchanged(m.ID)
		return nil
	case err == nil:
		if _, err := w.tx.Exec(ctx, `update memories set superseded_at = $2 where id = $1 and superseded_at is null`, m.ID, w.now); err != nil {
			return err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}

	next := version + 1
	system, native := source(m.Source)
	_, err = w.tx.Exec(ctx, `
		insert into memories (id, version, type, statement, why, valid_from, valid_until, confidence, body, said,
			recall_count, last_recalled_at, source_system, source_native_id, recorded_at)
		select $1, $2, $3, $4, nullif($5, ''), $6, $7, $8, $9, to_tsvector('english', $4 || ' ' || $5),
			coalesce(prev.recall_count, 0), prev.last_recalled_at, $10, $11, $12
		from (select 1) one
		left join memories prev on prev.id = $1 and prev.version = $2 - 1`,
		m.ID, next, string(m.Type), m.Statement, m.Why, at(m.ValidFrom), at(m.ValidUntil), m.Confidence, body, system, native, w.now)
	if err != nil {
		return err
	}
	for _, ev := range m.Evidence {
		if _, err := w.tx.Exec(ctx, `insert into evidence (memory_id, version, event_id, role) values ($1, $2, $3, $4) on conflict do nothing`,
			m.ID, next, ev.Event, string(ev.Role)); err != nil {
			return err
		}
	}
	return w.involve(ctx, "memory", m.ID, next, "", m.Participants, m.Subjects, m.Involves)
}

func (w *writer) appraisal(ctx context.Context, a record.Appraisal) error {
	if err := w.claim(ctx, a.ID, "appraisal", ""); err != nil {
		return err
	}
	body, err := json.Marshal(a)
	if err != nil {
		return err
	}
	exists, same, err := w.sameAs(ctx, "appraisals", "id = $1", body, a.ID)
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
	system, native := source(a.Source)
	_, err = w.tx.Exec(ctx, `
		insert into appraisals (id, target_id, by_id, at, valence, intensity, importance, surprise, confidence, body,
			source_system, source_native_id, recorded_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		a.ID, a.Target, a.By, at(a.At), a.Valence, a.Intensity, a.Importance, a.Surprise, a.Confidence, body, system, native, w.now)
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
	exists, same, err := w.sameAs(ctx, "links", "id = $1", body, l.ID)
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
	system, native := source(l.Source)
	_, err = w.tx.Exec(ctx, `
		insert into links (id, from_id, type, to_id, at, body, source_system, source_native_id, recorded_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		l.ID, l.From, string(l.Type), l.To, at(l.At), body, system, native, w.now)
	return err
}

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

// buckets checks every bucket the batch writes into exists, is not deleted
// and may be written by the access.
func (w *writer) buckets(ctx context.Context, b *store.Batch) error {
	writable := map[string]bool{}
	for _, id := range w.access.Write {
		writable[id] = true
	}
	named := map[string]bool{}
	for _, e := range b.Entities {
		named[e.Bucket] = true
	}
	for _, e := range b.Events {
		named[e.Bucket] = true
	}
	for _, m := range b.Memories {
		named[m.Bucket] = true
	}
	for _, a := range b.Appraisals {
		named[a.Bucket] = true
	}
	for _, l := range b.Links {
		named[l.Bucket] = true
	}
	var want []string
	for id := range named {
		if !writable[id] {
			return fmt.Errorf("%w: %s", store.ErrBucket, id)
		}
		want = append(want, id)
	}
	rows, err := w.tx.Query(ctx, `select id from buckets where workspace_id = $1 and id = any($2) and deleted_at is null`, w.ws, want)
	if err != nil {
		return err
	}
	live := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		live[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	sort.Strings(want)
	for _, id := range want {
		if !live[id] {
			return fmt.Errorf("%w: %s", store.ErrBucket, id)
		}
	}
	return nil
}

// visible reports whether the access may read the stored record with this
// id. An id with no record yet is visible: there is nothing to hide.
func (w *writer) visible(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := w.tx.QueryRow(ctx, `select not exists (
		select 1 from entities where workspace_id = $1 and id = $2 and not requires <@ $3
		union all select 1 from events where workspace_id = $1 and id = $2 and not requires <@ $3
		union all select 1 from memories where workspace_id = $1 and id = $2 and superseded_at is null and not requires <@ $3
		union all select 1 from appraisals where workspace_id = $1 and id = $2 and not requires <@ $3
		union all select 1 from links where workspace_id = $1 and id = $2 and not requires <@ $3)`,
		w.ws, id, w.access.Read).Scan(&ok)
	return ok, err
}

// stays refuses writing a stored record into another bucket.
func (w *writer) stays(ctx context.Context, table, id, bucket string) error {
	var stored string
	err := w.tx.QueryRow(ctx, `select bucket_id from `+table+` where workspace_id = $1 and id = $2 limit 1`, w.ws, id).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if stored != bucket {
		return fmt.Errorf("%w: %s is in %s", store.ErrMove, id, stored)
	}
	return nil
}

// planned is a batch record whose requirement set depends on others.
type planned struct {
	bucket string
	memory bool
	inputs []input
}

// plan works out every batch record's requirement set: its own bucket, and
// the requirement sets of what it is made from, judges or links. A memory
// also keeps everything its current version required, so a new version
// never widens who may read it. Records outside the batch are read as
// stored, and only when the access may read them.
func (w *writer) plan(ctx context.Context, b *store.Batch) error {
	w.requires = map[string][]string{}
	w.inputs = map[string][]input{}
	batch := map[string]planned{}
	// Copies of one source deduplicated into one id are versions of one
	// record, so the set covers what every copy was made from.
	add := func(id string, p planned) {
		if earlier, ok := batch[id]; ok {
			p.inputs = append(earlier.inputs, p.inputs...)
		}
		batch[id] = p
	}
	for _, e := range b.Entities {
		add(e.ID, planned{bucket: e.Bucket})
	}
	for _, e := range b.Events {
		add(e.ID, planned{bucket: e.Bucket})
	}
	for _, m := range b.Memories {
		p := planned{bucket: m.Bucket, memory: true}
		for _, ev := range m.Evidence {
			p.inputs = append(p.inputs, input{id: ev.Event})
		}
		for _, d := range m.DependsOn {
			p.inputs = append(p.inputs, input{id: d.Record, version: d.Version})
		}
		add(m.ID, p)
	}
	for _, a := range b.Appraisals {
		add(a.ID, planned{bucket: a.Bucket, inputs: []input{{id: a.Target}}})
	}
	for _, l := range b.Links {
		add(l.ID, planned{bucket: l.Bucket, inputs: []input{{id: l.From}, {id: l.To}}})
	}

	if err := w.references(ctx, b, batch); err != nil {
		return err
	}

	next := map[string]int{}
	visiting := map[string]bool{}
	var of func(id string, version int) ([]string, int, error)
	of = func(id string, version int) ([]string, int, error) {
		// A memory named at a version is that version as stored, even when
		// the batch writes a newer one.
		p, inBatch := batch[id]
		if !inBatch || version > 0 {
			return w.stored(ctx, id, version)
		}
		if set, done := w.requires[id]; done {
			return set, next[id], nil
		}
		if visiting[id] {
			return nil, 0, fmt.Errorf("%s is made from itself through other records in this batch", id)
		}
		visiting[id] = true
		defer delete(visiting, id)

		set := []string{p.bucket}
		if p.memory {
			previous, version, err := w.current(ctx, id)
			if err != nil {
				return nil, 0, err
			}
			set = append(set, previous...)
			next[id] = version + 1
		}
		seen := map[input]bool{}
		for _, in := range p.inputs {
			req, version, err := of(in.id, in.version)
			if err != nil {
				return nil, 0, err
			}
			set = append(set, req...)
			// A memory in the batch has no version until it is written, so it
			// is looked up once the batch's memories are.
			_, sameBatch := batch[in.id]
			if p.memory && !seen[input{id: in.id, version: version}] {
				seen[input{id: in.id, version: version}] = true
				w.inputs[id] = append(w.inputs[id], input{id: in.id, version: version, batch: sameBatch && batch[in.id].memory && in.version == 0})
			}
		}
		w.requires[id] = union(set)
		return w.requires[id], next[id], nil
	}
	for id := range batch {
		if _, _, err := of(id, 0); err != nil {
			return err
		}
	}
	return nil
}

// stored is the requirement set of a stored record the access may read, and
// for a memory the version read: the one named, or else the current one.
func (w *writer) stored(ctx context.Context, id string, version int) ([]string, int, error) {
	var requires []string
	var found int
	err := w.tx.QueryRow(ctx, `
		select requires, 0 from entities where workspace_id = $1 and id = $2
		union all select requires, 0 from events where workspace_id = $1 and id = $2
		union all select requires, version from memories where workspace_id = $1 and id = $2
			and (case when $3 > 0 then version = $3 else superseded_at is null end)
		union all select requires, 0 from appraisals where workspace_id = $1 and id = $2
		union all select requires, 0 from links where workspace_id = $1 and id = $2
		limit 1`, w.ws, id, version).Scan(&requires, &found)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !w.access.Readable(requires)) {
		return nil, 0, fmt.Errorf("%s is not in this batch or the store", id)
	}
	return requires, found, err
}

// current is what a stored memory's current version requires, and its
// version. A memory not stored yet requires nothing and is at version 0.
func (w *writer) current(ctx context.Context, id string) ([]string, int, error) {
	var requires []string
	var version int
	err := w.tx.QueryRow(ctx, `select requires, version from memories where workspace_id = $1 and id = $2 and superseded_at is null`,
		w.ws, id).Scan(&requires, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	return requires, version, err
}

func union(sets []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, b := range sets {
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	sort.Strings(out)
	return out
}

// references refuses a reference outside the batch to a record the access
// may not read, exactly as one to a record that does not exist. Without it a
// writer could tell the two apart by whether the write succeeded. References
// the spec lets dangle are left alone: they need no stored record.
func (w *writer) references(ctx context.Context, b *store.Batch, batch map[string]planned) error {
	var refs []record.Ref
	for i := range b.Entities {
		refs = append(refs, b.Entities[i].Refs()...)
	}
	for i := range b.Events {
		refs = append(refs, b.Events[i].Refs()...)
	}
	for i := range b.Memories {
		refs = append(refs, b.Memories[i].Refs()...)
	}
	for i := range b.Appraisals {
		refs = append(refs, b.Appraisals[i].Refs()...)
	}
	for i := range b.Links {
		refs = append(refs, b.Links[i].Refs()...)
	}
	var outside []string
	for _, r := range refs {
		if _, in := batch[*r.ID]; !in && !r.MayDangle {
			outside = append(outside, *r.ID)
		}
	}
	if len(outside) == 0 {
		return nil
	}
	rows, err := w.tx.Query(ctx, `
		select id from entities where workspace_id = $1 and requires <@ $2 and id = any($3)
		union select id from events where workspace_id = $1 and requires <@ $2 and id = any($3)
		union select id from memories where workspace_id = $1 and requires <@ $2 and id = any($3)
		union select id from appraisals where workspace_id = $1 and requires <@ $2 and id = any($3)
		union select id from links where workspace_id = $1 and requires <@ $2 and id = any($3)`,
		w.ws, w.access.Read, outside)
	if err != nil {
		return err
	}
	readable := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		readable[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range outside {
		if !readable[id] {
			return fmt.Errorf("%s is not in this batch or the store", id)
		}
	}
	return nil
}

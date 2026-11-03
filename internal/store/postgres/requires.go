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
	inputs []string
}

func (w *writer) plan(ctx context.Context, b *store.Batch) error {
	w.requires = map[string][]string{}
	batch := map[string]planned{}
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
	for _, a := range b.Appraisals {
		add(a.ID, planned{bucket: a.Bucket, inputs: []string{a.Target}})
	}
	for _, l := range b.Links {
		add(l.ID, planned{bucket: l.Bucket, inputs: []string{l.From, l.To}})
	}

	if err := w.references(ctx, b, batch); err != nil {
		return err
	}

	visiting := map[string]bool{}
	var of func(id string) ([]string, error)
	of = func(id string) ([]string, error) {
		p, inBatch := batch[id]
		if !inBatch {
			return w.stored(ctx, id)
		}
		if set, done := w.requires[id]; done {
			return set, nil
		}
		if visiting[id] {
			return nil, fmt.Errorf("%s is made from itself through other records in this batch", id)
		}
		visiting[id] = true
		defer delete(visiting, id)

		set := []string{p.bucket}
		for _, in := range p.inputs {
			req, err := of(in)
			if err != nil {
				return nil, err
			}
			set = append(set, req...)
		}
		w.requires[id] = union(set)
		return w.requires[id], nil
	}
	for id := range batch {
		if _, err := of(id); err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) stored(ctx context.Context, id string) ([]string, error) {
	var requires []string
	err := w.tx.QueryRow(ctx, `
		select requires from entities where workspace_id = $1 and id = $2
		union all select requires from events where workspace_id = $1 and id = $2
		union all select requires from appraisals where workspace_id = $1 and id = $2
		union all select requires from links where workspace_id = $1 and id = $2
		limit 1`, w.ws, id).Scan(&requires)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !w.access.Readable(requires)) {
		return nil, fmt.Errorf("%s is not in this batch or the store", id)
	}
	return requires, err
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

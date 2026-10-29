package postgres

import (
	"context"
	"strings"

	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

// strip removes from records every reference to a record the access may not
// read, before they are returned. A reference to a record that does not
// exist is removed too, so the two cannot be told apart. A memory's evidence
// and dependencies stay: its requirement set already holds theirs.
func (s *Store) strip(ctx context.Context, access store.Access, entities []*record.Entity, events []*record.Event, memories []*record.Memory) error {
	var refs []record.Ref
	for _, e := range entities {
		refs = append(refs, e.Refs()...)
	}
	for _, e := range events {
		refs = append(refs, e.Refs()...)
	}
	for _, m := range memories {
		for _, r := range m.Refs() {
			if !strings.HasPrefix(r.Field, "evidence[") && !strings.HasPrefix(r.Field, "depends_on[") {
				refs = append(refs, r)
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, *r.ID)
	}
	readable, err := s.readable(ctx, access, ids)
	if err != nil {
		return err
	}
	for _, r := range refs {
		if !readable[*r.ID] {
			*r.ID = ""
		}
	}
	for _, e := range entities {
		e.MemberOf = present(e.MemberOf, func(id string) string { return id })
		if e.Audience != nil {
			e.Audience.Members = present(e.Audience.Members, func(id string) string { return id })
		}
	}
	for _, e := range events {
		e.Participants = present(e.Participants, func(p record.Participant) string { return p.Actor })
		e.Subjects = present(e.Subjects, func(s record.SubjectRef) string { return s.Subject })
		e.Involves = present(e.Involves, func(v record.Involvement) string { return v.Entity })
		e.Appraisals = present(e.Appraisals, func(id string) string { return id })
	}
	for _, m := range memories {
		m.Participants = present(m.Participants, func(p record.Participant) string { return p.Actor })
		m.Subjects = present(m.Subjects, func(s record.SubjectRef) string { return s.Subject })
		m.Involves = present(m.Involves, func(v record.Involvement) string { return v.Entity })
		m.Appraisals = present(m.Appraisals, func(id string) string { return id })
	}
	return nil
}

func present[T any](items []T, id func(T) string) []T {
	if items == nil {
		return nil
	}
	out := items[:0]
	for _, item := range items {
		if id(item) != "" {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// readable is which of these ids name a stored record the access may read
// as it stands now.
func (s *Store) readable(ctx context.Context, access store.Access, ids []string) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `
		select id from entities where workspace_id = $1 and requires <@ $2 and id = any($3)
		union select id from events where workspace_id = $1 and requires <@ $2 and id = any($3)
		union select id from memories where workspace_id = $1 and superseded_at is null and requires <@ $2 and id = any($3)
		union select id from appraisals where workspace_id = $1 and requires <@ $2 and id = any($3)
		union select id from links where workspace_id = $1 and requires <@ $2 and id = any($3)`,
		access.Workspace, access.Read, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

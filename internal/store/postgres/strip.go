package postgres

import (
	"context"

	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

func (s *Store) strip(ctx context.Context, access store.Access, entities []*record.Entity, events []*record.Event) error {
	var refs []record.Ref
	for _, e := range entities {
		if e.Class == record.ActorClass && e.Type == "team" {
			e.Type = "group"
		}
		refs = append(refs, e.Refs()...)
	}
	for _, e := range events {
		refs = append(refs, e.Refs()...)
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

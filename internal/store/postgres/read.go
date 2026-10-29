package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

func (s *Store) body(ctx context.Context, query string, into any, args ...any) error {
	var raw []byte
	err := s.pool.QueryRow(ctx, query, args...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}

// Entity is not found when the access may not read it, exactly as when it
// does not exist. Event and Memory read the same way.
func (s *Store) Entity(ctx context.Context, access store.Access, id string) (record.Entity, error) {
	var e record.Entity
	access, err := s.live(ctx, access)
	if err != nil {
		return e, err
	}
	if err := s.body(ctx, `select body from entities where workspace_id = $1 and requires <@ $2 and id = $3`, &e,
		access.Workspace, access.Read, id); err != nil {
		return e, err
	}
	return e, s.strip(ctx, access, []*record.Entity{&e}, nil, nil)
}

func (s *Store) Event(ctx context.Context, access store.Access, id string) (record.Event, error) {
	var e record.Event
	access, err := s.live(ctx, access)
	if err != nil {
		return e, err
	}
	if err := s.body(ctx, `select body from events where workspace_id = $1 and requires <@ $2 and id = $3`, &e,
		access.Workspace, access.Read, id); err != nil {
		return e, err
	}
	return e, s.strip(ctx, access, nil, []*record.Event{&e}, nil)
}

func (s *Store) Memory(ctx context.Context, access store.Access, id string, asOf *time.Time) (record.Memory, error) {
	scope := store.Scope{AsOf: asOf, Kinds: []string{store.MemoryKind}}
	found, err := s.Records(ctx, access, scope, []store.Ref{{Kind: store.MemoryKind, ID: id}})
	if err != nil {
		return record.Memory{}, err
	}
	if len(found) == 0 {
		return record.Memory{}, store.ErrNotFound
	}
	return *found[0].Memory, nil
}

func (s *Store) Kinds(ctx context.Context, access store.Access, ids []string) (map[string]record.Kind, error) {
	access, err := s.live(ctx, access)
	if err != nil {
		return nil, err
	}
	out := map[string]record.Kind{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		select r.id, r.kind, coalesce(r.class, '') from record_ids r
		where r.workspace_id = $1 and r.id = any($3) and exists (
			select 1 from entities x where x.workspace_id = $1 and x.id = r.id and x.requires <@ $2
			union all select 1 from events x where x.workspace_id = $1 and x.id = r.id and x.requires <@ $2
			union all select 1 from memories x where x.workspace_id = $1 and x.id = r.id and x.requires <@ $2
			union all select 1 from appraisals x where x.workspace_id = $1 and x.id = r.id and x.requires <@ $2
			union all select 1 from links x where x.workspace_id = $1 and x.id = r.id and x.requires <@ $2)`,
		access.Workspace, access.Read, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, kind, class string
		if err := rows.Scan(&id, &kind, &class); err != nil {
			return nil, err
		}
		out[id] = record.Kind{Record: kind, Class: record.Class(class)}
	}
	return out, rows.Err()
}

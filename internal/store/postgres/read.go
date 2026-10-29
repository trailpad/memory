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

func (s *Store) Entity(ctx context.Context, id string) (record.Entity, error) {
	var e record.Entity
	return e, s.body(ctx, `select body from entities where id = $1`, &e, id)
}

func (s *Store) Event(ctx context.Context, id string) (record.Event, error) {
	var e record.Event
	return e, s.body(ctx, `select body from events where id = $1`, &e, id)
}

func (s *Store) Memory(ctx context.Context, id string, asOf *time.Time) (record.Memory, error) {
	var m record.Memory
	var raw []byte
	var count int
	var last *time.Time
	query := `select body, recall_count, last_recalled_at from memories where id = $1 and superseded_at is null`
	args := []any{id}
	if asOf != nil {
		query = `select body, recall_count, last_recalled_at from memories
			where id = $1 and recorded_at <= $2 and (superseded_at is null or superseded_at > $2)`
		args = append(args, *asOf)
	}
	err := s.pool.QueryRow(ctx, query, args...).Scan(&raw, &count, &last)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, store.ErrNotFound
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	m.Recall = &record.Rehearsal{Count: count}
	if last != nil {
		at := record.Exact(*last)
		m.Recall.LastAt = &at
	}
	return m, nil
}

func (s *Store) Kinds(ctx context.Context, ids []string) (map[string]record.Kind, error) {
	out := map[string]record.Kind{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `select id, kind, coalesce(class, '') from record_ids where id = any($1)`, ids)
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

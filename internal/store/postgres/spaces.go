package postgres

import (
	"context"
	"encoding/json"

	"github.com/trailpad/memory/internal/store"
)

func (s *Store) CreateWorkspace(ctx context.Context, w store.Workspace) error {
	if w.ID == "" {
		return store.ErrNoAccess
	}
	var external []byte
	if w.External != nil {
		var err error
		if external, err = json.Marshal(w.External); err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `insert into workspaces (id, external) values ($1, $2)`, w.ID, external); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into buckets (workspace_id, id, name, quarantine) values ($1, $2, 'Quarantine', true)`,
		w.ID, store.QuarantineBucket); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CreateBucket(ctx context.Context, workspace string, b store.Bucket) error {
	var system, installation, container *string
	if b.Binding != nil {
		system, installation, container = &b.Binding.System, &b.Binding.Installation, &b.Binding.Container
	}
	_, err := s.pool.Exec(ctx, `
		insert into buckets (workspace_id, id, name, system, installation, container, quarantine)
		values ($1, $2, nullif($3, ''), $4, $5, $6, $7)`,
		workspace, b.ID, b.Name, system, installation, container, b.Quarantine)
	return err
}

func (s *Store) DeleteBucket(ctx context.Context, workspace, id string) error {
	tag, err := s.pool.Exec(ctx, `update buckets set deleted_at = now() where workspace_id = $1 and id = $2 and deleted_at is null`, workspace, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// live is the access with deleted buckets taken out of what it may read. A
// deleted bucket is never readable, so nothing that required it is either,
// whatever the caller was told when it worked out the access.
func (s *Store) live(ctx context.Context, access store.Access) (store.Access, error) {
	if err := access.Check(); err != nil {
		return access, err
	}
	if len(access.Read) == 0 {
		return access, nil
	}
	var read []string
	err := s.pool.QueryRow(ctx, `select coalesce(array_agg(id order by id), '{}') from buckets
		where workspace_id = $1 and id = any($2) and deleted_at is null`, access.Workspace, access.Read).Scan(&read)
	access.Read = read
	return access, err
}

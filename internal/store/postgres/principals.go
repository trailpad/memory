package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/trailpad/memory/internal/access"
	"github.com/trailpad/memory/internal/store"
)

func (s *Store) ServiceByKey(ctx context.Context, hash []byte) (access.Service, error) {
	var svc access.Service
	err := s.pool.QueryRow(ctx, `select workspace_id, id, actions from services
		where key_hash = $1 and revoked_at is null`, hash).
		Scan(&svc.Workspace, &svc.ID, &svc.Actions)
	if errors.Is(err, pgx.ErrNoRows) {
		return svc, store.ErrNotFound
	}
	if err != nil {
		return svc, err
	}
	rows, err := s.pool.Query(ctx, `select system, installation, accounts from installations
		where workspace_id = $1 and service_id = $2 order by system, installation`, svc.Workspace, svc.ID)
	if err != nil {
		return svc, err
	}
	svc.Installations, err = pgx.CollectRows(rows, pgx.RowToStructByPos[access.Installation])
	return svc, err
}

func (s *Store) Issuer(ctx context.Context, workspace, issuer string) (access.Issuer, error) {
	var is access.Issuer
	var key []byte
	err := s.pool.QueryRow(ctx, `select issuer, service_id, public_key, attests from issuers
		where workspace_id = $1 and issuer = $2 and removed_at is null`, workspace, issuer).
		Scan(&is.Issuer, &is.Service, &key, &is.Attests)
	if errors.Is(err, pgx.ErrNoRows) {
		return is, store.ErrNotFound
	}
	is.PublicKey = key
	return is, err
}

func (s *Store) TokenTTL(ctx context.Context, workspace string) (time.Duration, error) {
	var ttl time.Duration
	err := s.pool.QueryRow(ctx, `select token_ttl from workspaces where id = $1`, workspace).Scan(&ttl)
	return ttl, err
}

func (s *Store) Person(ctx context.Context, workspace, issuer, sub string, accounts []access.Account) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var principal string
	err = tx.QueryRow(ctx, `select principal_id from subjects where workspace_id = $1 and issuer = $2 and sub = $3`,
		workspace, issuer, sub).Scan(&principal)
	if errors.Is(err, pgx.ErrNoRows) {
		principal = newID("pr_")
		if _, err := tx.Exec(ctx, `insert into principals (workspace_id, id, type) values ($1, $2, 'person')`, workspace, principal); err != nil {
			return "", err
		}
		// A concurrent first sign-in may have won; whichever row stands is the principal.
		if _, err := tx.Exec(ctx, `insert into subjects (workspace_id, issuer, sub, principal_id) values ($1, $2, $3, $4)
			on conflict do nothing`, workspace, issuer, sub, principal); err != nil {
			return "", err
		}
		err = tx.QueryRow(ctx, `select principal_id from subjects where workspace_id = $1 and issuer = $2 and sub = $3`,
			workspace, issuer, sub).Scan(&principal)
	}
	if err != nil {
		return "", err
	}
	for _, acc := range accounts {
		if _, err := tx.Exec(ctx, `insert into account_links (workspace_id, system, installation, account, principal_id)
			values ($1, $2, $3, $4, $5) on conflict do nothing`,
			workspace, acc.System, acc.Installation, acc.Account, principal); err != nil {
			return "", err
		}
		var owner string
		if err := tx.QueryRow(ctx, `select principal_id from account_links
			where workspace_id = $1 and system = $2 and installation = $3 and account = $4`,
			workspace, acc.System, acc.Installation, acc.Account).Scan(&owner); err != nil {
			return "", err
		}
		if owner != principal {
			if err := logChange(ctx, tx, workspace, issuer, principal, "link refused", acc); err != nil {
				return "", err
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return principal, nil
}

func (s *Store) Grants(ctx context.Context, workspace, principal string, at time.Time) (read, write []string, err error) {
	err = s.pool.QueryRow(ctx, `
		with readable as (
		  select bucket_id from grants
		   where workspace_id = $1 and principal_id = $2 and 'read' = any(rights)
		  union
		  select r.bucket_id from synced_readers r
		    join account_links l on l.workspace_id = r.workspace_id and l.system = r.system
		     and l.installation = r.installation and l.account = r.account
		   where r.workspace_id = $1 and l.principal_id = $2 and r.expires_at > $3
		), writable as (
		  select bucket_id from grants
		   where workspace_id = $1 and principal_id = $2 and 'write' = any(rights)
		)
		select
		  coalesce((select array_agg(b.id order by b.id) from buckets b join readable r on r.bucket_id = b.id
		            where b.workspace_id = $1 and b.deleted_at is null), '{}'),
		  coalesce((select array_agg(b.id order by b.id) from buckets b join writable w on w.bucket_id = b.id
		            where b.workspace_id = $1 and b.deleted_at is null), '{}')`,
		workspace, principal, at).Scan(&read, &write)
	return read, write, err
}

// CreateService registers a service principal with a key hash. The key itself
// is never stored.
func (s *Store) CreateService(ctx context.Context, svc access.Service, hash []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `insert into principals (workspace_id, id, type, name) values ($1, $2, 'service', $2)`,
		svc.Workspace, svc.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into services (workspace_id, id, key_hash, actions) values ($1, $2, $3, $4)`,
		svc.Workspace, svc.ID, hash, svc.Actions); err != nil {
		return err
	}
	for _, in := range svc.Installations {
		if in.System == "" || in.Installation == "" || in.Accounts == "" {
			return errors.New("an installation needs a system, an id and an account namespace")
		}
		if _, err := tx.Exec(ctx, `insert into installations (workspace_id, service_id, system, installation, accounts) values ($1, $2, $3, $4, $5)`,
			svc.Workspace, svc.ID, in.System, in.Installation, in.Accounts); err != nil {
			return err
		}
	}
	if err := logChange(ctx, tx, svc.Workspace, "operator", "", "service created", svc); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AddIssuer(ctx context.Context, workspace string, is access.Issuer) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `insert into issuers (workspace_id, issuer, service_id, public_key, attests) values ($1, $2, $3, $4, $5)`,
		workspace, is.Issuer, is.Service, []byte(is.PublicKey), nonNil(is.Attests)); err != nil {
		return err
	}
	if err := logChange(ctx, tx, workspace, "operator", "", "issuer added", map[string]any{"issuer": is.Issuer, "service": is.Service, "attests": is.Attests}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Grant sets a principal's manual rights on a bucket; no rights removes the
// grant.
func (s *Store) Grant(ctx context.Context, workspace, bucket, principal string, rights []string, by string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(rights) == 0 {
		_, err = tx.Exec(ctx, `delete from grants where workspace_id = $1 and bucket_id = $2 and principal_id = $3`, workspace, bucket, principal)
	} else {
		_, err = tx.Exec(ctx, `insert into grants (workspace_id, bucket_id, principal_id, rights, created_by) values ($1, $2, $3, $4, $5)
			on conflict (workspace_id, bucket_id, principal_id) do update set rights = excluded.rights, created_by = excluded.created_by, created_at = now()`,
			workspace, bucket, principal, rights, by)
	}
	if err != nil {
		return err
	}
	if err := logChange(ctx, tx, workspace, by, "", "grant set", map[string]any{"bucket": bucket, "principal": principal, "rights": rights}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BindBucket creates a bucket bound to a container in one of the service's
// installations, or finds the one already bound to it, and lets the service
// read and write it. It returns the bucket's id, which the engine picks, so a
// connector cannot learn which ids exist by asking for them.
func (s *Store) BindBucket(ctx context.Context, svc access.Service, b store.Bucket) (string, error) {
	if b.Binding == nil {
		return "", access.ErrForbidden
	}
	if _, ok := svc.Installed(b.Binding.System, b.Binding.Installation); !ok {
		return "", access.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	err = tx.QueryRow(ctx, `select id from buckets where workspace_id = $1 and system = $2 and installation = $3 and container = $4`,
		svc.Workspace, b.Binding.System, b.Binding.Installation, b.Binding.Container).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		id = newID("bk_")
		if _, err := tx.Exec(ctx, `insert into buckets (workspace_id, id, name, system, installation, container)
			values ($1, $2, nullif($3, ''), $4, $5, $6)`,
			svc.Workspace, id, b.Name, b.Binding.System, b.Binding.Installation, b.Binding.Container); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `insert into grants (workspace_id, bucket_id, principal_id, rights, created_by)
			values ($1, $2, $3, array['read', 'write'], $3)`, svc.Workspace, id, svc.ID); err != nil {
			return "", err
		}
		if err := logChange(ctx, tx, svc.Workspace, svc.ID, "", "bucket bound", map[string]any{"bucket": id, "binding": b.Binding}); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

// SyncReaders replaces a bound bucket's synced readers with the complete list
// a connector read from the source, and renews them for the workspace's
// synced grant lifetime.
func (s *Store) SyncReaders(ctx context.Context, svc access.Service, bucket string, accounts []access.Account, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var system, installation *string
	err = tx.QueryRow(ctx, `select system, installation from buckets where workspace_id = $1 and id = $2 and deleted_at is null for update`,
		svc.Workspace, bucket).Scan(&system, &installation)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return err
	}
	// A bucket outside the connector's installations answers as one that
	// does not exist.
	if system == nil || installation == nil {
		return store.ErrNotFound
	}
	in, ok := svc.Installed(*system, *installation)
	if !ok {
		return store.ErrNotFound
	}
	for _, acc := range accounts {
		if acc.System != in.System || acc.Installation != in.Accounts || acc.Account == "" {
			return access.ErrForbidden
		}
	}
	if _, err := tx.Exec(ctx, `delete from synced_readers where workspace_id = $1 and bucket_id = $2`, svc.Workspace, bucket); err != nil {
		return err
	}
	for _, acc := range accounts {
		if _, err := tx.Exec(ctx, `insert into synced_readers (workspace_id, bucket_id, system, installation, account, expires_at)
			select $1, $2, $3, $4, $5, $6::timestamptz + synced_ttl from workspaces where id = $1
			on conflict do nothing`,
			svc.Workspace, bucket, acc.System, acc.Installation, acc.Account, at); err != nil {
			return err
		}
	}
	if err := logChange(ctx, tx, svc.Workspace, svc.ID, "", "readers synced", map[string]any{"bucket": bucket, "readers": len(accounts)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func logChange(ctx context.Context, tx pgx.Tx, workspace, actor, onBehalfOf, change string, detail any) error {
	d, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `insert into access_log (workspace_id, actor, on_behalf_of, change, detail) values ($1, $2, nullif($3, ''), $4, $5)`,
		workspace, actor, onBehalfOf, change, d)
	return err
}

func newID(prefix string) string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

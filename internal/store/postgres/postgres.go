// Package postgres keeps records in PostgreSQL.
package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/trailpad/memory/internal/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is a store.Store over a PostgreSQL pool.
type Store struct {
	pool              *pgxpool.Pool
	EmbeddingLog      *slog.Logger
	embeddingLogMu    sync.Mutex
	embeddingLogAt    time.Time
	embeddingLogCount int
}

// Open connects and brings the schema up to date.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	s := &Store{pool: pool}
	if err := s.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return s, nil
}

// New wraps a pool whose schema is already up to date.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Close() { s.pool.Close() }

// Pool is the connection pool, for tooling and tests that inspect tables.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

type migration struct {
	version int
	up      string
	down    string
}

func load() ([]migration, error) {
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	byVersion := map[int]*migration{}
	for _, f := range files {
		name := strings.TrimPrefix(f, "migrations/")
		number, rest, ok := strings.Cut(name, "_")
		version, err := strconv.Atoi(number)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %s does not start with a number", name)
		}
		body, err := migrations.ReadFile(f)
		if err != nil {
			return nil, err
		}
		m := byVersion[version]
		if m == nil {
			m = &migration{version: version}
			byVersion[version] = m
		}
		switch {
		case strings.HasSuffix(rest, ".up.sql"):
			m.up = string(body)
		case strings.HasSuffix(rest, ".down.sql"):
			m.down = string(body)
		}
	}
	out := make([]migration, 0, len(byVersion))
	for _, m := range byVersion {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// migrationLock is the advisory lock key held while migrating, so several
// processes starting against one database apply migrations one at a time.
const migrationLock = 7_301_118

// locked runs fn on one connection while holding the migration lock.
func (s *Store) locked(ctx context.Context, fn func(*pgxpool.Conn) error) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, migrationLock); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, migrationLock) }()
	return fn(conn)
}

// Migrate applies every migration not yet applied, each in its own transaction.
func (s *Store) Migrate(ctx context.Context) error {
	return s.locked(ctx, func(conn *pgxpool.Conn) error { return migrate(ctx, conn) })
}

func migrate(ctx context.Context, conn *pgxpool.Conn) error {
	if _, err := conn.Exec(ctx, `create table if not exists schema_migrations (version integer primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}
	all, err := load()
	if err != nil {
		return err
	}
	for _, m := range all {
		var done bool
		if err := conn.QueryRow(ctx, `select exists (select 1 from schema_migrations where version = $1)`, m.version).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, m.up); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(ctx, `insert into schema_migrations (version) values ($1)`, m.version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Rollback undoes the most recent migration.
func (s *Store) Rollback(ctx context.Context) error {
	return s.locked(ctx, func(conn *pgxpool.Conn) error { return rollback(ctx, conn) })
}

func rollback(ctx context.Context, conn *pgxpool.Conn) error {
	all, err := load()
	if err != nil {
		return err
	}
	var latest int
	if err := conn.QueryRow(ctx, `select coalesce(max(version), 0) from schema_migrations`).Scan(&latest); err != nil {
		return err
	}
	for _, m := range all {
		if m.version != latest {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, m.down); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("undo migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(ctx, `delete from schema_migrations where version = $1`, m.version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		return tx.Commit(ctx)
	}
	return nil
}

var (
	_ store.Store    = (*Store)(nil)
	_ store.Recaller = (*Store)(nil)
	_ store.Spaces   = (*Store)(nil)
)

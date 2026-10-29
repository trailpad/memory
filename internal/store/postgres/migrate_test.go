package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/postgres"
	"github.com/trailpad/memory/internal/store/postgres/pgtest"
)

func exists(t *testing.T, s *postgres.Store, table string) bool {
	t.Helper()
	var found bool
	if err := s.Pool().QueryRow(context.Background(), `select to_regclass($1) is not null`, table).Scan(&found); err != nil {
		t.Fatal(err)
	}
	return found
}

func TestMigrationsGoDownAndBackUp(t *testing.T) {
	s := pgtest.ForTest(t)
	ctx := context.Background()
	// Each migration in turn, latest first, with a table it adds.
	for _, table := range []string{"principals", "workspaces", "rehearsals", "record_ids"} {
		if err := s.Rollback(ctx); err != nil {
			t.Fatalf("rolling back %s: %v", table, err)
		}
		if exists(t, s, table) {
			t.Fatalf("%s is still there after rolling back the migration that added it", table)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	for _, table := range []string{"record_ids", "rehearsals", "workspaces", "dependencies", "principals", "grants"} {
		if !exists(t, s, table) {
			t.Fatalf("%s is missing after migrating again", table)
		}
	}
}

// legacy rolls back to before workspaces and stores one memory used three
// times, as the counters before rehearsals kept it.
func legacy(t *testing.T) *postgres.Store {
	t.Helper()
	s := pgtest.ForTest(t)
	ctx := context.Background()
	for range 3 {
		if err := s.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Pool().Exec(ctx, `
		insert into record_ids (id, kind) values ('mm_x', 'memory');
		insert into memories (id, version, type, statement, body, said, recorded_at, recall_count, last_recalled_at)
		values ('mm_x', 1, 'crm.objection', 'x', '{"id":"mm_x","type":"crm.objection","statement":"x"}', to_tsvector('x'), now(), 3, '2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUsesCountedBeforeTheUpgradeAreKept(t *testing.T) {
	s := legacy(t)
	ctx := context.Background()
	var uses int
	var last string
	if err := s.Pool().QueryRow(ctx, `select count(*), max(at)::date::text from rehearsals where memory_id = 'mm_x'`).Scan(&uses, &last); err != nil {
		t.Fatal(err)
	}
	if uses != 3 || last != "2026-09-01" {
		t.Fatalf("three uses, the last on 1 September, got %d on %s", uses, last)
	}
	var lineage []string
	if err := s.Pool().QueryRow(ctx, `select type_lineage from memories where id = 'mm_x'`).Scan(&lineage); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lineage, " ") != "crm.objection crm" {
		t.Fatalf("earlier rows get their namespace parents, got %v", lineage)
	}
}

func TestRecordsFromBeforeWorkspacesAreQuarantined(t *testing.T) {
	s := legacy(t)
	ctx := context.Background()
	outside := store.Access{Workspace: "default", Principal: "pr_ana", Read: []string{"bk_anything"}}
	if _, err := s.Memory(ctx, outside, "mm_x", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("nobody established who may read it, so only quarantine readers see it, got %v", err)
	}
	inside := store.Access{Workspace: "default", Principal: "pr_admin", Read: []string{store.QuarantineBucket}}
	m, err := s.Memory(ctx, inside, "mm_x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Recall.Count != 0 {
		t.Fatalf("uses counted before uses were kept per principal belong to nobody, got %d", m.Recall.Count)
	}
}

func TestQuarantinedBodiesNameTheirBucket(t *testing.T) {
	s := legacy(t)
	inside := store.Access{Workspace: "default", Principal: "pr_admin", Read: []string{store.QuarantineBucket}}
	m, err := s.Memory(context.Background(), inside, "mm_x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Bucket != store.QuarantineBucket {
		t.Fatalf("a migrated record reads back in quarantine, got %q", m.Bucket)
	}
}

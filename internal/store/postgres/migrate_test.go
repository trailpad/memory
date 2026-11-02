package postgres_test

import (
	"context"
	"testing"

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
	if err := s.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"memories", "evidence", "dependencies"} {
		if !exists(t, s, table) {
			t.Fatalf("rollback did not recreate %s", table)
		}
	}
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
	for _, table := range []string{"record_ids", "rehearsals", "workspaces", "principals", "grants"} {
		if !exists(t, s, table) {
			t.Fatalf("%s is missing after migrating again", table)
		}
	}
}

func TestEpisodicMigrationPreservesEvents(t *testing.T) {
	s := pgtest.ForTest(t)
	ctx := t.Context()
	if err := s.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool().Exec(ctx, `
 insert into record_ids (workspace_id, id, kind) values ('default', 'ev_legacy', 'event'), ('default', 'mm_legacy', 'memory');
 insert into events (workspace_id, id, type, body, said, recorded_at, bucket_id, requires)
 values ('default', 'ev_legacy', 'meeting', '{"bucket":"quarantine","id":"ev_legacy","type":"meeting","appraisals":["ap_later"]}', to_tsvector('meeting'), now(), 'quarantine', '{quarantine}');
 insert into memories (workspace_id, id, version, type, statement, body, said, recorded_at, bucket_id, requires)
 values ('default', 'mm_legacy', 1, 'fact', 'old fact', '{}', to_tsvector('old fact'), now(), 'quarantine', '{quarantine}');
 insert into evidence (workspace_id, memory_id, version, event_id, role) values ('default', 'mm_legacy', 1, 'ev_legacy', 'established');
 insert into dependencies (workspace_id, memory_id, version, input_id) values ('default', 'mm_legacy', 1, 'ev_legacy');
 insert into rehearsals (workspace_id, memory_id, principal, at) values ('default', 'mm_legacy', 'reader', now());`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"memories", "evidence", "dependencies"} {
		if exists(t, s, table) {
			t.Fatalf("%s remains", table)
		}
	}
	var body string
	if err := s.Pool().QueryRow(ctx, `select body::text from events where workspace_id = 'default' and id = 'ev_legacy'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if body != `{"id": "ev_legacy", "type": "meeting", "bucket": "quarantine"}` {
		t.Fatalf("event body: %s", body)
	}
	var records, uses int
	if err := s.Pool().QueryRow(ctx, `select (select count(*) from record_ids where kind = 'memory'), (select count(*) from rehearsals)`).Scan(&records, &uses); err != nil {
		t.Fatal(err)
	}
	if records != 0 || uses != 0 {
		t.Fatalf("semantic records: %d, uses: %d", records, uses)
	}
	if err := s.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

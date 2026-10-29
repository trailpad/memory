package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/trailpad/memory/internal/store/postgres/pgtest"
)

func TestMigrationsGoDownAndBackUp(t *testing.T) {
	s := pgtest.ForTest(t)
	ctx := context.Background()
	if err := s.Rollback(ctx); err != nil {
		t.Fatalf("down one: %v", err)
	}
	if err := s.Rehearse(ctx, []string{"x"}, time.Now()); err == nil {
		t.Fatal("the latest migration's table is still there after rolling it back")
	}
	if _, err := s.Kinds(ctx, []string{"x"}); err != nil {
		t.Fatalf("the earlier migration's tables went too: %v", err)
	}
	if err := s.Rollback(ctx); err != nil {
		t.Fatalf("down two: %v", err)
	}
	if _, err := s.Kinds(ctx, []string{"x"}); err == nil {
		t.Fatal("tables are still there after rolling everything back")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if _, err := s.Kinds(ctx, []string{"x"}); err != nil {
		t.Fatalf("tables are missing after migrating again: %v", err)
	}
	if err := s.Rehearse(ctx, []string{"x"}, time.Now()); err != nil {
		t.Fatalf("rehearsals are missing after migrating again: %v", err)
	}
}

func TestUsesCountedBeforeTheUpgradeAreKept(t *testing.T) {
	s := pgtest.ForTest(t)
	ctx := context.Background()
	if err := s.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	last := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Pool().Exec(ctx, `
		insert into record_ids (id, kind) values ('mm_x', 'memory');
		insert into memories (id, version, type, statement, body, said, recorded_at, recall_count, last_recalled_at)
		values ('mm_x', 1, 'crm.objection', 'x', '{"id":"mm_x","type":"crm.objection","statement":"x"}', to_tsvector('x'), now(), 3, '2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := s.Memory(ctx, "mm_x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Recall.Count != 3 || m.Recall.LastAt == nil || !m.Recall.LastAt.At.Equal(last) {
		t.Fatalf("three uses, the last on 1 September, got %+v", m.Recall)
	}
	var lineage []string
	if err := s.Pool().QueryRow(ctx, `select type_lineage from memories where id = 'mm_x'`).Scan(&lineage); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lineage, " ") != "crm.objection crm" {
		t.Fatalf("earlier rows get their namespace parents, got %v", lineage)
	}
}

package postgres_test

import (
	"context"
	"testing"

	"github.com/trailpad/memory/internal/store/postgres/pgtest"
)

func TestMigrationsGoDownAndBackUp(t *testing.T) {
	s := pgtest.ForTest(t)
	ctx := context.Background()
	if err := s.Rollback(ctx); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := s.Kinds(ctx, []string{"x"}); err == nil {
		t.Fatal("tables are still there after rolling back")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if _, err := s.Kinds(ctx, []string{"x"}); err != nil {
		t.Fatalf("tables are missing after migrating again: %v", err)
	}
}

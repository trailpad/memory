// Package pgtest gives tests a migrated store in a schema of their own.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/postgres"
)

// TestURLVariable names the database tests run against. Each test gets its
// own schema in it, dropped when the test ends.
const TestURLVariable = "MEMORY_TEST_DATABASE_URL"

// RequireVariable, when set, turns a missing database into a failure, so CI
// cannot pass by skipping every storage test.
const RequireVariable = "MEMORY_REQUIRE_DATABASE"

// ForTest opens a store in a fresh schema, migrated, or skips the test when no
// test database is configured.
func ForTest(t testing.TB) *postgres.Store {
	t.Helper()
	url := os.Getenv(TestURLVariable)
	if url == "" {
		if os.Getenv(RequireVariable) != "" {
			t.Fatalf("%s is set but %s is empty", RequireVariable, TestURLVariable)
		}
		t.Skipf("set %s to run tests against PostgreSQL", TestURLVariable)
	}
	ctx := context.Background()
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	schema := "test_" + hex.EncodeToString(suffix)

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `create schema `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `drop schema `+schema+` cascade`)
		admin.Close()
	})

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ", public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := postgres.New(pool)
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

// Workspace creates a workspace with these buckets and returns an access
// that may read and write all of them, acting as principal.
func Workspace(t testing.TB, s *postgres.Store, id, principal string, buckets ...string) store.Access {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateWorkspace(ctx, store.Workspace{ID: id}); err != nil {
		t.Fatal(err)
	}
	for _, b := range buckets {
		if err := s.CreateBucket(ctx, id, store.Bucket{ID: b}); err != nil {
			t.Fatal(err)
		}
	}
	return store.Access{Workspace: id, Principal: principal, Read: buckets, Write: buckets}
}

// InBucket puts every record in the batch that names no bucket into this
// one.
func InBucket(b store.Batch, bucket string) store.Batch {
	for i := range b.Entities {
		if b.Entities[i].Bucket == "" {
			b.Entities[i].Bucket = bucket
		}
	}
	for i := range b.Events {
		if b.Events[i].Bucket == "" {
			b.Events[i].Bucket = bucket
		}
	}
	for i := range b.Memories {
		if b.Memories[i].Bucket == "" {
			b.Memories[i].Bucket = bucket
		}
	}
	for i := range b.Appraisals {
		if b.Appraisals[i].Bucket == "" {
			b.Appraisals[i].Bucket = bucket
		}
	}
	for i := range b.Links {
		if b.Links[i].Bucket == "" {
			b.Links[i].Bucket = bucket
		}
	}
	return b
}

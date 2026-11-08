package vectortest

import (
	"fmt"
	"testing"
	"time"

	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

func Run(t *testing.T, constructor func(*testing.T) store.VectorIndex) {
	t.Helper()
	t.Run("operations", func(t *testing.T) {
		index := constructor(t)
		ctx := t.Context()
		at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
		rows := []store.EventVector{
			{Workspace: "ws", EventID: "a", BucketID: "small", OccurredAt: &at, Type: "review.comment", Model: "first", Vector: []float32{1, 0, 0}},
			{Workspace: "ws", EventID: "b", BucketID: "small", Model: "first", Vector: []float32{0.9, 0.1, 0}},
			{Workspace: "ws", EventID: "c", BucketID: "large", Model: "first", Vector: []float32{1, 0, 0}},
			{Workspace: "other", EventID: "d", BucketID: "small", Model: "first", Vector: []float32{1, 0, 0}},
			{Workspace: "ws", EventID: "e", BucketID: "small", Model: "second", Vector: []float32{1, 0, 0}},
		}
		if err := index.Upsert(ctx, rows); err != nil {
			t.Fatal(err)
		}
		check := func(model, bucket string, want []string) {
			t.Helper()
			got, err := index.Nearest(ctx, "ws", model, []float32{1, 0, 0}, store.VectorFilter{Buckets: []string{bucket}, K: 10})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(want) {
				t.Fatalf("got %+v, want %v", got, want)
			}
			for i, id := range want {
				if got[i].EventID != id {
					t.Fatalf("got %+v, want %v", got, want)
				}
			}
		}
		check("first", "small", []string{"a", "b"})
		filtered, err := index.Nearest(ctx, "ws", "first", []float32{1, 0, 0}, store.VectorFilter{Buckets: []string{"small"}, Types: []record.Type{"review"}, From: &at, K: 10})
		if err != nil || len(filtered) != 1 || filtered[0].EventID != "a" {
			t.Fatalf("type and date filters: %+v %v", filtered, err)
		}
		filtered, err = index.Nearest(ctx, "ws", "first", []float32{1, 0, 0}, store.VectorFilter{Buckets: []string{"small"}, To: &at, K: 10})
		if err != nil || len(filtered) != 0 {
			t.Fatalf("exclusive upper bound: %+v %v", filtered, err)
		}
		check("second", "small", []string{"e"})
		check("first", "large", []string{"c"})
		rows[1].Vector = []float32{0, 1, 0}
		if err := index.Upsert(ctx, rows[1:2]); err != nil {
			t.Fatal(err)
		}
		got, err := index.Nearest(ctx, "ws", "first", []float32{1, 0, 0}, store.VectorFilter{Buckets: []string{"small"}, K: 2})
		if err != nil || len(got) != 2 || got[1].Cosine != 0 {
			t.Fatalf("upsert: %+v %v", got, err)
		}
		if err := index.Delete(ctx, "ws", []string{"a"}); err != nil {
			t.Fatal(err)
		}
		check("first", "small", []string{"b"})
	})
	t.Run("small_bucket", func(t *testing.T) {
		index := constructor(t)
		ctx := t.Context()
		rows := []store.EventVector{}
		for i := 0; i < 10000; i++ {
			rows = append(rows, store.EventVector{Workspace: "ws", EventID: fmt.Sprintf("large_%05d", i), BucketID: "large", Model: "first", Vector: []float32{1, float32(i%100) / 100000, 0}})
		}
		for i := 0; i < 8; i++ {
			rows = append(rows, store.EventVector{Workspace: "ws", EventID: fmt.Sprintf("small_%d", i), BucketID: "small", Model: "first", Vector: []float32{0.8, float32(i+1) / 100, 0.1}})
		}
		if err := index.Upsert(ctx, rows); err != nil {
			t.Fatal(err)
		}
		got, err := index.Nearest(ctx, "ws", "first", []float32{1, 0, 0}, store.VectorFilter{Buckets: []string{"small"}, K: 8})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 8 {
			t.Fatalf("small bucket returned %d of 8", len(got))
		}
		for i, r := range got {
			if r.EventID != fmt.Sprintf("small_%d", i) {
				t.Fatalf("order: %+v", got)
			}
		}
	})
}

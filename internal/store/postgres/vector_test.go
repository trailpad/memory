package postgres_test

import (
	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/postgres/pgtest"
	"github.com/trailpad/memory/internal/store/vectortest"
	"testing"
)

func TestPostgresVectorConformance(t *testing.T) {
	vectortest.Run(t, func(t *testing.T) store.VectorIndex {
		s := pgtest.ForTest(t)
		index, err := s.NewVectorIndex(t.Context(), 3)
		if err != nil {
			t.Fatal(err)
		}
		return index
	})
}

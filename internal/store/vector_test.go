package store_test

import (
	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/vectortest"
	"testing"
)

func TestMemoryVectorConformance(t *testing.T) {
	vectortest.Run(t, func(t *testing.T) store.VectorIndex { return store.NewMemoryVectorIndex() })
}

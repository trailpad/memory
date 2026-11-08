package store

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/trailpad/memory/internal/record"
)

type EventVector struct {
	Workspace, EventID, BucketID string
	OccurredAt                   *time.Time
	Type                         record.Type
	Model                        string
	Vector                       []float32
}
type VectorFilter struct {
	Buckets  []string
	From, To *time.Time
	Types    []record.Type
	K        int
}
type VectorMatch struct {
	EventID string
	Cosine  float64
}
type VectorIndex interface {
	Upsert(context.Context, []EventVector) error
	Nearest(context.Context, string, string, []float32, VectorFilter) ([]VectorMatch, error)
	Delete(context.Context, string, []string) error
}

type MemoryVectorIndex struct {
	mu   sync.RWMutex
	rows map[[3]string]EventVector
}

func NewMemoryVectorIndex() *MemoryVectorIndex {
	return &MemoryVectorIndex{rows: map[[3]string]EventVector{}}
}
func (m *MemoryVectorIndex) Upsert(ctx context.Context, rows []EventVector) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range rows {
		row.Vector = slices.Clone(row.Vector)
		m.rows[[3]string{row.Workspace, row.EventID, row.Model}] = row
	}
	return nil
}
func (m *MemoryVectorIndex) Delete(ctx context.Context, workspace string, ids []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.rows {
		if key[0] == workspace && slices.Contains(ids, key[1]) {
			delete(m.rows, key)
		}
	}
	return nil
}
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}
func (m *MemoryVectorIndex) Nearest(ctx context.Context, workspace, model string, vector []float32, f VectorFilter) ([]VectorMatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.K < 1 {
		return nil, errors.New("vector k must be positive")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []VectorMatch{}
	for _, r := range m.rows {
		if r.Workspace != workspace || r.Model != model || len(r.Vector) != len(vector) || !slices.Contains(f.Buckets, r.BucketID) {
			continue
		}
		if f.From != nil && (r.OccurredAt == nil || r.OccurredAt.Before(*f.From)) {
			continue
		}
		if f.To != nil && (r.OccurredAt == nil || !r.OccurredAt.Before(*f.To)) {
			continue
		}
		if len(f.Types) > 0 {
			match := false
			for _, t := range f.Types {
				if slices.Contains(r.Type.Lineage(), t) {
					match = true
				}
			}
			if !match {
				continue
			}
		}
		out = append(out, VectorMatch{r.EventID, Cosine(vector, r.Vector)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cosine != out[j].Cosine {
			return out[i].Cosine > out[j].Cosine
		}
		return out[i].EventID < out[j].EventID
	})
	if len(out) > f.K {
		out = out[:f.K]
	}
	return out, nil
}

type BatchVectorIndex interface {
	NearestBatch(context.Context, string, string, [][]float32, VectorFilter) ([][]VectorMatch, error)
}

func (m *MemoryVectorIndex) NearestBatch(ctx context.Context, workspace, model string, vectors [][]float32, filter VectorFilter) ([][]VectorMatch, error) {
	out := make([][]VectorMatch, len(vectors))
	for i, v := range vectors {
		rows, err := m.Nearest(ctx, workspace, model, v, filter)
		if err != nil {
			return nil, err
		}
		out[i] = rows
	}
	return out, nil
}

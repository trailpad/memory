package store

import (
	"context"
	"slices"
	"sync"
)

type LinkCandidate struct {
	Cosine                   float64
	Model                    string
	From, To, Approach       string
	Shared, Path             []string
	Value                    float64
	Frequency, Total         int
	LinkID, LinkFrom, LinkTo string
	Truncated                bool
	Type                     string
}

type Linker interface {
	LinkCandidates(context.Context, Access, Scope, []string, []string) ([]LinkCandidate, error)
}

type truncationKey struct{}
type truncation struct {
	sync.Mutex
	names []string
}

func WithLinkTruncation(ctx context.Context) context.Context {
	return context.WithValue(ctx, truncationKey{}, &truncation{})
}
func ReportLinkTruncation(ctx context.Context, name string) {
	if t, ok := ctx.Value(truncationKey{}).(*truncation); ok {
		t.Lock()
		defer t.Unlock()
		if !slices.Contains(t.names, name) {
			t.names = append(t.names, name)
		}
	}
}
func LinkTruncation(ctx context.Context) []string {
	if t, ok := ctx.Value(truncationKey{}).(*truncation); ok {
		t.Lock()
		defer t.Unlock()
		out := slices.Clone(t.names)
		slices.Sort(out)
		return out
	}
	return nil
}

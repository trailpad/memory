package recall

import (
	"context"
	"math"
	"slices"

	"github.com/trailpad/memory/internal/embed"
	"github.com/trailpad/memory/internal/store"
)

func meaningScore(cosine, floor float64) float64 {
	return math.Max(0, math.Min(1, (cosine-floor)/(1-floor)))
}
func (s *Service) floor() float64 {
	if s.MeaningFloor == nil {
		return 0.6
	}
	return *s.MeaningFloor
}
func (s *Service) rawNearest(ctx context.Context, access store.Access, scope store.Scope, vector []float32, k int) ([]store.VectorMatch, error) {
	buckets := []string{}
	for _, b := range access.Read {
		if len(scope.Buckets) == 0 || slices.Contains(scope.Buckets, b) {
			buckets = append(buckets, b)
		}
	}
	// Vector backends narrow the search; the record store remains the access authority.
	matches, err := s.Vectors.Nearest(ctx, access.Workspace, s.Embedder.Model(), vector, store.VectorFilter{Buckets: buckets, From: scope.OccurredFrom, To: scope.OccurredTo, Types: scope.Types, K: min(k*8+1, 10000)})
	if err != nil {
		return nil, err
	}
	return matches, nil
}
func (s *Service) nearest(ctx context.Context, access store.Access, scope store.Scope, vector []float32, k int) ([]store.VectorMatch, error) {
	matches, err := s.rawNearest(ctx, access, scope, vector, k)
	if err != nil {
		return nil, err
	}

	refs := make([]store.Ref, len(matches))
	for i, m := range matches {
		refs[i] = store.Ref{ID: m.EventID}
	}
	found, err := s.Store.Records(ctx, access, scope, refs)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, f := range found {
		allowed[f.Ref.ID] = true
	}
	out := []store.VectorMatch{}
	seen := map[string]bool{}
	for _, m := range matches {
		if allowed[m.EventID] && !seen[m.EventID] {
			seen[m.EventID] = true
			out = append(out, m)
			if len(out) == k {
				break
			}
		}
	}
	return out, nil
}
func (s *Service) meaningLinks(ctx context.Context, access store.Access, scope store.Scope, anchors []string, floor float64) ([]store.LinkCandidate, error) {
	if s.Embedder == nil || s.Vectors == nil {
		return nil, nil
	}
	refs := make([]store.Ref, len(anchors))
	for i, id := range anchors {
		refs[i] = store.Ref{ID: id}
	}
	found, err := s.Store.Records(ctx, access, scope, refs)
	if err != nil {
		return nil, err
	}
	texts := make([]string, len(found))
	for i, f := range found {
		texts[i] = embed.EventText(*f.Event)
	}
	if len(texts) == 0 {
		return nil, nil
	}
	vectors, err := s.Embedder.Embed(ctx, texts)
	if err != nil {
		return nil, nil
	}
	allMatches := make([][]store.VectorMatch, len(found))
	if batch, ok := s.Vectors.(store.BatchVectorIndex); ok {
		buckets := []string{}
		for _, b := range access.Read {
			if len(scope.Buckets) == 0 || slices.Contains(scope.Buckets, b) {
				buckets = append(buckets, b)
			}
		}
		allMatches, err = batch.NearestBatch(ctx, access.Workspace, s.Embedder.Model(), vectors, store.VectorFilter{Buckets: buckets, From: scope.OccurredFrom, To: scope.OccurredTo, Types: scope.Types, K: 1617})
		if err != nil {
			return nil, err
		}
	} else {
		for i := range found {
			matches, err := s.rawNearest(ctx, access, scope, vectors[i], 202)
			if err != nil {
				return nil, err
			}
			allMatches[i] = matches
		}
	}
	union := []store.Ref{}
	seen := map[string]bool{}
	for _, matches := range allMatches {
		for _, m := range matches {
			if !seen[m.EventID] {
				seen[m.EventID] = true
				union = append(union, store.Ref{ID: m.EventID})
			}
		}
	}

	readable, err := s.Store.Records(ctx, access, scope, union)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, f := range readable {
		allowed[f.Ref.ID] = true
	}
	out := []store.LinkCandidate{}
	for i, f := range found {
		matches := allMatches[i]
		count := 0
		for _, m := range matches {
			if m.EventID == f.Ref.ID || !allowed[m.EventID] {
				continue
			}
			count++
			if count > 200 {
				store.ReportLinkTruncation(ctx, "meaning")
				break
			}
			out = append(out, store.LinkCandidate{From: f.Ref.ID, To: m.EventID, Approach: "meaning", Value: meaningScore(m.Cosine, floor), Cosine: m.Cosine, Model: s.Embedder.Model()})
		}
	}
	return out, nil
}

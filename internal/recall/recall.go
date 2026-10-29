package recall

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/trailpad/memory/internal/profile"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

// Service runs recalls against a store.
type Service struct {
	Store    store.Recaller
	Profiles *profile.Registry
	// Now is the clock; nil is the system clock.
	Now func() time.Time
}

// Response is what a recall returns, best first.
type Response struct {
	Results []Result `json:"results"`
}

// Result is one record and why it came back.
type Result struct {
	Kind     string   `json:"kind"`
	ID       string   `json:"id"`
	Record   any      `json:"record"`
	Score    float64  `json:"score"`
	Because  *Because `json:"because,omitempty"`
	Evidence []Quote  `json:"evidence,omitempty"`
}

// Because is each signal on its own, the cues shared and the path from the
// context to the record.
type Because struct {
	Relevance float64  `json:"relevance"`
	Cue       CueMatch `json:"cue"`
	Recency   float64  `json:"recency"`
	Path      []string `json:"path,omitempty"`
}

// CueMatch is the cue signal and the cues behind it, strongest first.
type CueMatch struct {
	Score  float64  `json:"score"`
	Shared []string `json:"shared,omitempty"`
}

// Quote is an event behind a memory: what it did for the memory, what was
// said, and where, from the outermost place in.
type Quote struct {
	Event     string   `json:"event"`
	Role      string   `json:"role"`
	Quote     string   `json:"quote,omitempty"`
	PlacePath []string `json:"place_path,omitempty"`
}

// QuoteLength is how many characters of an utterance a quote keeps.
const QuoteLength = 280

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Recall answers a request, or returns record.Problems when it cannot be
// answered as asked.
func (s *Service) Recall(ctx context.Context, access store.Access, req Request) (Response, error) {
	if problems := req.Validate(); len(problems) > 0 {
		return Response{}, problems
	}
	now := s.now()
	at := now
	switch {
	case req.Context.At != nil:
		at = req.Context.At.At
	case req.AsOf != nil:
		at = req.AsOf.At
	}
	limit := req.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	pool := max(limit*5, 50)
	scope := s.scope(req, now)
	scope.UsedBy = &at

	textRefs, err := s.Store.Text(ctx, access, scope, req.Context.Text, pool)
	if err != nil {
		return Response{}, err
	}

	// The context's cues reach upward: a thread in the context also shares
	// its pull request with records elsewhere on it.
	cues := req.cues()
	ancestors, err := s.Store.Ancestors(ctx, access, cues)
	if err != nil {
		return Response{}, err
	}
	ctxDepth := map[string]int{}
	ctxFrom := map[string]string{}
	for _, c := range cues {
		for _, h := range ancestors[c] {
			if d, seen := ctxDepth[h.Entity]; !seen || h.Depth < d {
				ctxDepth[h.Entity] = h.Depth
				ctxFrom[h.Entity] = c
			}
		}
	}
	reach := make([]string, 0, len(ctxDepth))
	for x := range ctxDepth {
		reach = append(reach, x)
	}
	sort.Strings(reach)
	freq, total, err := s.Store.Frequency(ctx, access, scope, reach)
	if err != nil {
		return Response{}, err
	}
	// Cues on most records find nothing in particular, so only rarer ones go
	// looking for candidates. All of them still count when scoring.
	var present, finding []string
	for _, x := range reach {
		if freq[x] == 0 {
			continue
		}
		present = append(present, x)
		if freq[x]*2 <= total {
			finding = append(finding, x)
		}
	}
	if len(finding) == 0 {
		finding = present
	}
	cueTouches, err := s.Store.Touches(ctx, access, scope, finding, nil, pool)
	if err != nil {
		return Response{}, err
	}

	var candidates []store.Ref
	seen := map[string]bool{}
	for _, r := range textRefs {
		if !seen[r.Ref.ID] {
			seen[r.Ref.ID] = true
			candidates = append(candidates, r.Ref)
		}
	}
	for _, t := range cueTouches {
		if !seen[t.Ref.ID] {
			seen[t.Ref.ID] = true
			candidates = append(candidates, t.Ref)
		}
	}
	if len(candidates) == 0 {
		return Response{Results: []Result{}}, nil
	}

	touches, err := s.Store.Touches(ctx, access, scope, present, candidates, 0)
	if err != nil {
		return Response{}, err
	}
	found, err := s.Store.Records(ctx, access, scope, candidates)
	if err != nil {
		return Response{}, err
	}

	relevance := fused(textRefs)
	// The most a record could share is every cue the context reaches, each in
	// the strongest role. Cue is the part of that a record shares, so a thin
	// match looks thin even when nothing better was found.
	// A context cue no record shares still belongs to the context, and is as
	// rare as a cue can be.
	possible := 0.0
	for _, x := range reach {
		possible += rarity(max(freq[x], 1), total) * profile.MaxCueWeight * nesting(ctxDepth[x])
	}
	type strongest struct {
		value float64
		touch store.Touch
	}
	best := map[string]map[string]strongest{}
	resolvers := map[string]*profile.Resolver{}
	for _, f := range found {
		resolvers[f.Ref.ID] = s.resolver(f)
	}
	for _, t := range touches {
		r, ok := resolvers[t.Ref.ID]
		if !ok {
			continue
		}
		v := rarity(freq[t.Cue], total) * r.CueWeight(t.Role) * nesting(ctxDepth[t.Cue]+t.Depth)
		if best[t.Ref.ID] == nil {
			best[t.Ref.ID] = map[string]strongest{}
		}
		if v > best[t.Ref.ID][t.Cue].value {
			best[t.Ref.ID][t.Cue] = strongest{v, t}
		}
	}
	cue := map[string]float64{}
	for id, byCue := range best {
		for _, b := range byCue {
			cue[id] += b.value
		}
		if possible > 0 {
			cue[id] = math.Min(cue[id]/possible, 1)
		} else {
			cue[id] = 0
		}
	}

	w := weights(req)
	results := make([]Result, 0, len(found))
	for _, f := range found {
		id := f.Ref.ID
		signals := map[Signal]float64{
			Relevance: relevance[id],
			Cue:       cue[id],
			Recency:   recency(f.Happened, lastUsed(f, at), at, s.halfLife(f, resolvers[id])),
		}
		res := Result{Kind: f.Ref.Kind, ID: id, Score: combine(signals, w)}
		if f.Memory != nil {
			res.Record = f.Memory
		} else {
			res.Record = f.Event
		}
		if req.Explain {
			because := &Because{Relevance: signals[Relevance], Cue: CueMatch{Score: signals[Cue]}, Recency: signals[Recency]}
			shared := make([]strongest, 0, len(best[id]))
			for _, b := range best[id] {
				shared = append(shared, b)
			}
			sort.Slice(shared, func(i, j int) bool {
				if shared[i].value != shared[j].value {
					return shared[i].value > shared[j].value
				}
				return shared[i].touch.Cue < shared[j].touch.Cue
			})
			for _, b := range shared {
				because.Cue.Shared = append(because.Cue.Shared, b.touch.Cue)
			}
			if len(shared) > 0 {
				t := shared[0].touch
				because.Path = path(ctxFrom[t.Cue], t.Cue, t.Entity, id)
			}
			res.Because = because
		}
		results = append(results, res)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})
	if len(results) > limit {
		results = results[:limit]
	}
	if req.Explain || req.Expand.Evidence {
		if err := s.quote(ctx, access, scope, results); err != nil {
			return Response{}, err
		}
	}
	return Response{Results: results}, nil
}

// Use records that results were useful, which keeps them fresher in later
// recalls. Browsing must not call it.
func (s *Service) Use(ctx context.Context, access store.Access, memories []string) error {
	return s.Store.Rehearse(ctx, access, memories, s.now())
}

func (s *Service) scope(req Request, now time.Time) store.Scope {
	scope := store.Scope{Kinds: req.Filters.Kinds}
	if req.AsOf != nil {
		t := req.AsOf.At
		scope.AsOf = &t
	}
	switch v := req.Filters.ValidAt; {
	case v.Any:
	case v.At != nil:
		scope.ValidAt = v.At
	default:
		scope.ValidAt = &now
	}
	scope.Types = req.Filters.Types
	scope.Buckets = req.Filters.Buckets
	if o := req.Filters.Occurred; o != nil {
		if o.From != nil {
			scope.OccurredFrom = &o.From.At
		}
		if o.To != nil {
			scope.OccurredTo = &o.To.At
		}
	}
	return scope
}

func (s *Service) resolver(f store.Found) *profile.Resolver {
	var refs []string
	if f.Memory != nil {
		refs = f.Memory.Profiles
	} else {
		refs = f.Event.Profiles
	}
	if r, err := s.Profiles.For(refs); err == nil {
		return r
	}
	return s.Profiles.Resolver()
}

func (s *Service) halfLife(f store.Found, r *profile.Resolver) float64 {
	if f.Memory != nil {
		return r.HalfLifeDays(f.Memory.Type)
	}
	return profile.DefaultHalfLifeDays
}

// lastUsed is the latest use at or before the moment asked about; a use
// after it has not happened yet from there.
func lastUsed(f store.Found, at time.Time) *time.Time {
	if f.Memory != nil && f.Memory.Recall != nil && f.Memory.Recall.LastAt != nil && !f.Memory.Recall.LastAt.At.After(at) {
		return &f.Memory.Recall.LastAt.At
	}
	return nil
}

// path runs from the context's cue to the record, skipping repeated steps.
func path(steps ...string) []string {
	var out []string
	for _, s := range steps {
		if s != "" && (len(out) == 0 || out[len(out)-1] != s) {
			out = append(out, s)
		}
	}
	return out
}

// quote attaches the events behind each memory, with what was said there
// and where.
func (s *Service) quote(ctx context.Context, access store.Access, scope store.Scope, results []Result) error {
	var refs []store.Ref
	for _, r := range results {
		if m, ok := r.Record.(*record.Memory); ok {
			for _, e := range m.Evidence {
				refs = append(refs, store.Ref{Kind: store.EventKind, ID: e.Event})
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	// Evidence is shown whatever its type or bucket filter; only the store's
	// clock and what the access may read limit it.
	found, err := s.Store.Records(ctx, access, store.Scope{AsOf: scope.AsOf}, refs)
	if err != nil {
		return err
	}
	events := map[string]*record.Event{}
	var places []string
	for _, f := range found {
		events[f.Ref.ID] = f.Event
		if f.Event.Place != "" {
			places = append(places, f.Event.Place)
		}
	}
	ancestors, err := s.Store.Ancestors(ctx, access, places)
	if err != nil {
		return err
	}
	var named []string
	for _, hops := range ancestors {
		for _, h := range hops {
			named = append(named, h.Entity)
		}
	}
	entities, err := s.Store.Entities(ctx, access, named)
	if err != nil {
		return err
	}
	for i, r := range results {
		m, ok := r.Record.(*record.Memory)
		if !ok {
			continue
		}
		for _, e := range m.Evidence {
			q := Quote{Event: e.Event, Role: string(e.Role)}
			if ev := events[e.Event]; ev != nil {
				q.Quote = firstWords(ev.Content)
				hops := ancestors[ev.Place]
				for j := len(hops) - 1; j >= 0; j-- {
					name := entities[hops[j].Entity].Name
					if name == "" {
						name = hops[j].Entity
					}
					q.PlacePath = append(q.PlacePath, name)
				}
			}
			results[i].Evidence = append(results[i].Evidence, q)
		}
	}
	return nil
}

func firstWords(content []record.Utterance) string {
	for _, u := range content {
		text := strings.TrimSpace(u.Text)
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) <= QuoteLength {
			return text
		}
		runes := []rune(text)
		return strings.TrimSpace(string(runes[:QuoteLength])) + "…"
	}
	return ""
}

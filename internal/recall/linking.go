package recall

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

var linkDefaults = map[string]float64{"thread": 1, "subject": 0.9, "meaning": 0.6, "people": 0.4, "time": 0.2, "text": 0.3, "explicit": 1}

type LinkOptions struct {
	perAnchorLimit     int
	AnchorsLimit       int                `json:"anchors_limit,omitempty"`
	After              string             `json:"after,omitempty"`
	CommonSubjectShare *float64           `json:"common_subject_share,omitempty"`
	MeaningFloor       *float64           `json:"meaning_floor,omitempty"`
	Approaches         []string           `json:"approaches,omitempty"`
	Weights            map[string]float64 `json:"weights,omitempty"`
	MinScore           float64            `json:"min_score,omitempty"`
	Limit              int                `json:"limit,omitempty"`
}

type LinkEvidence struct {
	Score      float64      `json:"score"`
	Shared     []string     `json:"shared,omitempty"`
	Path       []string     `json:"path,omitempty"`
	GapSeconds *float64     `json:"gap_seconds,omitempty"`
	Links      []StoredLink `json:"links,omitempty"`
	Cosine     *float64     `json:"cosine,omitempty"`
	Model      string       `json:"model,omitempty"`
}

type StoredLink struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type EventLink struct {
	From    string                  `json:"from"`
	To      string                  `json:"to"`
	Score   float64                 `json:"score"`
	Because map[string]LinkEvidence `json:"because"`
}

type Neighbour struct {
	ID      string                  `json:"id"`
	Record  *record.Event           `json:"record"`
	Score   float64                 `json:"score"`
	Because map[string]LinkEvidence `json:"because"`
}

func (o LinkOptions) Validate() record.Problems {
	var p record.Problems
	add := func(f, m string) { p = append(p, record.Problem{Field: f, Message: m}) }
	for i, a := range o.Approaches {
		if _, ok := linkDefaults[a]; !ok {
			add(fmt.Sprintf("approaches[%d]", i), "is not an approach this version scores")
		}
	}
	for _, a := range sortedKeys(o.Weights) {
		w := o.Weights[a]
		if _, ok := linkDefaults[a]; !ok {
			add("weights."+a, "is not an approach this version scores")
		} else if math.IsNaN(w) || math.IsInf(w, 0) || w < 0 || w > 1 {
			add("weights."+a, "must be between 0 and 1")
		}
	}
	if o.AnchorsLimit < 0 || o.AnchorsLimit > 1000 {
		add("anchors_limit", "must be between 1 and 1000")
	}
	if value := o.CommonSubjectShare; value != nil && (math.IsNaN(*value) || *value < 0 || *value > 1) {
		add("common_subject_share", "must be between 0 and 1")
	}
	if value := o.MeaningFloor; value != nil && (math.IsNaN(*value) || *value < 0 || *value >= 1) {
		add("meaning_floor", "must be at least 0 and below 1")
	}

	if o.MinScore < 0 || o.MinScore > 1 {
		add("min_score", "must be between 0 and 1")
	}
	if o.Limit < 0 || o.Limit > 10000 {
		add("limit", "must be between 1 and 10000")
	}
	_, w := o.settings()
	total := 0.0
	for _, v := range w {
		total += v
	}
	if total == 0 {
		add("weights", "at least one approach needs a weight above 0")
	}
	return p
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func (o LinkOptions) settings() ([]string, map[string]float64) {
	names := o.Approaches
	if names == nil {
		names = []string{"explicit", "thread", "subject", "people", "time", "meaning"}
	}
	w := map[string]float64{}
	for _, a := range names {
		w[a] = linkDefaults[a]
		if v, ok := o.Weights[a]; ok {
			w[a] = v
		}
	}
	return names, w
}

func (s *Service) Links(ctx context.Context, access store.Access, scope store.Scope, anchors []string, o LinkOptions, undirected bool) ([]EventLink, error) {
	if p := o.Validate(); len(p) > 0 {
		return nil, p
	}
	names, w := o.settings()
	scope.CommonSubjectShare = 0.2
	if o.CommonSubjectShare != nil {
		scope.CommonSubjectShare = *o.CommonSubjectShare
	}
	rows, err := s.Store.LinkCandidates(ctx, access, scope, anchors, names)
	if err != nil {
		return nil, err
	}
	if slices.Contains(names, "meaning") {
		floor := s.floor()
		if o.MeaningFloor != nil {
			floor = *o.MeaningFloor
		}
		semantic, err := s.meaningLinks(ctx, access, scope, anchors, floor)
		if err != nil {
			return nil, err
		}
		rows = append(rows, semantic...)
	}
	anchorSet := map[string]bool{}
	for _, id := range anchors {
		anchorSet[id] = true
	}
	pairs := map[[2]string]*EventLink{}
	for _, r := range rows {
		if r.Truncated {
			store.ReportLinkTruncation(ctx, r.Approach)
		}
		from, to := r.From, r.To
		if undirected && !anchorSet[from] && !anchorSet[to] {
			continue
		}
		if undirected && from > to {
			from, to = to, from
			slices.Reverse(r.Path)
		}
		key := [2]string{from, to}
		p := pairs[key]
		if p == nil {
			p = &EventLink{From: from, To: to, Because: map[string]LinkEvidence{}}
			pairs[key] = p
		}
		e := p.Because[r.Approach]
		switch r.Approach {
		case "meaning":
			e.Score = r.Value
			e.Cosine = &r.Cosine
			e.Model = r.Model
		case "subject":
			for _, id := range r.Shared {
				if !slices.Contains(e.Shared, id) {
					e.Shared = append(e.Shared, id)
					e.Score += rarity(r.Frequency, r.Total) / math.Log(float64(r.Total+1))
				}
			}
			e.Score = math.Min(e.Score, 1)
		case "people":
			e.Score = math.Pow(0.5, r.Value/(7*24*3600))
			e.Shared = append(e.Shared, r.Shared...)
		case "time":
			e.Score = math.Pow(0.5, r.Value/(3*3600))
			gap := r.Value
			e.GapSeconds = &gap
		case "text":
			e.Score = math.Max(e.Score, r.Value)
		case "thread":
			e.Score = 1
			if len(e.Path) == 0 || len(r.Path) < len(e.Path) {
				e.Path = r.Path
			}
		case "explicit":
			e.Score = 1
			stored := StoredLink{r.LinkID, r.LinkFrom, r.LinkTo, r.Type}
			if !slices.Contains(e.Links, stored) {
				e.Links = append(e.Links, stored)
			}
		}
		slices.Sort(e.Shared)
		e.Shared = slices.Compact(e.Shared)
		p.Because[r.Approach] = e
	}
	out := []EventLink{}
	for _, p := range pairs {
		remaining := 1.0
		for _, a := range sortedKeys(w) {
			remaining *= 1 - w[a]*p.Because[a].Score
		}
		p.Score = 1 - remaining
		if p.Score > 0 && p.Score >= o.MinScore {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	if o.perAnchorLimit > 0 {
		selected := make([]EventLink, 0, min(len(out), len(anchors)*o.perAnchorLimit))
		counts := map[string]int{}
		for _, link := range out {
			if counts[link.From] < o.perAnchorLimit {
				counts[link.From]++
				selected = append(selected, link)
			}
		}
		return selected, nil
	}
	limit := o.Limit
	if limit == 0 {
		limit = 500
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Service) neighbours(ctx context.Context, access store.Access, scope store.Scope, results []Result, n int) error {
	anchors := make([]string, len(results))
	for i := range results {
		anchors[i] = results[i].ID
	}
	links, err := s.Links(ctx, access, scope, anchors, LinkOptions{perAnchorLimit: n}, false)
	if err != nil {
		return err
	}
	byAnchor := map[string][]EventLink{}
	refs := []store.Ref{}
	seen := map[string]bool{}
	for _, l := range links {
		if len(byAnchor[l.From]) >= n {
			continue
		}
		byAnchor[l.From] = append(byAnchor[l.From], l)
		if !seen[l.To] {
			seen[l.To] = true
			refs = append(refs, store.Ref{ID: l.To})
		}
	}
	found, err := s.Store.Records(ctx, access, scope, refs)
	if err != nil {
		return err
	}
	records := map[string]*record.Event{}
	for _, f := range found {
		records[f.Ref.ID] = f.Event
	}
	for i := range results {
		results[i].Neighbours = []Neighbour{}
		for _, l := range byAnchor[results[i].ID] {
			if e := records[l.To]; e != nil {
				results[i].Neighbours = append(results[i].Neighbours, Neighbour{ID: l.To, Record: e, Score: l.Score, Because: l.Because})
			}
		}
	}
	return nil
}

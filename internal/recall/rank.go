package recall

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/trailpad/memory/internal/store"
)

// Signal is one reason a record scores.
type Signal string

const (
	Meaning   Signal = "meaning"
	Relevance Signal = "relevance"
	Cue       Signal = "cue"
	Recency   Signal = "recency"
)

func (s Signal) known() bool {
	if s == Meaning {
		return true
	}
	_, ok := presets[""][s]
	return ok
}

// presets are default weights for what a recall is for. A review cares most
// about what touches the code in front of it; an incident about what happened
// lately.
var presets = map[string]map[Signal]float64{
	"":         {Relevance: 0.45, Cue: 0.35, Recency: 0.2},
	"review":   {Relevance: 0.3, Cue: 0.55, Recency: 0.15},
	"planning": {Relevance: 0.5, Cue: 0.3, Recency: 0.2},
	"incident": {Relevance: 0.35, Cue: 0.3, Recency: 0.35},
}

func presetNames() string {
	var names []string
	for n := range presets {
		if n != "" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func weights(r Request) map[Signal]float64 {
	out := map[Signal]float64{}
	for s, w := range presets[r.Use] {
		out[s] = w
	}
	for s, w := range r.Weights {
		out[s] = w
	}
	return out
}

// fused scales each list by its own best match, so the best scores 1, then
// keeps each record's strongest showing. A record that one list finds well
// is relevant even if another list misses it. Lexical text is the only list
// today; the semantic one joins it later.
func fused(lists ...[]store.Scored) map[string]float64 {
	out := map[string]float64{}
	for _, list := range lists {
		top := 0.0
		for _, s := range list {
			top = math.Max(top, s.Score)
		}
		if top <= 0 {
			continue
		}
		for _, s := range list {
			out[s.Ref.ID] = math.Max(out[s.Ref.ID], s.Score/top)
		}
	}
	return out
}

// rarity is how much sharing a cue says: a cue on every record says almost
// nothing, one on a single record says a lot.
func rarity(records, total int) float64 {
	if records <= 0 || total <= 0 {
		return 0
	}
	return math.Log(float64(total+1) / float64(records))
}

// nesting is how much a cue counts when it is shared only through parents:
// half for each step up, on either side.
func nesting(depth int) float64 { return math.Pow(0.5, float64(depth)) }

// decay is how much of a thing remains after age, given its half-life.
func decay(age time.Duration, halfLifeDays float64) float64 {
	if age <= 0 {
		return 1
	}
	return math.Pow(0.5, age.Hours()/24/halfLifeDays)
}

// recency fades with the record's half-life. A recall brings back up to half
// of what has faded, fading itself at the same rate, as rehearsal does.
func recency(happened time.Time, lastUsed *time.Time, at time.Time, halfLifeDays float64) float64 {
	d := decay(at.Sub(happened), halfLifeDays)
	if lastUsed == nil {
		return d
	}
	return d + (1-d)*0.5*decay(at.Sub(*lastUsed), halfLifeDays)
}

func combine(signals map[Signal]float64, w map[Signal]float64) float64 {
	var sum, total float64
	for s, weight := range w {
		sum += weight * signals[s]
		total += weight
	}
	if total == 0 {
		return 0
	}
	return sum / total
}

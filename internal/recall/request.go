// Package recall answers "what would someone looking at this want to know,
// and why". It gathers candidates from the store, scores each on separate
// signals, and explains every result.
package recall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/trailpad/memory/internal/record"
)

// Context is the situation a recall is asked from. Every part is a cue.
type Context struct {
	Text         string          `json:"text,omitempty"`
	Place        string          `json:"place,omitempty"`
	Participants []string        `json:"participants,omitempty"`
	Subjects     []string        `json:"subjects,omitempty"`
	Involves     []string        `json:"involves,omitempty"`
	At           *record.Instant `json:"at,omitempty"`
}

// ValidAt is "now", a time, or null for memories whether or not they still
// hold. Left out, it means now.
type ValidAt struct {
	Set bool
	Any bool
	At  *time.Time
}

func (v *ValidAt) UnmarshalJSON(data []byte) error {
	v.Set = true
	switch {
	case bytes.Equal(data, []byte("null")):
		v.Any = true
		return nil
	case bytes.Equal(data, []byte(`"now"`)):
		return nil
	}
	var at record.Instant
	if err := json.Unmarshal(data, &at); err != nil {
		return fmt.Errorf(`valid_at is "now", a time or null: %w`, err)
	}
	v.At = &at.At
	return nil
}

func (v ValidAt) MarshalJSON() ([]byte, error) {
	switch {
	case v.Any:
		return []byte("null"), nil
	case v.At != nil:
		return json.Marshal(record.Exact(*v.At))
	}
	return []byte(`"now"`), nil
}

// Span is a stretch of time, from inclusive and to exclusive.
type Span struct {
	From *record.Instant `json:"from,omitempty"`
	To   *record.Instant `json:"to,omitempty"`
}

// Filters are hard limits: a record outside them is never returned.
type Filters struct {
	Kinds    []string      `json:"kinds,omitempty"`
	Types    []record.Type `json:"types,omitempty"`
	ValidAt  ValidAt       `json:"valid_at"`
	Occurred *Span         `json:"occurred,omitempty"`

	// Filters the spec defines that this version cannot apply yet. A request
	// using one is refused rather than answered without it.
	Valence    json.RawMessage `json:"valence,omitempty"`
	Places     json.RawMessage `json:"places,omitempty"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
}

// Expand asks for more around each result.
type Expand struct {
	Evidence   bool `json:"evidence,omitempty"`
	Neighbours int  `json:"neighbours,omitempty"`
}

// Request is one recall.
type Request struct {
	Context Context `json:"context"`
	Filters Filters `json:"filters"`
	// AsOf answers as the store would have at that time.
	AsOf *record.Instant `json:"as_of,omitempty"`
	// Use picks default weights for what the recall is for.
	Use     string             `json:"use,omitempty"`
	Weights map[Signal]float64 `json:"weights,omitempty"`
	Limit   int                `json:"limit,omitempty"`
	Expand  Expand             `json:"expand,omitempty"`
	Explain bool               `json:"explain,omitempty"`
}

const (
	DefaultLimit = 10
	MaxLimit     = 100
	// MaxWeight keeps the weighted sum finite; weights are relative, so a
	// larger one adds nothing.
	MaxWeight = 1000
)

func (r Request) cues() []string {
	var out []string
	if r.Context.Place != "" {
		out = append(out, r.Context.Place)
	}
	out = append(out, r.Context.Participants...)
	out = append(out, r.Context.Subjects...)
	return append(out, r.Context.Involves...)
}

// Validate reports everything wrong with the request at once.
func (r Request) Validate() record.Problems {
	var problems record.Problems
	add := func(field, format string, args ...any) {
		problems = append(problems, record.Problem{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if r.Context.Text == "" && len(r.cues()) == 0 {
		add("context", "needs text or at least one place, participant, subject or involved entity")
	}
	if r.Limit < 0 || r.Limit > MaxLimit {
		add("limit", "is between 1 and %d", MaxLimit)
	}
	for i, k := range r.Filters.Kinds {
		if k != "memory" && k != "event" {
			add(fmt.Sprintf("filters.kinds[%d]", i), "%q is not memory or event", k)
		}
	}
	for i, t := range r.Filters.Types {
		if !t.Valid() {
			add(fmt.Sprintf("filters.types[%d]", i), "%q is not dot separated lowercase words", t)
		}
	}
	if o := r.Filters.Occurred; o != nil && o.From != nil && o.To != nil && !o.From.At.Before(o.To.At) {
		add("filters.occurred", "from must be before to")
	}
	for _, f := range []struct {
		field string
		raw   json.RawMessage
	}{{"filters.valence", r.Filters.Valence}, {"filters.places", r.Filters.Places}, {"filters.attributes", r.Filters.Attributes}} {
		if len(f.raw) > 0 && !bytes.Equal(f.raw, []byte("null")) {
			add(f.field, "is not supported yet")
		}
	}
	if r.Expand.Neighbours != 0 {
		add("expand.neighbours", "is not supported yet")
	}
	if r.Use != "" {
		if _, ok := presets[r.Use]; !ok {
			add("use", "%q is not one of %s", r.Use, presetNames())
		}
	}
	signals := make([]Signal, 0, len(r.Weights))
	for s := range r.Weights {
		signals = append(signals, s)
	}
	slices.Sort(signals)
	for _, s := range signals {
		w := r.Weights[s]
		if !s.known() {
			add("weights."+string(s), "is not a signal this version scores")
		} else if w < 0 || w > MaxWeight {
			add("weights."+string(s), "is between 0 and %v", MaxWeight)
		}
	}
	if r.Use == "" || presets[r.Use] != nil {
		total := 0.0
		for _, w := range weights(r) {
			total += w
		}
		if total == 0 {
			add("weights", "at least one signal needs a weight above 0")
		}
	}
	return problems
}

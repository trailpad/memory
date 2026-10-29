package profile

import (
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/trailpad/memory/internal/record"
)

// PlainCueWeight is what a role counts for before any profile gives it a
// weight: unknown roles still work as cues, just not strong ones.
const PlainCueWeight = 0.5

// MaxCueWeight is the most any role counts for; profiles weigh roles from 0
// to it.
const MaxCueWeight = 1.0

// DefaultHalfLifeDays is how fast a memory of an undeclared type fades.
const DefaultHalfLifeDays = 365

// Resolver answers questions about types, roles and attributes using one set
// of profiles, so a record is read with the vocabularies it was written with.
type Resolver struct {
	profiles []*Profile
}

// Resolver over the newest version of every profile.
func (r *Registry) Resolver() *Resolver { return &Resolver{profiles: r.latest()} }

// For is a resolver over the profiles a record names, plus core. A name the
// registry does not know is reported, not ignored.
func (r *Registry) For(refs []string) (*Resolver, error) {
	core, _ := r.get(Core + "@1")
	chosen := []*Profile{core}
	for _, ref := range refs {
		p, ok := r.get(ref)
		if !ok {
			return nil, fmt.Errorf("profile %s is not registered", ref)
		}
		if p.Name != Core {
			chosen = append(chosen, p)
		}
	}
	return &Resolver{profiles: chosen}, nil
}

// chain walks from a type to what it falls back to: a declaration's own
// falls_back_to first, otherwise the type's parent in the namespace.
func chain[T any](t record.Type, find func(record.Type) (T, bool), next func(T) record.Type) []T {
	var out []T
	seen := map[record.Type]bool{}
	for t != "" && !seen[t] {
		seen[t] = true
		if found, ok := find(t); ok {
			out = append(out, found)
			if to := next(found); to != "" {
				t = to
				continue
			}
		}
		t = parent(t)
	}
	return out
}

func parent(t record.Type) record.Type {
	i := strings.LastIndex(string(t), ".")
	if i < 0 {
		return ""
	}
	return t[:i]
}

func (r *Resolver) memoryType(t record.Type) (MemoryType, bool) {
	for _, p := range r.profiles {
		for _, m := range p.MemoryTypes {
			if m.Type == t {
				return m, true
			}
		}
	}
	return MemoryType{}, false
}

func (r *Resolver) eventType(t record.Type) (EventType, bool) {
	for _, p := range r.profiles {
		for _, e := range p.EventTypes {
			if e.Type == t {
				return e, true
			}
		}
	}
	return EventType{}, false
}

func (r *Resolver) entityType(t record.Type) (EntityType, bool) {
	for _, p := range r.profiles {
		for _, e := range p.EntityTypes {
			if e.Type == t {
				return e, true
			}
		}
	}
	return EntityType{}, false
}

// HalfLifeDays is how fast a memory of this type fades, from the nearest
// declaration that says.
func (r *Resolver) HalfLifeDays(t record.Type) float64 {
	for _, m := range chain(t, r.memoryType, func(m MemoryType) record.Type { return m.FallsBackTo }) {
		if m.HalfLifeDays > 0 {
			return m.HalfLifeDays
		}
	}
	return DefaultHalfLifeDays
}

// KnownMemoryType reports whether the type, or something it falls back to,
// is declared.
func (r *Resolver) KnownMemoryType(t record.Type) bool {
	return len(chain(t, r.memoryType, func(m MemoryType) record.Type { return m.FallsBackTo })) > 0
}

// EventIcon is the icon for an event type, from the nearest declaration.
func (r *Resolver) EventIcon(t record.Type) string {
	for _, e := range chain(t, r.eventType, func(e EventType) record.Type { return e.FallsBackTo }) {
		if e.Icon != "" {
			return e.Icon
		}
	}
	return ""
}

// EntityIcon is the icon for an entity type, from the nearest declaration.
func (r *Resolver) EntityIcon(t record.Type) string {
	next := func(e EntityType) record.Type {
		if e.FallsBackTo != "" {
			return e.FallsBackTo
		}
		return e.ParentType
	}
	for _, e := range chain(t, r.entityType, next) {
		if e.Icon != "" {
			return e.Icon
		}
	}
	return ""
}

// CueWeight is how much sharing a role counts for in recall.
func (r *Resolver) CueWeight(role record.Role) float64 {
	for _, t := range role.Lineage() {
		for _, p := range r.profiles {
			for _, declared := range p.Roles {
				if declared.Role == t {
					return declared.CueWeight
				}
			}
		}
	}
	return PlainCueWeight
}

// Filterable reports whether recall may filter on an attribute.
func (r *Resolver) Filterable(key record.Type) bool {
	for _, p := range r.profiles {
		for _, a := range p.Attributes {
			if a.Key == key {
				return a.Filterable
			}
		}
	}
	return false
}

// CheckAttributes validates the attributes a profile declares a schema for.
// Undeclared attributes are kept as written.
func (r *Resolver) CheckAttributes(field string, values record.Attributes) record.Problems {
	var problems record.Problems
	for key, raw := range values {
		schema := r.schemaFor(key)
		if schema == nil {
			continue
		}
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			problems = append(problems, record.Problem{Field: field + "." + string(key), Message: "is not valid JSON"})
			continue
		}
		if err := schema.Validate(value); err != nil {
			problems = append(problems, record.Problem{Field: field + "." + string(key), Message: firstLine(err)})
		}
	}
	return problems
}

// CheckDimensions validates judgements against the scales profiles declare.
// A dimension no profile declares is refused: unlike an attribute, a
// judgement with no scale cannot be compared with any other.
func (r *Resolver) CheckDimensions(field string, values map[record.Type]int) record.Problems {
	var problems record.Problems
	for key, value := range values {
		scale, ok := r.scaleFor(key)
		if !ok {
			problems = append(problems, record.Problem{Field: field + "." + string(key), Message: "no profile declares this dimension"})
			continue
		}
		if value < scale[0] || value > scale[1] {
			problems = append(problems, record.Problem{Field: field + "." + string(key), Message: fmt.Sprintf("%d is outside %d to %d", value, scale[0], scale[1])})
		}
	}
	return problems
}

func (r *Resolver) schemaFor(key record.Type) *jsonschema.Schema {
	for _, p := range r.profiles {
		if s, ok := p.schemas[key]; ok {
			return s
		}
	}
	return nil
}

func (r *Resolver) scaleFor(key record.Type) ([]int, bool) {
	for _, p := range r.profiles {
		for _, d := range p.Appraisals {
			if d.Dimension == key {
				return d.Scale, true
			}
		}
	}
	return nil, false
}

// firstLine is the most specific reason a value failed its schema: the
// library reports the schema first and the failing keyword last.
func firstLine(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")
	return strings.TrimLeft(strings.TrimSpace(lines[len(lines)-1]), "- ")
}

// MemoryLineage is a memory type followed by every type it falls back to:
// declared fallbacks, which may cross into another profile, and namespace
// parents.
func (r *Resolver) MemoryLineage(t record.Type) []record.Type {
	return lineage(t, func(t record.Type) record.Type {
		m, _ := r.memoryType(t)
		return m.FallsBackTo
	})
}

// EventLineage is the same for an event type.
func (r *Resolver) EventLineage(t record.Type) []record.Type {
	return lineage(t, func(t record.Type) record.Type {
		e, _ := r.eventType(t)
		return e.FallsBackTo
	})
}

func lineage(t record.Type, fallsBackTo func(record.Type) record.Type) []record.Type {
	var out []record.Type
	seen := map[record.Type]bool{}
	var walk func(record.Type)
	walk = func(t record.Type) {
		for t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
			if to := fallsBackTo(t); to != "" {
				walk(to)
			}
			t = parent(t)
		}
	}
	walk(t)
	return out
}

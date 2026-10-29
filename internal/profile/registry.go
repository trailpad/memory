// Package profile holds the vocabularies that give types, roles, attributes
// and judgements their meaning. The spec's own vocabulary is the core profile.
package profile

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/trailpad/memory/internal/record"
)

// Core is the profile every registry starts with.
const Core = "core"

//go:embed core.json
var coreJSON []byte

type Medium struct {
	Sync      *bool  `json:"sync,omitempty"`
	Formality string `json:"formality,omitempty"`
	Record    string `json:"record,omitempty"`
}

type EntityType struct {
	Type        record.Type  `json:"type"`
	Class       record.Class `json:"class,omitempty"`
	ParentType  record.Type  `json:"parent_type,omitempty"`
	FallsBackTo record.Type  `json:"falls_back_to,omitempty"`
	Icon        string       `json:"icon,omitempty"`
	Medium      *Medium      `json:"medium,omitempty"`
}

type EventType struct {
	Type        record.Type `json:"type"`
	FallsBackTo record.Type `json:"falls_back_to,omitempty"`
	Icon        string      `json:"icon,omitempty"`
	Medium      *Medium     `json:"medium,omitempty"`
}

type MemoryType struct {
	Type         record.Type `json:"type"`
	FallsBackTo  record.Type `json:"falls_back_to,omitempty"`
	HalfLifeDays float64     `json:"half_life_days,omitempty"`
}

type Role struct {
	Role      record.Role `json:"role"`
	CueWeight float64     `json:"cue_weight"`
}

type Attribute struct {
	Key        record.Type     `json:"key"`
	Schema     json.RawMessage `json:"schema,omitempty"`
	Filterable bool            `json:"filterable,omitempty"`
}

type Dimension struct {
	Dimension record.Type `json:"dimension"`
	Scale     [2]int      `json:"scale"`
}

type LinkType struct {
	Type record.Type `json:"type"`
	From string      `json:"from"`
	To   string      `json:"to"`
}

// Profile is one versioned vocabulary.
type Profile struct {
	Name        string       `json:"profile"`
	Version     string       `json:"version"`
	EntityTypes []EntityType `json:"entity_types,omitempty"`
	EventTypes  []EventType  `json:"event_types,omitempty"`
	MemoryTypes []MemoryType `json:"memory_types,omitempty"`
	Roles       []Role       `json:"roles,omitempty"`
	Attributes  []Attribute  `json:"attributes,omitempty"`
	Appraisals  []Dimension  `json:"appraisals,omitempty"`
	Links       []LinkType   `json:"links,omitempty"`

	schemas map[record.Type]*jsonschema.Schema
}

// Ref is how a record names the profile it was written with: "crm@1".
func (p *Profile) Ref() string { return p.Name + "@" + p.Version }

var profileName = regexp.MustCompile(`^[a-z0-9_]+$`)
var profileVersion = regexp.MustCompile(`^[0-9]+$`)

// Parse reads a profile and checks it follows the rules for profiles.
func Parse(data []byte) (*Profile, error) {
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("profile is not valid JSON: %w", err)
	}
	if err := p.check(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p *Profile) check() error {
	var problems record.Problems
	add := func(field, format string, args ...any) {
		problems = append(problems, record.Problem{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if !profileName.MatchString(p.Name) {
		add("profile", "%q is not a lowercase word", p.Name)
	}
	if !profileVersion.MatchString(p.Version) {
		add("version", "%q is not a whole number", p.Version)
	}
	// Everything a profile adds is namespaced by the profile's name; the
	// unnamespaced names belong to the core profile.
	owned := func(field string, t record.Type) {
		if !t.Valid() {
			add(field, "%q is not dot separated lowercase words", t)
			return
		}
		if p.Name != Core && (!t.Namespaced() || t.Platform() != p.Name) {
			add(field, "%q is not in this profile's namespace; write it as %s.<name>", t, p.Name)
		}
	}
	for i, t := range p.EntityTypes {
		owned(fmt.Sprintf("entity_types[%d].type", i), t.Type)
	}
	for i, t := range p.EventTypes {
		owned(fmt.Sprintf("event_types[%d].type", i), t.Type)
	}
	for i, t := range p.MemoryTypes {
		owned(fmt.Sprintf("memory_types[%d].type", i), t.Type)
		if t.HalfLifeDays < 0 {
			add(fmt.Sprintf("memory_types[%d].half_life_days", i), "cannot be negative")
		}
	}
	for i, r := range p.Roles {
		owned(fmt.Sprintf("roles[%d].role", i), r.Role)
		if r.CueWeight < 0 || r.CueWeight > 1 {
			add(fmt.Sprintf("roles[%d].cue_weight", i), "%v is outside 0 to 1", r.CueWeight)
		}
	}
	p.schemas = map[record.Type]*jsonschema.Schema{}
	for i, a := range p.Attributes {
		field := fmt.Sprintf("attributes[%d]", i)
		if !a.Key.Valid() || !a.Key.Namespaced() {
			add(field+".key", "%q is not namespaced, like crm.amount", a.Key)
			continue
		}
		if len(a.Schema) == 0 {
			continue
		}
		schema, err := compile(p.Ref()+"/"+string(a.Key), a.Schema)
		if err != nil {
			add(field+".schema", "%v", err)
			continue
		}
		p.schemas[a.Key] = schema
	}
	for i, d := range p.Appraisals {
		field := fmt.Sprintf("appraisals[%d]", i)
		if !d.Dimension.Valid() || !d.Dimension.Namespaced() {
			add(field+".dimension", "%q is not namespaced, like crm.risk", d.Dimension)
		}
		if d.Scale[0] >= d.Scale[1] {
			add(field+".scale", "the low end must be below the high end")
		}
	}
	for i, l := range p.Links {
		owned(fmt.Sprintf("links[%d].type", i), l.Type)
	}
	return problems.Err()
}

func compile(name string, raw json.RawMessage) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	url := "urn:profile:" + name
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// Registry is every profile the store knows, by name and version.
type Registry struct {
	profiles map[string]map[string]*Profile
}

// NewRegistry starts with the core profile.
func NewRegistry() *Registry {
	core, err := Parse(coreJSON)
	if err != nil {
		panic("core profile is invalid: " + err.Error())
	}
	r := &Registry{profiles: map[string]map[string]*Profile{}}
	r.put(core)
	return r
}

func (r *Registry) put(p *Profile) {
	if r.profiles[p.Name] == nil {
		r.profiles[p.Name] = map[string]*Profile{}
	}
	r.profiles[p.Name][p.Version] = p
}

// Add registers a profile. A published version never changes, so adding a
// different profile under an existing name and version is refused.
func (r *Registry) Add(p *Profile) error {
	if p.Name == Core {
		return fmt.Errorf("the core profile comes with the spec and cannot be replaced")
	}
	if existing, ok := r.profiles[p.Name][p.Version]; ok {
		a, _ := json.Marshal(existing)
		b, _ := json.Marshal(p)
		if string(a) != string(b) {
			return fmt.Errorf("%s is already registered with different content; publish it as a new version", p.Ref())
		}
		return nil
	}
	r.put(p)
	return nil
}

// Latest is the newest version of each registered profile.
func (r *Registry) Latest() []*Profile {
	out := make([]*Profile, 0, len(r.profiles))
	for _, versions := range r.profiles {
		var best *Profile
		for _, p := range versions {
			if best == nil || number(p.Version) > number(best.Version) {
				best = p
			}
		}
		out = append(out, best)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get is one profile by its ref, "crm@1".
func (r *Registry) Get(ref string) (*Profile, bool) {
	name, version, ok := strings.Cut(ref, "@")
	if !ok {
		return nil, false
	}
	p, found := r.profiles[name][version]
	return p, found
}

func number(version string) int {
	n, _ := strconv.Atoi(version)
	return n
}

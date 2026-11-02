package record_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/trailpad/memory/internal/profile"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/spec"
)

// schemas are the published schemas, as the spec package embeds them.
func schemas(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	out := map[string]*jsonschema.Schema{}
	for _, kind := range []string{"event", "entity", "appraisal", "link", "profile"} {
		s, err := spec.Schema(kind)
		if err != nil {
			t.Fatal(err)
		}
		out[kind] = s
	}
	return out
}

type validator interface{ Validate() record.Problems }

// decode reads a record strictly, so a field the spec shows but the Go type
// lacks fails instead of disappearing.
func decode(kind string, raw []byte) (validator, error) {
	var into validator
	switch kind {
	case "event":
		into = &record.Event{}
	case "entity":
		into = &record.Entity{}
	case "appraisal":
		into = &record.Appraisal{}
	case "link":
		into = &record.Link{}
	default:
		return nil, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		return nil, err
	}
	switch v := into.(type) {
	case *record.Event:
		return *v, nil
	case *record.Entity:
		return *v, nil
	case *record.Appraisal:
		return *v, nil
	case *record.Link:
		return *v, nil
	}
	return nil, nil
}

func kindOf(file string) string {
	name := filepath.Base(file)
	kind, _, _ := strings.Cut(strings.TrimSuffix(name, ".json"), ".")
	return kind
}

func instance(t *testing.T, raw []byte) any {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSpecExamplesPassSchemaAndGo(t *testing.T) {
	compiled := schemas(t)
	files, _ := filepath.Glob("../../spec/examples/*.json")
	if len(files) == 0 {
		t.Fatal("no examples found")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			kind := kindOf(f)
			schema, ok := compiled[kind]
			if !ok {
				t.Fatalf("no schema for %s", kind)
			}
			if err := schema.Validate(instance(t, raw)); err != nil {
				t.Fatalf("schema refuses the spec's own example: %v", err)
			}
			if kind == "profile" {
				if _, err := profile.Parse(raw); err != nil {
					t.Fatalf("Go refuses the spec's own example: %v", err)
				}
				return
			}
			rec, err := decode(kind, raw)
			if err != nil {
				t.Fatalf("Go refuses the spec's own example: %v", err)
			}
			if problems := rec.Validate(); len(problems) > 0 {
				t.Fatalf("Go validation refuses the spec's own example: %v", problems)
			}
			written, _ := json.Marshal(rec)
			if err := schema.Validate(instance(t, written)); err != nil {
				t.Fatalf("what Go writes back fails the schema: %v\n%s", err, written)
			}
		})
	}
}

// Each case breaks one rule in a spec example. The schema and the Go
// validation must both refuse it, so the two cannot drift apart.
func TestSchemaAndGoRefuseTheSameRecords(t *testing.T) {
	compiled := schemas(t)
	cases := []struct {
		name, file string
		change     func(map[string]any)
	}{
		{"event without id", "event.review-comment.json", func(m map[string]any) { delete(m, "id") }},
		{"event type with capitals", "event.review-comment.json", func(m map[string]any) { m["type"] = "GitHub.Comment" }},
		{"participant without role", "event.review-comment.json", func(m map[string]any) {
			m["participants"] = []any{map[string]any{"actor": "ac_ana"}}
		}},
		{"empty utterance", "event.review-comment.json", func(m map[string]any) {
			m["content"] = []any{map[string]any{"speaker": "ac_ana", "text": ""}}
		}},
		{"attribute key without namespace", "event.meeting.json", func(m map[string]any) {
			m["attributes"] = map[string]any{"amount": 3}
		}},
		{"declared kind without namespace", "entity.deal.json", func(m map[string]any) { m["type"] = "deal" }},
		{"unknown class", "entity.place.json", func(m map[string]any) { m["class"] = "room" }},
		{"identity without handle", "entity.actor.json", func(m map[string]any) {
			m["identities"] = []any{map[string]any{"platform": "github"}}
		}},
		{"valence of three", "appraisal.json", func(m map[string]any) { m["valence"] = 3 }},
		{"intensity of zero", "appraisal.json", func(m map[string]any) { m["intensity"] = 0 }},
		{"appraisal that judges nothing", "appraisal.json", func(m map[string]any) {
			for _, k := range []string{"valence", "intensity", "importance", "surprise", "dimensions"} {
				delete(m, k)
			}
		}},
		{"dimension without namespace", "appraisal.json", func(m map[string]any) {
			m["dimensions"] = map[string]any{"risk": 2}
		}},
		{"link without target", "link.json", func(m map[string]any) { delete(m, "to") }},
		{"imprecise time with unknown precision", "event.meeting.json", func(m map[string]any) {
			m["occurred_at"] = map[string]any{"at": "2026-04-21T09:00:00Z", "precision": "week"}
		}},
		{"time without offset", "event.meeting.json", func(m map[string]any) { m["occurred_at"] = "2026-04-21T09:00:00" }},
		{"unknown formality", "entity.place.json", func(m map[string]any) {
			m["medium"] = map[string]any{"formality": "invalid"}
		}},
		{"unknown record medium", "entity.place.json", func(m map[string]any) {
			m["medium"] = map[string]any{"record": "invalid"}
		}},
		{"role without weight", "profile.crm.json", func(m map[string]any) {
			m["roles"] = []any{map[string]any{"role": "crm.buyer"}}
		}},
		{"link type without ends", "profile.crm.json", func(m map[string]any) {
			m["links"] = []any{map[string]any{"type": "crm.blocks"}}
		}},
		{"entity type of unknown class", "profile.crm.json", func(m map[string]any) {
			m["entity_types"] = []any{map[string]any{"type": "crm.deal", "class": "invalid"}}
		}},
		{"scale with three ends", "profile.crm.json", func(m map[string]any) {
			m["appraisals"] = []any{map[string]any{"dimension": "crm.risk", "scale": []any{1, 5, 99}}}
		}},
		{"attribute from another profile", "profile.crm.json", func(m map[string]any) {
			m["attributes"] = []any{map[string]any{"key": "care.dose", "schema": false}}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, err := os.ReadFile("../../spec/examples/" + c.file)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			c.change(m)
			broken, _ := json.Marshal(m)
			kind := kindOf(c.file)

			if kind == "profile" {
				// A profile from another namespace is valid JSON; owning names
				// is a rule only Go can check.
				if _, err := profile.Parse(broken); err == nil {
					t.Error("Go accepts it")
				}
				return
			}
			if err := compiled[kind].Validate(instance(t, broken)); err == nil {
				t.Error("schema accepts it")
			}
			rec, err := decode(kind, broken)
			if err == nil && len(rec.Validate()) == 0 {
				t.Error("Go accepts it")
			}
		})
	}
}

// Rules that relate two fields of one record are beyond JSON Schema; Go
// checks them alone.
func TestRulesOnlyGoChecks(t *testing.T) {
	cases := []struct {
		name, file string
		change     func(map[string]any)
		field      string
	}{
		{"meeting ends before it starts", "event.meeting.json", func(m map[string]any) {
			m["ended_at"] = "2026-04-21T08:00:00Z"
		}, "ended_at"},
		{"event part of itself", "event.review-comment.json", func(m map[string]any) { m["part_of"] = m["id"] }, "part_of"},
		{"entity its own parent", "entity.subject.json", func(m map[string]any) { m["parent"] = m["id"] }, "parent"},
		{"link to itself", "link.json", func(m map[string]any) { m["to"] = m["from"] }, "to"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, _ := os.ReadFile("../../spec/examples/" + c.file)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			c.change(m)
			broken, _ := json.Marshal(m)
			rec, err := decode(kindOf(c.file), broken)
			if err != nil {
				t.Fatal(err)
			}
			problems := rec.Validate()
			if len(problems) != 1 || problems[0].Field != c.field {
				t.Fatalf("want one problem on %s, got %v", c.field, problems)
			}
		})
	}
}

package profile_test

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/trailpad/memory/internal/profile"
	"github.com/trailpad/memory/internal/record"
)

func crm(t *testing.T) *profile.Registry {
	t.Helper()
	raw, err := os.ReadFile("../../spec/examples/profile.crm.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatalf("the spec's example profile is refused: %v", err)
	}
	r := profile.NewRegistry()
	if err := r.Add(p); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRolesCountByWhatTheyDid(t *testing.T) {
	r := crm(t).Resolver()
	if r.CueWeight("decider") <= r.CueWeight("mentioned") {
		t.Fatal("a decider must count for more than someone mentioned")
	}
	if got := r.CueWeight("crm.buyer"); got != 0.9 {
		t.Fatalf("a role the profile declares keeps its weight, got %v", got)
	}
	if got := r.CueWeight("legal.witness"); got != profile.PlainCueWeight {
		t.Fatalf("an undeclared role still counts as a plain cue, got %v", got)
	}
	if got := r.CueWeight("decider.final"); got != r.CueWeight("decider") {
		t.Fatalf("a narrower role falls back to its parent, got %v", got)
	}
}

func TestTypesFallBack(t *testing.T) {
	r := crm(t).Resolver()
	if got := r.HalfLifeDays("crm.objection"); got != 180 {
		t.Fatalf("declared half life, got %v", got)
	}
	if got := r.HalfLifeDays("crm.objection.pricing"); got != 180 {
		t.Fatalf("an undeclared narrower type takes its parent's half life, got %v", got)
	}
	if got := r.HalfLifeDays("decision"); got != 365 {
		t.Fatalf("core half life, got %v", got)
	}
	if got := r.EventIcon("crm.call"); got != "calendar" {
		t.Fatalf("a call falls back to a meeting's icon, got %q", got)
	}
	if got := r.EntityIcon("crm.deal"); got != "handshake" {
		t.Fatalf("entity icon, got %q", got)
	}
}

func TestAttributesFollowTheirProfile(t *testing.T) {
	r := crm(t).Resolver()
	values := record.Attributes{
		"crm.stage":  json.RawMessage(`"maybe"`),
		"crm.amount": json.RawMessage(`{"value": 120000, "unit": "EUR"}`),
		"legal.case": json.RawMessage(`"anything"`),
	}
	problems := r.CheckAttributes("attributes", values)
	if len(problems) != 1 || problems[0].Field != "attributes.crm.stage" {
		t.Fatalf("only the stage breaks its schema, got %v", problems)
	}
	if !r.Filterable("crm.stage") || r.Filterable("legal.case") {
		t.Fatal("only declared filterable attributes can be filtered on")
	}
}

func TestJudgementsStayOnTheirScale(t *testing.T) {
	r := crm(t).Resolver()
	if p := r.CheckDimensions("dimensions", map[record.Type]int{"crm.risk": 4}); len(p) != 0 {
		t.Fatalf("in range, got %v", p)
	}
	if p := r.CheckDimensions("dimensions", map[record.Type]int{"crm.risk": 9}); len(p) != 1 {
		t.Fatal("out of range must be refused")
	}
	if p := r.CheckDimensions("dimensions", map[record.Type]int{"care.pain": 2}); len(p) != 1 {
		t.Fatal("a dimension no profile declares has no scale to compare on")
	}
}

func TestProfilesStayInTheirNamespace(t *testing.T) {
	_, err := profile.Parse([]byte(`{"profile":"crm","version":"1","roles":[{"role":"buyer","cue_weight":0.9}],"event_types":[{"type":"care.dose"}]}`))
	if err == nil || !strings.Contains(err.Error(), "roles[0].role") || !strings.Contains(err.Error(), "event_types[0].type") {
		t.Fatalf("a profile may only add names under its own, got %v", err)
	}
}

func TestPublishedVersionsDoNotChange(t *testing.T) {
	r := crm(t)
	changed, err := profile.Parse([]byte(`{"profile":"crm","version":"1","roles":[{"role":"crm.buyer","cue_weight":0.1}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Add(changed); err == nil {
		t.Fatal("crm@1 with different content must be refused")
	}
	next, _ := profile.Parse([]byte(`{"profile":"crm","version":"2","roles":[{"role":"crm.buyer","cue_weight":0.1}]}`))
	if err := r.Add(next); err != nil {
		t.Fatal(err)
	}
	if got := r.Resolver().CueWeight("crm.buyer"); got != 0.1 {
		t.Fatalf("the newest version answers by default, got %v", got)
	}
	old, err := r.For([]string{"crm@1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := old.CueWeight("crm.buyer"); got != 0.9 {
		t.Fatalf("a record written with crm@1 is read with crm@1, got %v", got)
	}
	if _, err := r.For([]string{"crm@7"}); err == nil {
		t.Fatal("an unknown profile version must be reported")
	}
}

func TestCoreCannotBeReplaced(t *testing.T) {
	p, _ := profile.Parse([]byte(`{"profile":"core","version":"2"}`))
	if err := profile.NewRegistry().Add(p); err == nil {
		t.Fatal("core comes with the spec")
	}
}

func TestProfilesThatCannotBeLoaded(t *testing.T) {
	cases := map[string]string{
		"name with capitals":      `{"profile":"CRM","version":"1"}`,
		"version that is a word":  `{"profile":"crm","version":"one"}`,
		"weight above one":        `{"profile":"crm","version":"1","roles":[{"role":"crm.buyer","cue_weight":2}]}`,
		"negative half life":      `{"profile":"crm","version":"1","event_types":[{"type":"crm.note","half_life_days":-1}]}`,
		"upside down scale":       `{"profile":"crm","version":"1","appraisals":[{"dimension":"crm.risk","scale":[5,1]}]}`,
		"attribute schema broken": `{"profile":"crm","version":"1","attributes":[{"key":"crm.stage","schema":{"type":"colour"}}]}`,
		"not json":                `{"profile":`,
	}
	for name, raw := range cases {
		if _, err := profile.Parse([]byte(raw)); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

func TestAPublishedProfileCannotBeChangedThroughAPointer(t *testing.T) {
	r := crm(t)
	before := r.Resolver().CueWeight("crm.buyer")
	held, ok := r.Get("crm@1")
	if !ok {
		t.Fatal("crm@1 is registered")
	}
	held.Roles[0].CueWeight = 0.01
	if got := r.Resolver().CueWeight("crm.buyer"); got != before {
		t.Fatalf("editing a copy changed the registry: %v", got)
	}
}

func TestTheRegistryServesWhileProfilesArrive(t *testing.T) {
	r := crm(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for v := 2; v < 50; v++ {
			p, err := profile.Parse([]byte(`{"profile":"crm","version":"` + strconv.Itoa(v) + `"}`))
			if err != nil {
				t.Error(err)
				return
			}
			if err := r.Add(p); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 0; i < 200; i++ {
		_ = r.Resolver().CueWeight("decider")
		if _, err := r.For([]string{"crm@1"}); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}

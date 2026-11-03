package httpapi_test

import (
	"encoding/json"
	"testing"

	"github.com/trailpad/memory/internal/record"
)

func TestActorVocabularyAndDelegationRoundTrip(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("actors", "ev_seed", "Actor vocabulary")
	for _, typ := range []string{"person", "agent", "bot", "service", "group", "organisation", "team"} {
		t.Run(typ, func(t *testing.T) {
			id := "ac_" + typ
			e.must(200, "POST", "/v1/records", e.key, "", map[string]any{
				"entities": []map[string]string{{"bucket": bucket, "id": id, "class": "actor", "type": typ}},
			})
			var entity record.Entity
			if err := json.Unmarshal(e.must(200, "GET", "/v1/entities/"+id, e.key, "", nil), &entity); err != nil {
				t.Fatal(err)
			}
			want := typ
			if typ == "team" {
				want = "group"
			}
			if entity.Type != record.Type(want) || entity.Class != record.ActorClass {
				t.Fatalf("actor read = %+v", entity)
			}
		})
	}
	participants := []record.Participant{
		{Actor: "ac_agent", Role: record.Author},
		{Actor: "ac_person", Role: record.OnBehalfOf},
		{Actor: "ac_team", Role: record.Addressee},
	}
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{
		"events": []map[string]any{{"bucket": bucket, "id": "ev_delegated", "type": "knowledge.updated", "occurred_at": "2026-10-21T10:00:00Z", "participants": participants}},
	})
	var event record.Event
	if err := json.Unmarshal(e.must(200, "GET", "/v1/events/ev_delegated", e.key, "", nil), &event); err != nil {
		t.Fatal(err)
	}
	if len(event.Participants) != len(participants) {
		t.Fatalf("participants = %+v", event.Participants)
	}
	for i, want := range participants {
		if event.Participants[i] != want {
			t.Fatalf("participant = %+v, want %+v", event.Participants[i], want)
		}
	}
	for _, response := range [][]byte{
		e.must(200, "GET", "/v1/events", e.key, "", nil),
	} {
		var page struct {
			Entities []record.Entity `json:"entities"`
		}
		if err := json.Unmarshal(response, &page); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, entity := range page.Entities {
			if entity.ID == "ac_team" {
				found = true
				if entity.Type != "group" {
					t.Fatalf("stored team returned as %s", entity.Type)
				}
			}
		}
		if !found {
			t.Fatalf("stored team missing from response: %s", response)
		}
	}
	var recalled struct {
		Results []struct {
			ID     string       `json:"id"`
			Record record.Event `json:"record"`
		} `json:"results"`
	}
	raw := e.must(200, "POST", "/v1/recall", e.key, "", `{"context":{"participants":["ac_person"]},"limit":10}`)
	if err := json.Unmarshal(raw, &recalled); err != nil {
		t.Fatal(err)
	}
	if len(recalled.Results) != 1 || recalled.Results[0].ID != "ev_delegated" {
		t.Fatalf("represented actor did not recall its event: %s", raw)
	}
	if len(recalled.Results[0].Record.Participants) != len(participants) {
		t.Fatalf("recall lost participants: %s", raw)
	}

}

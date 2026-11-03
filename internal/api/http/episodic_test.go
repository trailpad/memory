package httpapi_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/trailpad/memory/internal/record"
)

type episodicResult struct {
	ID      string       `json:"id"`
	Record  record.Event `json:"record"`
	Because struct {
		Recency float64 `json:"recency"`
		Cue     struct {
			Score float64 `json:"score"`
		} `json:"cue"`
	} `json:"because"`
}

func (e *engine) episodicRecall(key, token string, request any) []episodicResult {
	e.t.Helper()
	var response struct {
		Results []episodicResult `json:"results"`
	}
	if err := json.Unmarshal(e.must(200, "POST", "/v1/recall", key, token, request), &response); err != nil {
		e.t.Fatal(err)
	}
	return response.Results
}

func TestEventReasonsAreBoundedReturnedAndSearchable(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_shop", "ev_retry", "Reduced retries.")
	event := record.Event{Bucket: bucket, ID: "ev_reason", Type: "deployment.completed", Why: "Circuitbreaker exhausted.", Content: []record.Utterance{{Text: "Deployment completed."}}}
	at := record.Exact(e.now.Add(-time.Hour))
	event.OccurredAt = &at
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"events": []record.Event{event}})
	var got record.Event
	if err := json.Unmarshal(e.must(200, "GET", "/v1/events/ev_reason", e.key, "", nil), &got); err != nil {
		t.Fatal(err)
	}
	if got.Why != event.Why {
		t.Fatalf("reason: %q", got.Why)
	}
	var page struct {
		Events []record.Event `json:"events"`
	}
	if err := json.Unmarshal(e.must(200, "GET", "/v1/events", e.key, "", nil), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Events[1].Why != event.Why {
		t.Fatalf("listing: %+v", page.Events)
	}
	results := e.episodicRecall(e.key, "", `{"context":{"text":"circuitbreaker"}}`)
	if len(results) != 1 || results[0].ID != event.ID || results[0].Record.Why != event.Why {
		t.Fatalf("reason search: %+v", results)
	}
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"events": []record.Event{got}})
	event.ID = "ev_max_reason"
	event.Why = strings.Repeat("é", 8192)
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"events": []record.Event{event}})
	event.ID = "ev_long_reason"
	event.Why += "é"
	out := e.must(422, "POST", "/v1/records", e.key, "", map[string]any{"events": []record.Event{event}})
	if !strings.Contains(string(out), "events[0].why") {
		t.Fatalf("reason error: %s", out)
	}
	e.must(404, "GET", "/v1/events/ev_long_reason", e.key, "", nil)
}

func TestEventAppraisalsAreReturnedOnlyWhenReadable(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Reduced retries.")
	secret := e.repository("repo_secret", "ev_secret", "Private review.")
	e.readers(shop, "gh_ana")
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"appraisals": []any{
		map[string]any{"bucket": shop, "id": "ap_public", "target": "ev_retry", "by": "ac_ev_retry", "valence": 1, "intensity": 3, "importance": 5},
		map[string]any{"bucket": secret, "id": "ap_private", "target": "ev_retry", "by": "ac_ev_secret", "valence": -1},
	}})
	read := func(path, token string) []record.Event {
		t.Helper()
		raw := e.must(200, "GET", path, e.key, token, nil)
		if path == "/v1/events" {
			var page struct {
				Events []record.Event `json:"events"`
			}
			if err := json.Unmarshal(raw, &page); err != nil {
				t.Fatal(err)
			}
			return page.Events
		}
		var event record.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			t.Fatal(err)
		}
		return []record.Event{event}
	}
	ana := e.sign(e.claims("user_ana", "gh_ana"))
	for _, path := range []string{"/v1/events/ev_retry", "/v1/events"} {
		events := read(path, ana)
		if len(events) != 1 || len(events[0].Appraisals) != 1 {
			t.Fatalf("%s: %+v", path, events)
		}
		a := events[0].Appraisals[0]
		if a.ID != "ap_public" || a.Valence == nil || *a.Valence != 1 || a.Intensity == nil || *a.Intensity != 3 || a.Importance == nil || *a.Importance != 5 {
			t.Fatalf("appraisal values: %+v", a)
		}
	}
	if got := read("/v1/events/ev_retry", ""); len(got[0].Appraisals) != 2 {
		t.Fatalf("service appraisals: %+v", got)
	}
	result := e.episodicRecall(e.key, ana, `{"context":{"text":"retries"}}`)
	if len(result) != 1 || len(result[0].Record.Appraisals) != 1 {
		t.Fatalf("recall appraisals: %+v", result)
	}
	e.must(422, "POST", "/v1/records", e.key, "", map[string]any{"appraisals": []any{map[string]any{"bucket": shop, "id": "ap_bad", "target": "ev_retry", "by": "ac_ev_retry", "intensity": 6}}})
}

func TestRecallingAndUsingEventsStrengthensOnlyTheCallersReturnedEvents(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_shop", "ev_retry", "Gateway retry review.")
	e.readers(bucket, "gh_ana")
	historical := `{"context":{"text":"gateway","at":"2026-10-29T12:00:00Z"},"explain":true}`
	snapshot := func(key, token string) episodicResult {
		t.Helper()
		results := e.episodicRecall(key, token, historical)
		if len(results) != 1 {
			t.Fatalf("snapshot: %+v", results)
		}
		return results[0]
	}
	baseline := snapshot(e.key, "")
	if baseline.Record.Recall == nil || baseline.Record.Recall.Count != 0 {
		t.Fatalf("baseline: %+v", baseline)
	}
	expected := math.Pow(0.5, (9*24+2)/24.0/365)
	if math.Abs(baseline.Because.Recency-expected) > 1e-9 {
		t.Fatalf("decay: got %v want %v", baseline.Because.Recency, expected)
	}
	for range 2 {
		e.must(200, "GET", "/v1/events/ev_retry", e.key, "", nil)
		e.must(200, "GET", "/v1/events", e.key, "", nil)
	}
	if got := snapshot(e.key, ""); got.Record.Recall.Count != 0 {
		t.Fatalf("browsing rehearsed: %+v", got)
	}
	e.episodicRecall(e.key, "", `{"context":{"text":"gateway"},"explain":true}`)
	recalled := snapshot(e.key, "")
	if recalled.Record.Recall.Count != 1 || recalled.Because.Recency <= baseline.Because.Recency {
		t.Fatalf("recall did not strengthen: %+v", recalled)
	}
	e.must(204, "POST", "/v1/events/use", e.key, "", `{"events":["ev_retry","ev_retry","missing"]}`)
	used := snapshot(e.key, "")
	if used.Record.Recall.Count != 2 || used.Record.Recall.LastAt == nil || !used.Record.Recall.LastAt.At.Equal(e.now) {
		t.Fatalf("uses: %+v", used.Record.Recall)
	}
	ana := e.sign(e.claims("user_ana", "gh_ana"))
	if got := snapshot(e.key, ana); got.Record.Recall.Count != 0 {
		t.Fatalf("another principal's uses leaked: %+v", got)
	}
	stranger := e.service("ws_acme", "stranger", []string{"read"})
	e.must(204, "POST", "/v1/events/use", stranger, "", `{"events":["ev_retry"]}`)
	e.must(204, "POST", "/v1/events/use", e.key, "", `{"events":["pl_repo_shop"]}`)
	if got := snapshot(e.key, ""); got.Record.Recall.Count != 2 {
		t.Fatalf("hidden or entity IDs rehearsed: %+v", got)
	}
}

func TestRecallRehearsesOnlyItsLimitedResultsAndRespectsEventFilters(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_shop", "ev_retry", "Gateway retry review.")
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"events": []any{
		map[string]any{"bucket": bucket, "id": "ev_old", "type": "deployment.completed", "occurred_at": "2020-01-01T00:00:00Z", "content": []any{map[string]string{"text": "Gateway retry review."}}, "place": "pl_repo_shop"},
	}})
	result := e.episodicRecall(e.key, "", `{"context":{"text":"gateway"},"limit":1,"explain":true}`)
	if len(result) != 1 || result[0].ID != "ev_retry" {
		t.Fatalf("recency ranking: %+v", result)
	}
	results := e.episodicRecall(e.key, "", `{"context":{"place":"pl_repo_shop","at":"2026-10-29T12:00:00Z"},"explain":true}`)
	if len(results) != 2 {
		t.Fatalf("cue candidates: %+v", results)
	}
	for _, result := range results {
		expected := 0
		if result.ID == "ev_retry" {
			expected = 1
		}
		if result.Record.Recall.Count != expected || result.Because.Cue.Score <= 0 {
			t.Fatalf("limited rehearsal and cues: %+v", result)
		}
	}
	results = e.episodicRecall(e.key, "", `{"context":{"text":"gateway"},"filters":{"types":["deployment"]}}`)
	if len(results) != 1 || results[0].ID != "ev_old" {
		t.Fatalf("type lineage: %+v", results)
	}
	results = e.episodicRecall(e.key, "", `{"context":{"text":"gateway"},"filters":{"occurred":{"from":"2026-01-01T00:00:00Z"}}}`)
	if len(results) != 1 || results[0].ID != "ev_retry" {
		t.Fatalf("occurrence filter: %+v", results)
	}
}

func TestSemanticRoutesAndWritesAreRemoved(t *testing.T) {
	e := newEngine(t)
	e.must(404, "GET", "/v1/memories/anything", e.key, "", nil)
	e.must(404, "POST", "/v1/memories/use", e.key, "", `{"memories":["anything"]}`)
	e.must(422, "POST", "/v1/records", e.key, "", `{"memories":[]}`)
	e.must(422, "POST", "/v1/recall", e.key, "", `{"context":{"text":"gateway"},"filters":{"kinds":["memory"]}}`)
	e.must(422, "POST", "/v1/events/use", e.key, "", `{"memories":["anything"]}`)
	for _, table := range []string{"memories", "evidence", "dependencies"} {
		var exists bool
		if err := e.store.Pool().QueryRow(t.Context(), `select to_regclass($1) is not null`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("removed table %s exists", table)
		}
	}
}

func TestKnowledgeChangeExampleCanBeWrittenAndRecalled(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_shop", "ev_retry", "Gateway review.")
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{
		"entities": []any{map[string]any{"bucket": bucket, "id": "sb_knowledge", "class": "subject", "type": "knowledge.item"}},
		"events":   []any{map[string]any{"bucket": bucket, "id": "ev_knowledge", "type": "knowledge.updated", "why": "The gateway rejected five attempts.", "occurred_at": "2026-10-29T10:00:00Z", "participants": []any{map[string]string{"actor": "ac_ev_retry", "role": "author"}}, "subjects": []any{map[string]string{"subject": "sb_knowledge", "role": "about"}}, "attributes": map[string]any{"knowledge.before": map[string]string{"text": "Five attempts."}, "knowledge.after": map[string]string{"text": "Three attempts."}}}},
	})
	results := e.episodicRecall(e.key, "", `{"context":{"subjects":["sb_knowledge"]},"filters":{"types":["knowledge"]}}`)
	if len(results) != 1 || results[0].ID != "ev_knowledge" || len(results[0].Record.Participants) != 1 || results[0].Record.Subjects[0].Subject != "sb_knowledge" || string(results[0].Record.Attributes["knowledge.after"]) != `{"text":"Three attempts."}` {
		t.Fatalf("knowledge event: %+v", results)
	}
}

func TestHistoricalRecallKeepsEarlierUsesAndAppraisals(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_shop", "ev_retry", "Gateway review.")
	var before time.Time
	if err := e.store.Pool().QueryRow(t.Context(), `select clock_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	e.must(204, "POST", "/v1/events/use", e.key, "", `{"events":["ev_retry"]}`)
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"appraisals": []any{map[string]any{"bucket": bucket, "id": "ap_later", "target": "ev_retry", "by": "ac_ev_retry", "valence": 1}}})
	request := map[string]any{"context": map[string]string{"text": "gateway"}, "as_of": before.Format(time.RFC3339Nano), "explain": true}
	historical := e.episodicRecall(e.key, "", request)
	if len(historical) != 1 || historical[0].Record.Recall.Count != 0 || len(historical[0].Record.Appraisals) != 0 {
		t.Fatalf("historical result changed: %+v", historical)
	}
	current := e.episodicRecall(e.key, "", `{"context":{"text":"gateway","at":"2026-10-29T12:00:00Z"},"explain":true}`)
	if len(current) != 1 || current[0].Record.Recall.Count != 1 || len(current[0].Record.Appraisals) != 1 {
		t.Fatalf("current result: %+v", current)
	}
	earlier := e.episodicRecall(e.key, "", `{"context":{"text":"gateway","at":"2026-10-28T12:00:00Z"},"explain":true}`)
	if len(earlier) != 1 || earlier[0].Record.Recall.Count != 0 {
		t.Fatalf("future use counted: %+v", earlier)
	}
}

func TestHiddenEventsDoNotChangeRecallScoresOrCues(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Gateway review.")
	e.readers(shop, "gh_ana")
	ana := e.sign(e.claims("user_ana", "gh_ana"))
	request := `{"context":{"text":"gateway","place":"pl_repo_shop","at":"2026-10-29T12:00:00Z"},"explain":true}`
	before := e.must(200, "POST", "/v1/recall", e.key, ana, request)
	secret := e.repository("repo_secret", "ev_secret", "Gateway review.")
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"events": []any{map[string]any{"bucket": secret, "id": "ev_hidden", "type": "meeting", "place": "pl_repo_shop", "content": []any{map[string]string{"text": "Gateway review."}}}}})
	after := e.must(200, "POST", "/v1/recall", e.key, ana, request)
	if string(before) != string(after) {
		t.Fatalf("hidden records changed response:\nbefore: %s\nafter: %s", before, after)
	}
	e.must(204, "POST", "/v1/events/use", e.key, ana, `{"events":["ev_hidden"]}`)
	results := e.episodicRecall(e.key, "", `{"context":{"text":"gateway","at":"2026-10-29T12:00:00Z"},"filters":{"buckets":["`+secret+`"]}}`)
	for _, result := range results {
		if result.Record.Recall.Count != 0 {
			t.Fatalf("hidden event rehearsed: %+v", result)
		}
	}
	writer := e.service("ws_acme", "writer", []string{"read", "write"})
	if err := e.store.Grant(t.Context(), "ws_acme", shop, "writer", []string{"read", "write"}, "operator"); err != nil {
		t.Fatal(err)
	}
	e.must(422, "POST", "/v1/records", writer, "", map[string]any{"appraisals": []any{map[string]any{"bucket": shop, "id": "ap_hidden_target", "target": "ev_hidden", "by": "ac_ev_retry", "valence": 1}}})
}

func TestRecallRejectsRemovedFilterAndOmitsDiscriminator(t *testing.T) {
	e := newEngine(t)
	e.repository("repo_shop", "ev_retry", "Gateway retry review.")
	for _, value := range []string{"event", "memory"} {
		e.must(422, "POST", "/v1/recall", e.key, "", map[string]any{
			"context": map[string]string{"text": "gateway"},
			"filters": map[string]any{"kinds": []string{value}},
		})
	}
	raw := e.must(200, "POST", "/v1/recall", e.key, "", `{"context":{"text":"gateway"}}`)
	var response struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results: %s", raw)
	}
	for _, result := range response.Results {
		if _, found := result["kind"]; found {
			t.Fatalf("unexpected discriminator: %s", raw)
		}
	}
}

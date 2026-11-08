package httpapi_test

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/trailpad/memory/internal/recall"
	"github.com/trailpad/memory/internal/record"
)

func TestEventLinkingThroughHTTP(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_shop", "ev_seed", "Initial setup.")
	private := e.repository("repo_private", "ev_private", "Hidden work.")
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{
		"entities": []map[string]any{
			{"bucket": bucket, "id": "sb_repo", "class": "subject", "type": "github.repository"},
			{"bucket": bucket, "id": "sb_file", "class": "subject", "type": "code.path"},
			{"bucket": bucket, "id": "sb_knowledge", "class": "subject", "type": "knowledge.item"},
		},
		"events": []map[string]any{
			{"bucket": bucket, "id": "ev_pr", "type": "github.pull_request", "occurred_at": "2026-10-21T09:00:00Z", "place": "pl_repo_shop", "subjects": []map[string]string{{"subject": "sb_repo", "role": "about"}, {"subject": "sb_file", "role": "about"}}, "participants": []map[string]string{{"actor": "ac_ev_seed", "role": "author"}}, "content": []map[string]string{{"text": "Retries stop after three attempts."}}},
			{"bucket": bucket, "id": "ev_review", "type": "github.pull_request.review", "occurred_at": "2026-10-21T10:00:00Z", "part_of": "ev_pr"},
			{"bucket": bucket, "id": "ev_reply", "type": "github.comment", "occurred_at": "2026-10-21T11:00:00Z", "in_reply_to": "ev_pr"},
			{"bucket": bucket, "id": "ev_push", "type": "github.push", "occurred_at": "2026-10-21T10:00:00Z", "place": "pl_repo_shop", "subjects": []map[string]string{{"subject": "sb_repo", "role": "about"}, {"subject": "sb_file", "role": "about"}}, "participants": []map[string]string{{"actor": "ac_ev_seed", "role": "author"}}, "content": []map[string]string{{"text": "Retries stop after three attempts."}}},
			{"bucket": bucket, "id": "ev_capture", "type": "knowledge.captured", "occurred_at": "2026-10-22T10:00:00Z", "subjects": []map[string]string{{"subject": "sb_repo", "role": "about"}, {"subject": "sb_knowledge", "role": "about"}}, "involves": []map[string]string{{"entity": "sb_file", "role": "about"}}},
			{"bucket": bucket, "id": "ev_unrelated", "type": "github.push", "occurred_at": "2026-10-23T10:00:00Z", "subjects": []map[string]string{{"subject": "sb_repo", "role": "about"}}},
			{"bucket": private, "id": "ev_hidden", "type": "github.push", "occurred_at": "2026-10-21T10:00:00Z", "subjects": []map[string]string{{"subject": "sb_file", "role": "about"}}, "part_of": "ev_pr"},
		},
		"links": []map[string]string{{"bucket": bucket, "id": "ln_related", "from": "ev_pr", "to": "ev_capture", "type": "related"}},
	})
	e.readers(bucket, "gh_ana")
	e.readers(private, "gh_hana")
	token := e.sign(e.claims("user_ana", "gh_ana"))
	request := map[string]any{"approaches": []string{"thread", "explicit", "subject", "people", "time", "text"}, "common_subject_share": 0.5, "from": "2026-10-21T00:00:00Z", "to": "2026-10-24T00:00:00Z"}
	var response struct {
		Events []record.Event     `json:"events"`
		Links  []recall.EventLink `json:"links"`
	}
	if err := json.Unmarshal(e.must(200, "POST", "/v1/events/links", e.key, token, request), &response); err != nil {
		t.Fatal(err)
	}
	pairs := map[[2]string]recall.EventLink{}
	for i, l := range response.Links {
		if l.From >= l.To {
			t.Fatalf("pair is not canonical: %+v", l)
		}
		key := [2]string{l.From, l.To}
		if _, ok := pairs[key]; ok {
			t.Fatalf("duplicate %v", key)
		}
		pairs[key] = l
		if l.From == "ev_hidden" || l.To == "ev_hidden" {
			t.Fatal("hidden event returned")
		}
		if i > 0 && response.Links[i-1].Score < l.Score {
			t.Fatal("links not ranked")
		}
	}
	for _, ev := range response.Events {
		if ev.ID == "ev_hidden" {
			t.Fatal("hidden event listed")
		}
	}
	pair := func(a, b string) recall.EventLink {
		if a > b {
			a, b = b, a
		}
		l, ok := pairs[[2]string{a, b}]
		if !ok {
			t.Fatalf("missing %s %s in %+v", a, b, response.Links)
		}
		return l
	}
	thread := pair("ev_pr", "ev_review")
	if !slices.Equal(thread.Because["thread"].Path, []string{"ev_pr", "ev_review"}) || thread.Because["thread"].Score != 1 {
		t.Fatalf("thread evidence: %+v", thread)
	}
	siblings := pair("ev_reply", "ev_review")
	if !slices.Equal(siblings.Because["thread"].Path, []string{"ev_reply", "ev_pr", "ev_review"}) {
		t.Fatalf("sibling path: %+v", siblings)
	}
	for _, id := range []string{"ev_push", "ev_capture"} {
		l := pair("ev_pr", id)
		if !slices.Contains(l.Because["subject"].Shared, "sb_file") {
			t.Fatalf("missing file evidence: %+v", l)
		}
		if l.Score <= 0 {
			t.Fatalf("file does not beat repository: %+v", l)
		}
	}
	push := pair("ev_pr", "ev_push")
	if !slices.Equal(push.Because["people"].Shared, []string{"ac_ev_seed"}) || push.Because["time"].GapSeconds == nil || *push.Because["time"].GapSeconds != 3600 || push.Because["text"].Score <= 0 {
		t.Fatalf("missing independent evidence: %+v", push)
	}
	if pair("ev_pr", "ev_capture").Because["explicit"].Score != 1 {
		t.Fatal("explicit link must score one")
	}
	if _, ok := pairs[[2]string{"ev_pr", "ev_unrelated"}]; ok {
		t.Fatal("repository alone linked events")
	}
	request["approaches"] = []string{"mystery"}
	raw := e.must(422, "POST", "/v1/events/links", e.key, token, request)
	var failure struct {
		Problems []record.Problem `json:"problems"`
	}
	if err := json.Unmarshal(raw, &failure); err != nil {
		t.Fatal(err)
	}
	if len(failure.Problems) == 0 || failure.Problems[0].Field != "approaches[0]" {
		t.Fatalf("missing field error: %s", raw)
	}
	request["approaches"] = []string{"explicit"}
	request["limit"] = 1
	e.must(200, "POST", "/v1/events/links", e.key, token, request)
	var expanded recall.Response
	raw = e.must(200, "POST", "/v1/recall", e.key, token, `{"context":{"subjects":["sb_file"]},"expand":{"neighbours":10},"explain":true}`)
	if err := json.Unmarshal(raw, &expanded); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, r := range expanded.Results {
		for _, n := range r.Neighbours {
			if n.ID == "ev_hidden" {
				t.Fatal("hidden neighbour")
			}
			if n.Record == nil || n.Record.ID != n.ID {
				t.Fatal("missing neighbour record")
			}
			if r.ID == "ev_pr" && n.ID == "ev_review" && n.Because["thread"].Score == 1 {
				seen = true
			}
		}
	}
	if !seen {
		t.Fatalf("review missing from expanded recall: %s", raw)
	}
}

package httpapi_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/trailpad/memory/internal/recall"
	"github.com/trailpad/memory/internal/record"
)

func TestLinkContractThroughHTTP(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_contract", "ev_seed", "Setup")
	events := []map[string]any{
		{"id": "ev_parent", "bucket": bucket, "type": "github.pull_request", "occurred_at": "2026-10-21T08:00:00Z"},
		{"id": "ev_a", "bucket": bucket, "type": "review", "part_of": "ev_parent", "occurred_at": "2026-10-21T09:00:00Z", "subjects": []map[string]string{{"subject": "sb_rare", "role": "about"}, {"subject": "sb_repository", "role": "about"}}, "content": []map[string]string{{"text": "tokens expire refresh"}}},
		{"id": "ev_b", "bucket": bucket, "type": "review", "part_of": "ev_parent", "occurred_at": "2026-10-21T10:00:00Z", "subjects": []map[string]string{{"subject": "sb_rare", "role": "about"}, {"subject": "sb_repository", "role": "about"}}, "content": []map[string]string{{"text": "tokens expire refresh session failures authentication"}}},
	}
	for i := 0; i < 25; i++ {
		events = append(events, map[string]any{"id": fmt.Sprintf("ev_filler_%02d", i), "bucket": bucket, "type": "observation", "occurred_at": "2026-10-22T12:00:00Z", "subjects": []map[string]string{{"subject": "sb_repository", "role": "about"}}})
	}
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"entities": []map[string]string{{"id": "sb_rare", "bucket": bucket, "class": "subject", "type": "code.path"}, {"id": "sb_repository", "bucket": bucket, "class": "subject", "type": "github.repository"}}, "events": events, "links": []map[string]string{{"id": "ln_direction", "bucket": bucket, "from": "ev_b", "to": "ev_a", "type": "related"}}})
	e.readers(bucket, "gh_ana")
	token := e.sign(e.claims("user_ana", "gh_ana"))
	type response struct {
		Links  []recall.EventLink `json:"links"`
		Events []record.Event     `json:"events"`
		Next   string             `json:"next"`
	}
	request := map[string]any{"buckets": []string{bucket}, "from": "2026-10-21T00:00:00Z", "to": "2026-10-23T00:00:00Z", "approaches": []string{"subject"}}
	call := func() response {
		t.Helper()
		raw := e.must(200, "POST", "/v1/events/links", e.key, token, request)
		var out response
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	wide := call()
	if len(wide.Links) != 1 {
		t.Fatalf("repository subject produced links: %+v", wide.Links)
	}
	request["to"] = "2026-10-22T00:00:00Z"
	narrow := call()
	if len(narrow.Links) != 1 || narrow.Links[0].Score != wide.Links[0].Score {
		t.Fatal("window changed subject score")
	}
	request["approaches"] = []string{"explicit", "thread", "subject", "people", "text", "time"}
	out := call()
	if len(out.Links) == 0 || out.Links[0].From != "ev_a" || out.Links[0].To != "ev_b" || out.Links[0].Score != 1 {
		t.Fatalf("explicit score: %+v", out.Links)
	}
	evidence := out.Links[0].Because["explicit"].Links
	if len(evidence) != 1 || evidence[0].ID != "ln_direction" || evidence[0].From != "ev_b" || evidence[0].To != "ev_a" {
		t.Fatalf("explicit identity: %+v", evidence)
	}
	request["weights"] = map[string]float64{"thread": 1.01}
	e.must(422, "POST", "/v1/events/links", e.key, token, request)
	delete(request, "weights")
	request["anchors_limit"] = 1
	request["approaches"] = []string{"explicit"}
	seen := map[string]bool{}
	for pages := 0; ; pages++ {
		if pages > 4 {
			t.Fatal("pagination loop")
		}
		page := call()
		for _, ev := range page.Events {
			participates := false
			for _, l := range page.Links {
				if l.From == ev.ID || l.To == ev.ID {
					participates = true
				}
			}
			if !participates {
				t.Fatal("unlinked event in response")
			}
		}
		if page.Next == "" {
			break
		}
		if seen[page.Next] {
			t.Fatal("repeated cursor")
		}
		seen[page.Next] = true
		request["after"] = page.Next
	}
	if len(seen) != 2 {
		t.Fatalf("anchor pages: %v", seen)
	}
	raw := e.must(200, "POST", "/v1/recall", e.key, token, `{"context":{"subjects":["sb_rare"]},"filters":{"types":["review"]},"expand":{"neighbours":10},"explain":true}`)
	var recalled recall.Response
	if err := json.Unmarshal(raw, &recalled); err != nil {
		t.Fatal(err)
	}
	thread := false
	for _, r := range recalled.Results {
		for _, n := range r.Neighbours {
			if (r.ID == "ev_a" && n.ID == "ev_b") || (r.ID == "ev_b" && n.ID == "ev_a") {
				if slices.Equal(n.Because["thread"].Path, []string{r.ID, "ev_parent", n.ID}) {
					thread = true
				}
			}
		}
	}
	if !thread {
		t.Fatalf("type filtered siblings: %s", raw)
	}
	delete(request, "after")
	request["anchors_limit"] = 2
	request["approaches"] = []string{"text"}
	forward := call()
	request["after"] = "ev_a"
	reverse := call()
	if len(forward.Links) != 1 || len(reverse.Links) != 1 || forward.Links[0].Because["text"].Score != reverse.Links[0].Because["text"].Score {
		t.Fatalf("asymmetric text: %+v %+v", forward, reverse)
	}
}

func TestLinkCandidateTruncationThroughHTTP(t *testing.T) {
	e := newEngine(t)
	bucket := e.repository("repo_truncate", "ev_seed", "Setup")
	events := []map[string]any{{"id": "ev_parent", "bucket": bucket, "type": "observation", "occurred_at": "2026-10-21T00:00:00Z"}}
	for i := 0; i < 205; i++ {
		events = append(events, map[string]any{"id": fmt.Sprintf("ev_child_%03d", i), "bucket": bucket, "type": "observation", "part_of": "ev_parent", "occurred_at": "2026-10-21T12:00:00Z"})
	}
	e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"events": events})
	e.readers(bucket, "gh_ana")
	token := e.sign(e.claims("user_ana", "gh_ana"))
	raw := e.must(200, "POST", "/v1/events/links", e.key, token, map[string]any{"buckets": []string{bucket}, "from": "2026-10-21T00:00:00Z", "to": "2026-10-22T00:00:00Z", "anchors_limit": 1, "approaches": []string{"thread"}})
	var out struct {
		Truncated []string           `json:"truncated"`
		Links     []recall.EventLink `json:"links"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(out.Truncated, "thread") || len(out.Links) != 200 {
		t.Fatalf("truncation: %s", raw)
	}
}

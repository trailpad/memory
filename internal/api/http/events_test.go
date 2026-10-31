package httpapi_test

import (
	"encoding/json"
	"slices"
	"testing"
)

type eventPage struct {
	Events []struct {
		ID     string `json:"id"`
		PartOf string `json:"part_of"`
	} `json:"events"`
	Entities []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Image string `json:"image"`
	} `json:"entities"`
	Next string `json:"next"`
}

func (e *engine) listed(path, key, token string) ([]string, string) {
	e.t.Helper()
	var page eventPage
	if err := json.Unmarshal(e.must(200, "GET", path, key, token, nil), &page); err != nil {
		e.t.Fatal(err)
	}
	ids := []string{}
	for _, ev := range page.Events {
		ids = append(ids, ev.ID)
	}
	return ids, page.Next
}

// review writes a pull request and the comments made on it, as a timeline
// would receive them from GitHub.
func (e *engine) review(bucket string) {
	e.t.Helper()
	e.must(200, "POST", "/v1/records", e.key, "", `{
	  "entities": [{"bucket": "`+bucket+`", "id": "pl_pr_7", "class": "place", "type": "github.pull_request", "name": "#7"}],
	  "events": [
	    {"bucket": "`+bucket+`", "id": "ev_pr_7", "type": "github.pull_request", "occurred_at": "2026-10-21T09:00:00Z", "place": "pl_pr_7"},
	    {"bucket": "`+bucket+`", "id": "ev_pr_7_a", "type": "github.pull_request.review_comment", "occurred_at": "2026-10-21T11:00:00Z", "place": "pl_pr_7", "part_of": "ev_pr_7",
	     "content": [{"text": "Stop after three tries."}]},
	    {"bucket": "`+bucket+`", "id": "ev_pr_7_b", "type": "github.pull_request.review", "occurred_at": "2026-10-22T08:00:00Z", "place": "pl_pr_7", "part_of": "ev_pr_7",
	     "content": [{"text": "Approved."}]},
	    {"bucket": "`+bucket+`", "id": "ev_undated", "type": "github.pull_request.review_comment", "place": "pl_pr_7"}
	  ]}`)
}

func TestEventsAreListedByWhenTheyHappened(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	e.review(shop)

	all, next := e.listed("/v1/events", e.key, "")
	if want := []string{"ev_retry", "ev_pr_7", "ev_pr_7_a", "ev_pr_7_b"}; !slices.Equal(all, want) {
		t.Fatalf("want %v earliest first and nothing undated, got %v", want, all)
	}
	if next != "" {
		t.Fatalf("a short page has nothing after it, got %q", next)
	}

	span, _ := e.listed("/v1/events?from=2026-10-21T00:00:00Z&to=2026-10-22T00:00:00Z", e.key, "")
	if want := []string{"ev_pr_7", "ev_pr_7_a"}; !slices.Equal(span, want) {
		t.Fatalf("from is inclusive and to exclusive: want %v, got %v", want, span)
	}

	top, _ := e.listed("/v1/events?top_level=true", e.key, "")
	if want := []string{"ev_retry", "ev_pr_7"}; !slices.Equal(top, want) {
		t.Fatalf("top level leaves out the parts, want %v, got %v", want, top)
	}

	parts, _ := e.listed("/v1/events?part_of=ev_pr_7", e.key, "")
	if want := []string{"ev_pr_7_a", "ev_pr_7_b"}; !slices.Equal(parts, want) {
		t.Fatalf("want the parts of the pull request %v, got %v", want, parts)
	}
}

func TestAnEventListingIsPaged(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	e.review(shop)

	var seen []string
	path := "/v1/events?limit=3"
	for range 3 {
		ids, next := e.listed(path, e.key, "")
		seen = append(seen, ids...)
		if next == "" {
			break
		}
		path = "/v1/events?limit=3&after=" + next
	}
	if want := []string{"ev_retry", "ev_pr_7", "ev_pr_7_a", "ev_pr_7_b"}; !slices.Equal(seen, want) {
		t.Fatalf("pages together list every event once, want %v, got %v", want, seen)
	}
}

func TestAnEventListingKeepsToWhatThePersonMayRead(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	payroll := e.repository("repo_payroll", "ev_salary", "Retries of salary runs need approval.")
	e.readers(shop, "gh_ana")
	e.readers(payroll, "gh_hana")

	ana := e.sign(e.claims("user_ana", "gh_ana"))
	if got, _ := e.listed("/v1/events", e.key, ana); !slices.Equal(got, []string{"ev_retry"}) {
		t.Fatalf("Ana lists only the shop repository, got %v", got)
	}
	other := e.service("ws_acme", "assistant", []string{"read"})
	if got, _ := e.listed("/v1/events", other, ""); len(got) != 0 {
		t.Fatalf("a service with no grants lists nothing, got %v", got)
	}
}

func TestAnEventListingRefusesWhatItCannotRead(t *testing.T) {
	e := newEngine(t)
	for _, q := range []string{"from=yesterday", "from=2026-10-22T00:00:00Z&to=2026-10-21T00:00:00Z", "limit=0", "limit=100000", "top_level=yes", "top_level=true&part_of=ev_pr_7"} {
		e.must(422, "GET", "/v1/events?"+q, e.key, "", nil)
	}
}

func TestAnEventListingCarriesWhoAndWhere(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	e.must(200, "POST", "/v1/records", e.key, "", `{
	  "entities": [{"bucket": "`+shop+`", "id": "pl_pr_9", "class": "place", "type": "github.pull_request", "name": "#9", "parent": "pl_repo_shop"},
	               {"bucket": "`+shop+`", "id": "ac_ben", "class": "actor", "type": "person", "name": "ben", "image": "https://avatars.example/ben"}],
	  "events": [{"bucket": "`+shop+`", "id": "ev_pr_9", "type": "github.pull_request", "occurred_at": "2026-10-23T09:00:00Z",
	              "place": "pl_pr_9", "participants": [{"actor": "ac_ben", "role": "author"}]}]}`)

	var page eventPage
	if err := json.Unmarshal(e.must(200, "GET", "/v1/events?from=2026-10-23T00:00:00Z", e.key, "", nil), &page); err != nil {
		t.Fatal(err)
	}
	named := map[string]string{}
	for _, en := range page.Entities {
		named[en.ID] = en.Name + " " + en.Image
	}
	want := map[string]string{"pl_pr_9": "#9 ", "pl_repo_shop": "repo_shop ", "ac_ben": "ben https://avatars.example/ben"}
	for id, w := range want {
		if named[id] != w {
			t.Fatalf("%s: want %q, got %q in %v", id, w, named[id], named)
		}
	}
	if len(named) != len(want) {
		t.Fatalf("only what the events name, and the places they sit in: got %v", named)
	}
}

func TestAnEventListingNarrowsToBuckets(t *testing.T) {
	e := newEngine(t)
	shop := e.repository("repo_shop", "ev_retry", "Retries stop after three attempts.")
	payroll := e.repository("repo_payroll", "ev_salary", "Retries of salary runs need approval.")

	if got, _ := e.listed("/v1/events?bucket="+payroll, e.key, ""); !slices.Equal(got, []string{"ev_salary"}) {
		t.Fatalf("one bucket lists its own events, got %v", got)
	}
	if got, _ := e.listed("/v1/events?bucket="+shop+"&bucket="+payroll, e.key, ""); len(got) != 2 {
		t.Fatalf("two buckets list both, got %v", got)
	}
}

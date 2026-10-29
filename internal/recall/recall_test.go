package recall_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/trailpad/memory/internal/capture"
	"github.com/trailpad/memory/internal/profile"
	"github.com/trailpad/memory/internal/recall"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/postgres"
	"github.com/trailpad/memory/internal/store/postgres/pgtest"
)

var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

type world struct {
	capture *capture.Capture
	recall  *recall.Service
	store   *postgres.Store
}

func at(s string) *record.Instant {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	i := record.Exact(t)
	return &i
}

func place(id string, typ record.Type, name, parent string) record.Entity {
	return record.Entity{ID: id, Class: record.PlaceClass, Type: typ, Name: name, Parent: parent}
}

func actor(id string) record.Entity {
	return record.Entity{ID: id, Class: record.ActorClass, Type: "person", Name: id}
}

func subject(id string) record.Entity {
	return record.Entity{ID: id, Class: record.SubjectClass, Type: "code.file", Name: id}
}

func said(speaker, text string) []record.Utterance {
	return []record.Utterance{{Speaker: speaker, Text: text}}
}

// team is a small history: a review thread where Ana set the retry limit,
// a memory drawn from it, an older limit it replaced, a convention of Ben's,
// and a run of Slack chatter Ben is on, which makes Ben a common cue.
func team() store.Batch {
	b := store.Batch{
		Entities: []record.Entity{
			place("pl_repo", "github.repository", "acme/shop", ""),
			place("pl_pr_4521", "github.pull_request", "PR 4521", "pl_repo"),
			place("pl_thread", "github.pull_request.review_thread", "retry.go thread", "pl_pr_4521"),
			place("pl_pr_4600", "github.pull_request", "PR 4600", "pl_repo"),
			place("pl_slack", "slack.channel", "#payments", ""),
			actor("ac_ana"), actor("ac_ben"),
			subject("sb_retry_go"), subject("sb_client_go"), subject("sb_readme"), subject("sb_unused"),
		},
		Events: []record.Event{{
			ID: "ev_comment", Type: "github.pull_request.review_comment", OccurredAt: at("2026-04-19T14:02:11Z"),
			Place:        "pl_thread",
			Participants: []record.Participant{{Actor: "ac_ana", Role: record.Author}, {Actor: "ac_ben", Role: record.Addressee}},
			Subjects:     []record.SubjectRef{{Subject: "sb_retry_go", Role: record.About}},
			Content:      said("ac_ana", "This should stop after three tries; the gateway bans us at five."),
		}},
		Memories: []record.Memory{
			{
				ID: "mm_retry", Type: "constraint", ValidFrom: at("2026-04-19T14:02:11Z"),
				Statement:    "Retries to the payment gateway stop after three attempts.",
				Why:          "The gateway bans a client after five failed calls in a minute.",
				Evidence:     []record.Evidence{{Event: "ev_comment", Role: record.Established}},
				Subjects:     []record.SubjectRef{{Subject: "sb_retry_go", Role: record.About}},
				Participants: []record.Participant{{Actor: "ac_ana", Role: record.Decider}},
			},
			{
				ID: "mm_old", Type: "decision", ValidFrom: at("2025-01-10T00:00:00Z"), ValidUntil: at("2026-04-19T14:02:11Z"),
				Statement: "Retries to the payment gateway stop after five attempts.",
				Subjects:  []record.SubjectRef{{Subject: "sb_retry_go", Role: record.About}},
			},
			{
				ID: "mm_tabs", Type: "convention", ValidFrom: at("2026-09-01T00:00:00Z"),
				Statement:    "Go files in the client package keep their imports grouped.",
				Subjects:     []record.SubjectRef{{Subject: "sb_client_go", Role: record.About}},
				Participants: []record.Participant{{Actor: "ac_ben", Role: record.Author}},
			},
			{
				ID: "mm_objection", Type: "crm.objection", Profiles: []string{"crm@1"},
				Statement: "Acme objects to the gateway pricing.",
			},
		},
	}
	for i, text := range []string{"Deploy went out.", "Lunch at noon?", "Standup moved.", "Rolled back the deploy.", "Docs updated.", "Who has the pager?"} {
		b.Events = append(b.Events, record.Event{
			ID: "ev_chat_" + string(rune('a'+i)), Type: "slack.message", OccurredAt: at("2026-10-2" + string(rune('0'+i)) + "T10:00:00Z"),
			Place:        "pl_slack",
			Participants: []record.Participant{{Actor: "ac_ben", Role: record.Author}},
			Subjects:     []record.SubjectRef{{Subject: "sb_readme", Role: record.Mentioned}},
			Content:      said("ac_ben", text),
		})
	}
	return b
}

func setup(t *testing.T) world {
	t.Helper()
	s := pgtest.ForTest(t)
	profiles := profile.NewRegistry()
	raw, err := os.ReadFile("../../spec/examples/profile.crm.json")
	if err != nil {
		t.Fatal(err)
	}
	crm, err := profile.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.Add(crm); err != nil {
		t.Fatal(err)
	}
	w := world{
		capture: &capture.Capture{Store: s, Profiles: profiles},
		recall:  &recall.Service{Store: s, Profiles: profiles, Now: func() time.Time { return now }},
		store:   s,
	}
	if _, err := w.capture.Ingest(context.Background(), team()); err != nil {
		t.Fatal(err)
	}
	return w
}

// ask decodes the request from JSON, as a caller would send it.
func (w world) ask(t *testing.T, request string) recall.Response {
	t.Helper()
	var req recall.Request
	if err := json.Unmarshal([]byte(request), &req); err != nil {
		t.Fatal(err)
	}
	res, err := w.recall.Recall(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func ids(res recall.Response) []string {
	out := make([]string, len(res.Results))
	for i, r := range res.Results {
		out[i] = r.ID
	}
	return out
}

func find(t *testing.T, res recall.Response, id string) recall.Result {
	t.Helper()
	for _, r := range res.Results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("%s is not among %v", id, ids(res))
	return recall.Result{}
}

func TestWordsFindTheMemoryAndItsEvidence(t *testing.T) {
	w := setup(t)
	res := w.ask(t, `{"context": {"text": "add a retry to the gateway client"}, "explain": true}`)
	if len(res.Results) == 0 || res.Results[0].ID != "mm_retry" {
		t.Fatalf("the retry limit comes first, got %v", ids(res))
	}
	top := res.Results[0]
	if top.Because == nil || top.Because.Relevance != 1 {
		t.Fatalf("the best text match has relevance 1, got %+v", top.Because)
	}
	if len(top.Evidence) != 1 {
		t.Fatalf("want the review comment as evidence, got %+v", top.Evidence)
	}
	q := top.Evidence[0]
	if q.Event != "ev_comment" || q.Role != "established" || q.Quote != "This should stop after three tries; the gateway bans us at five." {
		t.Fatalf("the evidence quotes the comment, got %+v", q)
	}
	if !slices.Equal(q.PlacePath, []string{"acme/shop", "PR 4521", "retry.go thread"}) {
		t.Fatalf("the place path runs from the repository in, got %v", q.PlacePath)
	}
}

func TestACueFindsRecordsWithoutAWordInCommon(t *testing.T) {
	w := setup(t)
	res := w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}, "explain": true}`)
	if !slices.Equal(ids(res)[:2], []string{"mm_retry", "ev_comment"}) && !slices.Equal(ids(res)[:2], []string{"ev_comment", "mm_retry"}) {
		t.Fatalf("the file finds the memory and the comment about it, got %v", ids(res))
	}
	r := find(t, res, "mm_retry")
	if !slices.Equal(r.Because.Path, []string{"sb_retry_go", "mm_retry"}) {
		t.Fatalf("reached straight from the file, got %v", r.Because.Path)
	}
	if !slices.Equal(r.Because.Cue.Shared, []string{"sb_retry_go"}) {
		t.Fatalf("shares the file, got %v", r.Because.Cue.Shared)
	}
}

func TestAParentPlaceReachesWhatHappenedInside(t *testing.T) {
	w := setup(t)
	res := w.ask(t, `{"context": {"place": "pl_pr_4521"}, "explain": true}`)
	r := find(t, res, "ev_comment")
	if !slices.Equal(r.Because.Path, []string{"pl_pr_4521", "pl_thread", "ev_comment"}) {
		t.Fatalf("reached through the thread inside the pull request, got %v", r.Because.Path)
	}
	sibling := find(t, w.ask(t, `{"context": {"place": "pl_pr_4600"}, "explain": true}`), "ev_comment")
	if !slices.Equal(sibling.Because.Cue.Shared, []string{"pl_repo"}) || sibling.Because.Cue.Score >= r.Because.Cue.Score {
		t.Fatalf("a sibling pull request shares only the repository, a weaker cue than the pull request itself, got %+v against %+v", sibling.Because, r.Because)
	}
}

func TestARareCueOutweighsACommonOne(t *testing.T) {
	w := setup(t)
	res := w.ask(t, `{"context": {"participants": ["ac_ben"], "subjects": ["sb_client_go"]}, "explain": true}`)
	if res.Results[0].ID != "mm_tabs" {
		t.Fatalf("the one record on the client file beats Ben's chatter, got %v", ids(res))
	}
	if !slices.Equal(res.Results[0].Because.Cue.Shared, []string{"sb_client_go", "ac_ben"}) {
		t.Fatalf("the file counts for more than Ben, got %v", res.Results[0].Because.Cue.Shared)
	}
}

func TestAStrongerRoleCountsForMore(t *testing.T) {
	w := setup(t)
	res := w.ask(t, `{"context": {"participants": ["ac_ana"]}, "weights": {"cue": 1, "relevance": 0, "recency": 0}}`)
	decided := find(t, res, "mm_retry")
	authored := find(t, res, "ev_comment")
	if decided.Score <= authored.Score {
		t.Fatalf("Ana deciding counts for more than Ana writing, got %v and %v", decided.Score, authored.Score)
	}
}

func TestRetiredMemoriesStayOutUnlessAskedFor(t *testing.T) {
	w := setup(t)
	text := `"context": {"text": "gateway retries attempts"}`
	if slices.Contains(ids(w.ask(t, `{`+text+`}`)), "mm_old") {
		t.Fatal("the replaced limit is not true now")
	}
	if !slices.Contains(ids(w.ask(t, `{`+text+`, "filters": {"valid_at": null}}`)), "mm_old") {
		t.Fatal("valid_at null includes retired memories")
	}
	then := ids(w.ask(t, `{`+text+`, "filters": {"valid_at": "2025-06-01T00:00:00Z"}}`))
	if !slices.Contains(then, "mm_old") || slices.Contains(then, "mm_retry") {
		t.Fatalf("in June 2025 only the old limit held, got %v", then)
	}
}

func TestAsOfAnswersAsTheStoreStoodThen(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	time.Sleep(50 * time.Millisecond)
	before := time.Now()
	time.Sleep(50 * time.Millisecond)
	changed := team().Memories[0]
	changed.Statement = "Retries to the payment gateway stop after two attempts."
	if _, err := w.capture.Ingest(ctx, store.Batch{Memories: []record.Memory{changed}}); err != nil {
		t.Fatal(err)
	}
	statement := func(res recall.Response) string {
		return find(t, res, "mm_retry").Record.(*record.Memory).Statement
	}
	asOf, _ := json.Marshal(record.Exact(before))
	if got := statement(w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}, "as_of": `+string(asOf)+`}`)); got != team().Memories[0].Statement {
		t.Fatalf("as of before the change, the old statement, got %q", got)
	}
	if got := statement(w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}}`)); got != changed.Statement {
		t.Fatalf("now, the new statement, got %q", got)
	}
}

func TestTypeFiltersFollowFallbacks(t *testing.T) {
	w := setup(t)
	got := ids(w.ask(t, `{"context": {"text": "gateway"}, "filters": {"types": ["fact"]}}`))
	if !slices.Equal(got, []string{"mm_objection"}) {
		t.Fatalf("a crm objection falls back to fact and nothing else is one, got %v", got)
	}
	got = ids(w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}, "filters": {"types": ["github.pull_request"]}}`))
	if !slices.Equal(got, []string{"ev_comment"}) {
		t.Fatalf("a review comment is under github.pull_request, got %v", got)
	}
	got = ids(w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}, "filters": {"kinds": ["memory"]}}`))
	if slices.Contains(got, "ev_comment") {
		t.Fatalf("kinds keeps events out, got %v", got)
	}
}

func TestOccurredBoundsWhenThingsHappened(t *testing.T) {
	w := setup(t)
	got := ids(w.ask(t, `{"context": {"participants": ["ac_ben"]}, "filters": {"occurred": {"from": "2026-10-21T00:00:00Z", "to": "2026-10-23T00:00:00Z"}}}`))
	slices.Sort(got)
	if !slices.Equal(got, []string{"ev_chat_b", "ev_chat_c"}) {
		t.Fatalf("only the chatter from the 21st and 22nd, got %v", got)
	}
}

func TestUsingAMemoryKeepsItFresh(t *testing.T) {
	w := setup(t)
	recency := func() float64 {
		return find(t, w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}, "explain": true}`), "mm_retry").Because.Recency
	}
	before := recency()
	if err := w.recall.Use(context.Background(), []string{"mm_retry"}); err != nil {
		t.Fatal(err)
	}
	after := recency()
	if after <= before {
		t.Fatalf("a use brings recency back up, got %v then %v", before, after)
	}
	if want := before + (1-before)*0.5; math.Abs(after-want) > 1e-9 {
		t.Fatalf("a use just now restores half of what had faded, want %v got %v", want, after)
	}
}

func TestWeightsDecideTheScore(t *testing.T) {
	w := setup(t)
	res := w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}, "weights": {"cue": 1, "relevance": 0, "recency": 0}, "explain": true}`)
	for _, r := range res.Results {
		if r.Score != r.Because.Cue.Score {
			t.Fatalf("with only cue weighted, the score is the cue signal, got %v and %v", r.Score, r.Because.Cue.Score)
		}
	}
	if res := w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}}`); res.Results[0].Because != nil || res.Results[0].Evidence != nil {
		t.Fatal("without explain, no because or evidence")
	}
}

func TestLimitAndEmptyAnswers(t *testing.T) {
	w := setup(t)
	if got := w.ask(t, `{"context": {"participants": ["ac_ben"]}, "limit": 2}`); len(got.Results) != 2 {
		t.Fatalf("want 2 results, got %v", ids(got))
	}
	if got := w.ask(t, `{"context": {"text": "kubernetes"}}`); got.Results == nil || len(got.Results) != 0 {
		t.Fatalf("nothing matching is an empty list, got %#v", got.Results)
	}
}

func TestRequestsItCannotHonourAreRefused(t *testing.T) {
	w := setup(t)
	var req recall.Request
	_ = json.Unmarshal([]byte(`{
		"context": {},
		"filters": {"valence": {"max": -1}, "places": {"under": "pl_repo"}, "kinds": ["note"]},
		"expand": {"neighbours": 1},
		"use": "gossip",
		"weights": {"importance": 1},
		"limit": 1000
	}`), &req)
	_, err := w.recall.Recall(context.Background(), req)
	var problems record.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("want problems, got %v", err)
	}
	fields := map[string]bool{}
	for _, p := range problems {
		fields[p.Field] = true
	}
	for _, f := range []string{"context", "filters.valence", "filters.places", "filters.kinds[0]", "expand.neighbours", "use", "weights.importance", "limit"} {
		if !fields[f] {
			t.Errorf("no problem reported on %s; got %v", f, problems)
		}
	}
}

func TestLoadingKeepsToTheScope(t *testing.T) {
	w := setup(t)
	found, err := w.store.Records(context.Background(), store.Scope{Types: []record.Type{"constraint"}},
		[]store.Ref{{Kind: store.MemoryKind, ID: "mm_tabs"}, {Kind: store.MemoryKind, ID: "mm_retry"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Ref.ID != "mm_retry" {
		t.Fatalf("only the constraint loads, got %+v", found)
	}
}

func TestATypeFilterReadsEachRecordWithItsOwnProfileVersion(t *testing.T) {
	w := setup(t)
	raw, err := os.ReadFile("../../spec/examples/profile.crm.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	doc["version"] = "2"
	doc["memory_types"] = []any{map[string]any{"type": "crm.objection", "falls_back_to": "decision"}}
	next, _ := json.Marshal(doc)
	crm2, err := profile.Parse(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.recall.Profiles.Add(crm2); err != nil {
		t.Fatal(err)
	}
	if got := ids(w.ask(t, `{"context": {"text": "gateway pricing"}, "filters": {"types": ["fact"]}}`)); !slices.Equal(got, []string{"mm_objection"}) {
		t.Fatalf("written with crm@1, the objection is still a fact, got %v", got)
	}
	if got := ids(w.ask(t, `{"context": {"text": "gateway pricing"}, "filters": {"types": ["decision"]}}`)); slices.Contains(got, "mm_objection") {
		t.Fatalf("crm@2 does not reach a record written with crm@1, got %v", got)
	}
}

func TestLaterUsesDoNotChangeAnEarlierAnswer(t *testing.T) {
	w := setup(t)
	time.Sleep(50 * time.Millisecond)
	before, _ := json.Marshal(record.Exact(time.Now()))
	time.Sleep(50 * time.Millisecond)
	request := `{"context": {"subjects": ["sb_retry_go"]}, "as_of": ` + string(before) + `, "explain": true}`
	first := find(t, w.ask(t, request), "mm_retry")
	if err := w.recall.Use(context.Background(), []string{"mm_retry"}); err != nil {
		t.Fatal(err)
	}
	again := find(t, w.ask(t, request), "mm_retry")
	if again.Because.Recency != first.Because.Recency || again.Record.(*record.Memory).Recall.Count != 0 {
		t.Fatalf("a use made later is not part of the earlier answer, got %+v then %+v", first.Because, again.Because)
	}
	now := find(t, w.ask(t, `{"context": {"subjects": ["sb_retry_go"]}}`), "mm_retry")
	if now.Record.(*record.Memory).Recall.Count != 1 {
		t.Fatalf("the use counts now, got %+v", now.Record.(*record.Memory).Recall)
	}
	earlier := find(t, w.ask(t, `{"context": {"subjects": ["sb_retry_go"], "at": "2026-10-01T00:00:00Z"}, "explain": true}`), "mm_retry")
	plain := find(t, w.ask(t, `{"context": {"subjects": ["sb_retry_go"], "at": "2026-10-01T00:00:00Z"}, "filters": {"valid_at": "2026-10-01T00:00:00Z"}, "explain": true}`), "mm_retry")
	if earlier.Because.Recency != plain.Because.Recency {
		t.Fatal("same moment, same recency")
	}
	if want := math.Pow(0.5, now.Record.(*record.Memory).ValidFrom.At.Sub(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)).Hours()/24/-540); math.Abs(earlier.Because.Recency-want) > 1e-9 {
		t.Fatalf("a use after the moment asked about does not count, want %v got %v", want, earlier.Because.Recency)
	}
}

func TestValidAtMeansNowNotTheContextsTime(t *testing.T) {
	w := setup(t)
	for _, req := range []string{
		`{"context": {"text": "gateway retries attempts", "at": "2025-06-01T00:00:00Z"}}`,
		`{"context": {"text": "gateway retries attempts", "at": "2025-06-01T00:00:00Z"}, "filters": {"valid_at": "now"}}`,
	} {
		got := ids(w.ask(t, req))
		if !slices.Contains(got, "mm_retry") || slices.Contains(got, "mm_old") {
			t.Fatalf("what holds now, whatever the context's time, got %v for %s", got, req)
		}
	}
}

func TestAContextCueNothingSharesStillCounts(t *testing.T) {
	w := setup(t)
	alone := find(t, w.ask(t, `{"context": {"subjects": ["sb_client_go"]}, "explain": true}`), "mm_tabs")
	half := find(t, w.ask(t, `{"context": {"subjects": ["sb_client_go", "sb_unused"]}, "explain": true}`), "mm_tabs")
	if math.Abs(half.Because.Cue.Score-alone.Because.Cue.Score/2) > 1e-9 {
		t.Fatalf("sharing one of two equally rare cues is half the share, got %v against %v", half.Because.Cue.Score, alone.Because.Cue.Score)
	}
}

func TestWeightsMustAddUpToSomething(t *testing.T) {
	w := setup(t)
	for request, field := range map[string]string{
		`{"context": {"text": "x"}, "weights": {"relevance": 1e308, "cue": 1e308, "recency": 1e308}}`: "weights.cue",
		`{"context": {"text": "x"}, "weights": {"relevance": 0, "cue": 0, "recency": 0}}`:             "weights",
	} {
		var req recall.Request
		_ = json.Unmarshal([]byte(request), &req)
		_, err := w.recall.Recall(context.Background(), req)
		var problems record.Problems
		if !errors.As(err, &problems) || !slices.ContainsFunc(problems, func(p record.Problem) bool { return p.Field == field }) {
			t.Errorf("%s: want a problem on %s, got %v", request, field, err)
		}
	}
}

func TestAnEarlierUseCountsWhenALaterOneDoesNot(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	for _, day := range []time.Time{time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)} {
		if err := w.store.Rehearse(ctx, []string{"mm_retry"}, day); err != nil {
			t.Fatal(err)
		}
	}
	got := find(t, w.ask(t, `{"context": {"subjects": ["sb_retry_go"], "at": "2026-06-01T00:00:00Z"}, "explain": true}`), "mm_retry")
	if last := got.Record.(*record.Memory).Recall.LastAt; last == nil || !last.At.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("on 1 June the last use was 1 May, got %+v", got.Record.(*record.Memory).Recall)
	}
	happened := time.Date(2026, 4, 19, 14, 2, 11, 0, time.UTC)
	june := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	d := math.Pow(0.5, june.Sub(happened).Hours()/24/540)
	want := d + (1-d)*0.5*math.Pow(0.5, june.Sub(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)).Hours()/24/540)
	if math.Abs(got.Because.Recency-want) > 1e-9 {
		t.Fatalf("the May use refreshes it, want %v got %v", want, got.Because.Recency)
	}
}

func TestAFallbackChainCanCrossProfiles(t *testing.T) {
	w := setup(t)
	sales, err := profile.Parse([]byte(`{"profile": "sales", "version": "1",
		"memory_types": [{"type": "sales.rule", "falls_back_to": "crm.objection"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.recall.Profiles.Add(sales); err != nil {
		t.Fatal(err)
	}
	if _, err := w.capture.Ingest(context.Background(), store.Batch{Memories: []record.Memory{{
		ID: "mm_rule", Type: "sales.rule", Profiles: []string{"crm@1", "sales@1"}, Statement: "Never discount gateway pricing.",
	}}}); err != nil {
		t.Fatal(err)
	}
	got := ids(w.ask(t, `{"context": {"text": "gateway pricing"}, "filters": {"types": ["fact"]}}`))
	slices.Sort(got)
	if !slices.Equal(got, []string{"mm_objection", "mm_rule"}) {
		t.Fatalf("a sales rule is an objection, which is a fact, got %v", got)
	}
}

func TestRejectedTypesNeverCrowdOutAMatch(t *testing.T) {
	w := setup(t)
	raw, _ := os.ReadFile("../../spec/examples/profile.crm.json")
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	doc["version"] = "2"
	doc["memory_types"] = []any{map[string]any{"type": "crm.objection", "falls_back_to": "decision"}}
	next, _ := json.Marshal(doc)
	crm2, err := profile.Parse(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.recall.Profiles.Add(crm2); err != nil {
		t.Fatal(err)
	}
	var crowd []record.Memory
	for i := 0; i < 60; i++ {
		crowd = append(crowd, record.Memory{ID: fmt.Sprintf("mm_crowd_%02d", i), Type: "crm.objection", Profiles: []string{"crm@2"},
			Statement: "Gateway pricing gateway pricing gateway pricing."})
	}
	if _, err := w.capture.Ingest(context.Background(), store.Batch{Memories: crowd}); err != nil {
		t.Fatal(err)
	}
	if got := ids(w.ask(t, `{"context": {"text": "gateway pricing"}, "filters": {"types": ["fact"]}}`)); !slices.Equal(got, []string{"mm_objection"}) {
		t.Fatalf("the one fact is found past sixty stronger decisions, got %v", got)
	}
}

func TestCapturingAgainRepairsAnOlderTypeChain(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	objection := team().Memories[3]
	if _, err := w.store.Pool().Exec(ctx, `update memories set type_lineage = '{crm.objection,crm}' where id = 'mm_objection'`); err != nil {
		t.Fatal(err)
	}
	if got := ids(w.ask(t, `{"context": {"text": "gateway pricing"}, "filters": {"types": ["fact"]}}`)); slices.Contains(got, "mm_objection") {
		t.Fatalf("with only its namespace parents, it is not yet a fact, got %v", got)
	}
	if _, err := w.store.Write(ctx, store.Batch{Memories: []record.Memory{objection}}); err != nil {
		t.Fatal(err)
	}
	if got := ids(w.ask(t, `{"context": {"text": "gateway pricing"}, "filters": {"types": ["fact"]}}`)); slices.Contains(got, "mm_objection") {
		t.Fatalf("a write without a chain leaves the stored one alone, got %v", got)
	}
	written, err := w.capture.Ingest(ctx, store.Batch{Memories: []record.Memory{objection}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(written.Unchanged, "mm_objection") {
		t.Fatalf("the memory itself is unchanged, got %+v", written)
	}
	if got := ids(w.ask(t, `{"context": {"text": "gateway pricing"}, "filters": {"types": ["fact"]}}`)); !slices.Equal(got, []string{"mm_objection"}) {
		t.Fatalf("captured again, it is a fact, got %v", got)
	}
}

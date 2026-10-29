package recall_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
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

// office is a store with workspaces whose engineering and HR records live in
// separate buckets.
type office struct {
	store   *postgres.Store
	capture *capture.Capture
	recall  *recall.Service
}

func newOffice(t *testing.T) office {
	t.Helper()
	s := pgtest.ForTest(t)
	profiles := profile.NewRegistry()
	return office{
		store:   s,
		capture: &capture.Capture{Store: s, Profiles: profiles},
		recall:  &recall.Service{Store: s, Profiles: profiles, Now: func() time.Time { return now }},
	}
}

// workspace creates a workspace with an engineering and an HR bucket.
func (o office) workspace(t *testing.T, id string) {
	t.Helper()
	pgtest.Workspace(t, o.store, id, "pr_admin", "bk_eng", "bk_hr")
}

func as(workspace, principal string, buckets ...string) store.Access {
	return store.Access{Workspace: workspace, Principal: principal, Read: buckets, Write: buckets}
}

var (
	engineer = func(ws string) store.Access { return as(ws, "pr_ben", "bk_eng") }
	hrLead   = func(ws string) store.Access { return as(ws, "pr_hana", "bk_eng", "bk_hr") }
)

func (o office) ingest(t *testing.T, access store.Access, b store.Batch) {
	t.Helper()
	if _, err := o.capture.Ingest(context.Background(), access, b); err != nil {
		t.Fatal(err)
	}
}

func (o office) ask(t *testing.T, access store.Access, request string) recall.Response {
	t.Helper()
	var req recall.Request
	if err := json.Unmarshal([]byte(request), &req); err != nil {
		t.Fatal(err)
	}
	res, err := o.recall.Recall(context.Background(), access, req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// engineering is what engineers may read.
func engineering() store.Batch {
	return store.Batch{
		Entities: []record.Entity{
			{Bucket: "bk_eng", ID: "pl_eng", Class: record.PlaceClass, Type: "slack.channel", Name: "#eng"},
			{Bucket: "bk_eng", ID: "ac_ana", Class: record.ActorClass, Type: "person", Name: "Ana"},
		},
		Events: []record.Event{{
			Bucket: "bk_eng", ID: "ev_deploy", Type: "slack.message", OccurredAt: at("2026-10-20T10:00:00Z"), Place: "pl_eng",
			Participants: []record.Participant{{Actor: "ac_ana", Role: record.Author}},
			Content:      said("ac_ana", "The gateway retry fix ships on Friday."),
		}},
		Memories: []record.Memory{{
			Bucket: "bk_eng", ID: "mm_deploy", Type: "decision", Statement: "Gateway retry fixes ship on Fridays.",
			ValidFrom: at("2026-10-20T10:00:00Z"),
			Evidence:  []record.Evidence{{Event: "ev_deploy", Role: record.Established}},
		}},
	}
}

// personnel is what only HR may read, though it shares words and people with
// engineering, and one memory written into engineering was made from it.
func personnel() store.Batch {
	return store.Batch{
		Entities: []record.Entity{
			{Bucket: "bk_hr", ID: "pl_hr", Class: record.PlaceClass, Type: "slack.channel", Name: "#promotions"},
			{Bucket: "bk_eng", ID: "pl_eng_thread", Class: record.PlaceClass, Type: "slack.thread", Name: "thread", Parent: "pl_hr"},
		},
		Events: []record.Event{{
			Bucket: "bk_hr", ID: "ev_review", Type: "slack.message", OccurredAt: at("2026-10-21T10:00:00Z"), Place: "pl_hr",
			Participants: []record.Participant{{Actor: "ac_ana", Role: record.Mentioned}},
			Content:      said("ac_ana", "Ana's gateway retry work puts her up for promotion."),
		}},
		Memories: []record.Memory{
			{
				Bucket: "bk_hr", ID: "mm_promotion", Type: "fact", Statement: "Ana is up for promotion for the gateway retry work.",
				ValidFrom:    at("2026-10-21T10:00:00Z"),
				Participants: []record.Participant{{Actor: "ac_ana", Role: record.Author}},
				Evidence:     []record.Evidence{{Event: "ev_review", Role: record.Established}},
			},
			{
				Bucket: "bk_eng", ID: "mm_derived", Type: "fact", Statement: "Gateway retry work matters for careers here.",
				ValidFrom: at("2026-10-21T11:00:00Z"),
				DependsOn: []record.Dependency{{Record: "ev_review"}},
			},
		},
	}
}

func TestAMemoryNeedsEverythingItWasMadeFrom(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	ctx := context.Background()

	if _, err := o.store.Memory(ctx, engineer("ws_acme"), "mm_derived", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a memory in engineering made from an HR message is not engineering's to read, got %v", err)
	}
	if _, err := o.store.Memory(ctx, hrLead("ws_acme"), "mm_derived", nil); err != nil {
		t.Fatalf("someone who may read both reads it: %v", err)
	}
	res := o.ask(t, engineer("ws_acme"), `{"context": {"text": "gateway retry promotion"}}`)
	if got := ids(res); !slices.Equal(got, []string{"mm_deploy", "ev_deploy"}) && !slices.Equal(got, []string{"ev_deploy", "mm_deploy"}) {
		t.Fatalf("an engineer recalls only engineering, got %v", got)
	}
	res = o.ask(t, hrLead("ws_acme"), `{"context": {"text": "gateway retry promotion"}}`)
	if !slices.Contains(ids(res), "mm_promotion") || !slices.Contains(ids(res), "mm_derived") {
		t.Fatalf("HR recalls both, got %v", ids(res))
	}
}

func TestHiddenRecordsChangeNothingAnEngineerSees(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_plain")
	o.workspace(t, "ws_secret")
	o.ingest(t, hrLead("ws_plain"), engineering())
	o.ingest(t, hrLead("ws_secret"), engineering())
	o.ingest(t, hrLead("ws_secret"), personnel())

	for _, request := range []string{
		`{"context": {"text": "gateway retry promotion"}, "explain": true, "expand": {"evidence": true}}`,
		`{"context": {"participants": ["ac_ana"]}, "explain": true}`,
		`{"context": {"place": "pl_hr", "text": "gateway"}, "explain": true}`,
		`{"context": {"place": "pl_eng_thread", "text": "Friday"}, "explain": true}`,
	} {
		plain, err := json.Marshal(o.ask(t, engineer("ws_plain"), request))
		if err != nil {
			t.Fatal(err)
		}
		secret, err := json.Marshal(o.ask(t, engineer("ws_secret"), request))
		if err != nil {
			t.Fatal(err)
		}
		if string(plain) != string(secret) {
			t.Fatalf("records the engineer may not read changed the answer to %s:\nwithout: %s\nwith:    %s", request, plain, secret)
		}
	}
}

func TestWorkspacesShareNothing(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.workspace(t, "ws_globex")
	b := engineering()
	b.Events[0].Source = &record.Source{System: "slack", Installation: "T1", NativeID: "msg-1"}
	o.ingest(t, engineer("ws_acme"), b)
	c := engineering()
	c.Events[0].Source = &record.Source{System: "slack", Installation: "T1", NativeID: "msg-1"}
	c.Memories[0].Statement = "Globex ships gateway retry fixes on Mondays."
	o.ingest(t, engineer("ws_globex"), c)

	ctx := context.Background()
	acme, err := o.store.Memory(ctx, engineer("ws_acme"), "mm_deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	globex, err := o.store.Memory(ctx, engineer("ws_globex"), "mm_deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if acme.Statement == globex.Statement {
		t.Fatal("the same id in two workspaces is two records")
	}
	res := o.ask(t, engineer("ws_acme"), `{"context": {"text": "Mondays"}}`)
	if len(res.Results) != 0 {
		t.Fatalf("one workspace never finds another's records, got %v", ids(res))
	}
}

func TestTheSameSourceInTwoInstallationsIsTwoRecords(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	b := engineering()
	b.Events[0].Source = &record.Source{System: "slack", Installation: "T1", NativeID: "msg-1"}
	o.ingest(t, engineer("ws_acme"), b)
	other := record.Event{Bucket: "bk_eng", ID: "ev_other", Type: "slack.message", Content: said("", "Unrelated."),
		Source: &record.Source{System: "slack", Installation: "T2", NativeID: "msg-1"}}
	written, err := o.capture.Ingest(context.Background(), engineer("ws_acme"), store.Batch{Events: []record.Event{other}})
	if err != nil {
		t.Fatal(err)
	}
	if written.Stored["ev_other"] != "ev_other" {
		t.Fatalf("a message from another installation is not the one already stored, got %v", written.Stored)
	}
}

func TestWritingKeepsToWritableBuckets(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	ctx := context.Background()
	b := engineering()
	b.Events[0].Bucket = "bk_hr"
	if _, err := o.capture.Ingest(ctx, engineer("ws_acme"), b); !errors.Is(err, store.ErrBucket) {
		t.Fatalf("an engineer cannot write into HR, got %v", err)
	}
	if err := o.store.DeleteBucket(ctx, "ws_acme", "bk_eng"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.capture.Ingest(ctx, engineer("ws_acme"), engineering()); !errors.Is(err, store.ErrBucket) {
		t.Fatalf("nothing is written into a deleted bucket, got %v", err)
	}
	if err := o.store.CreateBucket(ctx, "ws_acme", store.Bucket{ID: "bk_eng"}); err == nil {
		t.Fatal("a deleted bucket's id is never used again")
	}
	if _, err := o.capture.Ingest(ctx, store.Access{}, engineering()); !errors.Is(err, store.ErrNoAccess) {
		t.Fatalf("a request naming no workspace writes nothing, got %v", err)
	}
	if _, err := o.recall.Recall(ctx, store.Access{}, recall.Request{Context: recall.Context{Text: "gateway"}}); !errors.Is(err, store.ErrNoAccess) {
		t.Fatalf("a request naming no workspace reads nothing, got %v", err)
	}
	missing := engineering()
	missing.Events[0].Bucket = ""
	_, err := o.capture.Ingest(ctx, hrLead("ws_acme"), missing)
	var problems record.Problems
	if !errors.As(err, &problems) || !strings.Contains(problems.Error(), "events[0].bucket: is required") {
		t.Fatalf("every record names its bucket, got %v", err)
	}
}

func TestAReferenceToAHiddenRecordLooksMissing(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	ctx := context.Background()
	write := func(place string) error {
		_, err := o.capture.Ingest(ctx, engineer("ws_acme"), store.Batch{Events: []record.Event{{
			Bucket: "bk_eng", ID: "ev_probe", Type: "slack.message", Place: place, Content: said("", "probe"),
		}}})
		return err
	}
	hidden, nothing := write("pl_hr"), write("pl_nowhere")
	if hidden == nil || nothing == nil {
		t.Fatalf("both references are refused, got %v and %v", hidden, nothing)
	}
	if strings.ReplaceAll(hidden.Error(), "pl_hr", "X") != strings.ReplaceAll(nothing.Error(), "pl_nowhere", "X") {
		t.Fatalf("a hidden record and no record are refused alike:\n%v\n%v", hidden, nothing)
	}
	kinds, err := o.store.Kinds(ctx, engineer("ws_acme"), []string{"pl_hr", "mm_promotion", "mm_derived", "pl_eng"})
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 1 || kinds["pl_eng"].Record != "entity" {
		t.Fatalf("an engineer learns the kind of engineering records only, got %v", kinds)
	}
}

func TestADuplicateOfAHiddenRecordRevealsNothing(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	ctx := context.Background()
	secret := record.Event{Bucket: "bk_hr", ID: "ev_secret", Type: "slack.message", Content: said("", "Salary bands."),
		Source: &record.Source{System: "slack", Installation: "T1", NativeID: "msg-9"}}
	o.ingest(t, hrLead("ws_acme"), store.Batch{Events: []record.Event{secret}})

	copy := record.Event{Bucket: "bk_eng", ID: "ev_copy", Type: "slack.message", Content: said("", "Salary bands."),
		Source: &record.Source{System: "slack", Installation: "T1", NativeID: "msg-9"}}
	written, err := o.capture.Ingest(ctx, engineer("ws_acme"), store.Batch{Events: []record.Event{copy}})
	if !errors.Is(err, postgres.ErrTaken) || strings.Contains(err.Error(), "ev_secret") {
		t.Fatalf("the engineer learns only that the source is taken, got %v and %v", written, err)
	}
	_, err = o.capture.Ingest(ctx, engineer("ws_acme"), store.Batch{Events: []record.Event{{
		Bucket: "bk_eng", ID: "ev_secret", Type: "slack.message", Content: said("", "Guess."),
	}}})
	if !errors.Is(err, postgres.ErrTaken) {
		t.Fatalf("writing over a hidden record's id is refused as taken, got %v", err)
	}
}

func TestRecordsStayInTheirBucket(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	moved := engineering()
	moved.Memories[0].Bucket = "bk_hr"
	moved.Memories[0].Statement = "Moved."
	if _, err := o.capture.Ingest(context.Background(), hrLead("ws_acme"), store.Batch{Memories: moved.Memories}); !errors.Is(err, store.ErrMove) {
		t.Fatalf("a record cannot move to another bucket, got %v", err)
	}
}

func TestANewVersionNeverWidensWhoMayRead(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	cleaned := personnel().Memories[1]
	cleaned.DependsOn = nil
	cleaned.Statement = "Gateway retry work matters."
	o.ingest(t, hrLead("ws_acme"), store.Batch{Memories: []record.Memory{cleaned}})
	if _, err := o.store.Memory(context.Background(), engineer("ws_acme"), "mm_derived", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("dropping a dependency in a later version does not hand the memory to engineering, got %v", err)
	}
}

func TestAMemoryKeepsTheRestrictionsOfTheVersionItRead(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	ctx := context.Background()
	// mm_deploy is at version 1, made from engineering alone. Version 2 adds
	// an HR message.
	widened := engineering().Memories[0]
	widened.Evidence = append(widened.Evidence, record.Evidence{Event: "ev_review", Role: record.Reinforced})
	o.ingest(t, hrLead("ws_acme"), store.Batch{Memories: []record.Memory{widened}})

	early := record.Memory{Bucket: "bk_eng", ID: "mm_from_v1", Type: "fact", Statement: "Fridays.",
		DependsOn: []record.Dependency{{Record: "mm_deploy", Version: 1}}}
	late := record.Memory{Bucket: "bk_eng", ID: "mm_from_v2", Type: "fact", Statement: "Fridays, and why.",
		DependsOn: []record.Dependency{{Record: "mm_deploy"}}}
	o.ingest(t, hrLead("ws_acme"), store.Batch{Memories: []record.Memory{early, late}})

	if _, err := o.store.Memory(ctx, engineer("ws_acme"), "mm_from_v1", nil); err != nil {
		t.Fatalf("made from version 1, it needs engineering only: %v", err)
	}
	if _, err := o.store.Memory(ctx, engineer("ws_acme"), "mm_from_v2", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("made from version 2, it needs HR too, got %v", err)
	}
	var version int
	if err := o.store.Pool().QueryRow(ctx, `select input_version from dependencies where workspace_id = 'ws_acme' and memory_id = 'mm_from_v2'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("a dependency named without a version records the version read, got %d", version)
	}
}

func TestALinkAndAnAppraisalNeedWhatTheyPointAt(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	two := 2
	o.ingest(t, hrLead("ws_acme"), store.Batch{
		Links:      []record.Link{{Bucket: "bk_eng", ID: "ln_x", From: "mm_deploy", Type: "relates_to", To: "mm_promotion"}},
		Appraisals: []record.Appraisal{{Bucket: "bk_eng", ID: "ap_x", Target: "mm_promotion", By: "ac_ana", Importance: &two}},
	})
	kinds, err := o.store.Kinds(context.Background(), engineer("ws_acme"), []string{"ln_x", "ap_x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 0 {
		t.Fatalf("a link to HR and a judgement of HR are HR's to read, though written in engineering, got %v", kinds)
	}
}

func TestUsesAreCountedPerPrincipal(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	ctx := context.Background()
	ana := as("ws_acme", "pr_ana", "bk_eng")
	if err := o.recall.Use(ctx, ana, []string{"mm_deploy"}); err != nil {
		t.Fatal(err)
	}
	mine, err := o.store.Memory(ctx, ana, "mm_deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := o.store.Memory(ctx, engineer("ws_acme"), "mm_deploy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if mine.Recall.Count != 1 || theirs.Recall.Count != 0 {
		t.Fatalf("one person's use is theirs alone, got %d and %d", mine.Recall.Count, theirs.Recall.Count)
	}
	if err := o.recall.Use(ctx, engineer("ws_acme"), []string{"mm_promotion"}); err != nil {
		t.Fatal(err)
	}
	var uses int
	if err := o.store.Pool().QueryRow(ctx, `select count(*) from rehearsals where principal = 'pr_ben'`).Scan(&uses); err != nil {
		t.Fatal(err)
	}
	if uses != 0 {
		t.Fatal("nobody records a use of a memory they may not read")
	}
}

func TestNarrowingToABucketNeverWidens(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	res := o.ask(t, engineer("ws_acme"), `{"context": {"text": "promotion"}, "filters": {"buckets": ["bk_hr"]}}`)
	if len(res.Results) != 0 {
		t.Fatalf("asking for HR gives an engineer nothing, got %v", ids(res))
	}
	res = o.ask(t, hrLead("ws_acme"), `{"context": {"text": "gateway retry"}, "filters": {"buckets": ["bk_hr"]}}`)
	for _, id := range ids(res) {
		if id == "mm_deploy" || id == "ev_deploy" || id == "mm_derived" {
			t.Fatalf("narrowed to HR, nothing in engineering comes back, got %v", ids(res))
		}
	}
}

func TestAPlacePathStopsAtAPlaceItMayNotRead(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	o.ingest(t, hrLead("ws_acme"), store.Batch{
		Events: []record.Event{{
			Bucket: "bk_eng", ID: "ev_threaded", Type: "slack.message", OccurredAt: at("2026-10-22T10:00:00Z"),
			Place: "pl_eng_thread", Content: said("", "Canary first, then the fleet."),
		}},
		Memories: []record.Memory{{
			Bucket: "bk_eng", ID: "mm_canary", Type: "convention", Statement: "Deploys go to the canary first.",
			ValidFrom: at("2026-10-22T10:00:00Z"),
			Evidence:  []record.Evidence{{Event: "ev_threaded", Role: record.Established}},
		}},
	})
	res := o.ask(t, engineer("ws_acme"), `{"context": {"text": "canary"}, "expand": {"evidence": true}}`)
	if len(res.Results) == 0 || len(res.Results[0].Evidence) != 1 {
		t.Fatalf("the canary memory comes back with its evidence, got %+v", res.Results)
	}
	path := res.Results[0].Evidence[0].PlacePath
	if !slices.Equal(path, []string{"thread"}) {
		t.Fatalf("the path shows the thread and nothing above it the engineer may not read, got %v", path)
	}
}

func TestTheStoreRefusesInputsTheWriterMayNotRead(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	_, err := o.store.Write(context.Background(), engineer("ws_acme"), store.Batch{Memories: []record.Memory{{
		Bucket: "bk_eng", ID: "mm_guess", Type: "fact", Statement: "Guess.",
		DependsOn: []record.Dependency{{Record: "ev_review"}},
	}}})
	if err == nil || !strings.Contains(err.Error(), "ev_review is not in this batch or the store") {
		t.Fatalf("without capture in front, the store still refuses an input the writer may not read, got %v", err)
	}
}

func TestReadRecordsNameNothingHidden(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	two := 2
	o.ingest(t, hrLead("ws_acme"), store.Batch{
		Entities: []record.Entity{{Bucket: "bk_hr", ID: "ac_hana", Class: record.ActorClass, Type: "person", Name: "Hana"}},
		Events: []record.Event{{
			Bucket: "bk_eng", ID: "ev_standup", Type: "slack.message", Place: "pl_eng",
			Participants: []record.Participant{{Actor: "ac_ana", Role: record.Author}, {Actor: "ac_hana", Role: record.Mentioned}},
			Content:      said("ac_hana", "Standup moves to ten."),
			Appraisals:   []string{"ap_hidden", "ap_never_written"},
		}},
		Memories: []record.Memory{{
			Bucket: "bk_eng", ID: "mm_standup", Type: "convention", Statement: "Standup is at ten.",
			Participants: []record.Participant{{Actor: "ac_hana", Role: record.Author}},
			Evidence:     []record.Evidence{{Event: "ev_standup", Role: record.Established}},
		}},
		Appraisals: []record.Appraisal{{Bucket: "bk_hr", ID: "ap_hidden", Target: "ev_standup", By: "ac_hana", Importance: &two}},
	})
	ctx := context.Background()
	ev, err := o.store.Event(ctx, engineer("ws_acme"), "ev_standup")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ev)
	if strings.Contains(string(raw), "ac_hana") || strings.Contains(string(raw), "ap_") || len(ev.Participants) != 1 {
		t.Fatalf("an engineer reads the event without the HR actor or judgements, got %s", raw)
	}
	m, err := o.store.Memory(ctx, engineer("ws_acme"), "mm_standup", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Participants) != 0 || len(m.Evidence) != 1 {
		t.Fatalf("the memory keeps its evidence and loses the HR author, got %+v", m)
	}
	thread, err := o.store.Entity(ctx, engineer("ws_acme"), "pl_eng_thread")
	if err != nil {
		t.Fatal(err)
	}
	if thread.Parent != "" {
		t.Fatalf("the thread's HR parent is not named, got %q", thread.Parent)
	}
	full, err := o.store.Event(ctx, hrLead("ws_acme"), "ev_standup")
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Participants) != 2 || len(full.Appraisals) != 1 {
		t.Fatalf("someone who may read both sees both, got %+v", full)
	}
}

func TestBatchCopiesOfOneMemoryKeepEveryRestriction(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	src := &record.Source{System: "extractor", Installation: "x", NativeID: "m-1"}
	first := record.Memory{Bucket: "bk_eng", ID: "mm_a", Type: "fact", Statement: "From HR.", Source: src,
		DependsOn: []record.Dependency{{Record: "ev_review"}}}
	second := record.Memory{Bucket: "bk_eng", ID: "mm_b", Type: "fact", Statement: "Cleaned.", Source: src}
	o.ingest(t, hrLead("ws_acme"), store.Batch{Memories: []record.Memory{first, second}})
	if _, err := o.store.Memory(context.Background(), engineer("ws_acme"), "mm_a", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("the later copy keeps the earlier copy's HR restriction, got %v", err)
	}
}

func TestAnExplicitVersionInTheSameBatchIsTheStoredOne(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	widened := engineering().Memories[0]
	widened.Evidence = append(widened.Evidence, record.Evidence{Event: "ev_review", Role: record.Reinforced})
	early := record.Memory{Bucket: "bk_eng", ID: "mm_from_v1", Type: "fact", Statement: "Fridays.",
		DependsOn: []record.Dependency{{Record: "mm_deploy", Version: 1}}}
	o.ingest(t, hrLead("ws_acme"), store.Batch{Memories: []record.Memory{widened, early}})
	ctx := context.Background()
	if _, err := o.store.Memory(ctx, engineer("ws_acme"), "mm_from_v1", nil); err != nil {
		t.Fatalf("made from version 1 as stored, it needs engineering only: %v", err)
	}
	var version int
	if err := o.store.Pool().QueryRow(ctx, `select input_version from dependencies where workspace_id = 'ws_acme' and memory_id = 'mm_from_v1'`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("the dependency records version 1, got %d", version)
	}
	// An engineer may capture a memory made from version 1, though the
	// current version is HR's.
	again := record.Memory{Bucket: "bk_eng", ID: "mm_engineer", Type: "fact", Statement: "Fridays, says Ben.",
		DependsOn: []record.Dependency{{Record: "mm_deploy", Version: 1}}}
	o.ingest(t, engineer("ws_acme"), store.Batch{Memories: []record.Memory{again}})
}

func TestASourceWithNoInstallationIsStillTaken(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	src := func() *record.Source { return &record.Source{System: "notes", NativeID: "n-1"} }
	o.ingest(t, hrLead("ws_acme"), store.Batch{Events: []record.Event{{Bucket: "bk_hr", ID: "ev_n1", Type: "note", Content: said("", "x"), Source: src()}}})
	_, err := o.capture.Ingest(context.Background(), engineer("ws_acme"), store.Batch{Events: []record.Event{{Bucket: "bk_eng", ID: "ev_n2", Type: "note", Content: said("", "x"), Source: src()}}})
	if !errors.Is(err, postgres.ErrTaken) {
		t.Fatalf("a source with no installation is one source, got %v", err)
	}
}

func TestADeletedBucketHidesItsRecordsWhateverTheCallerSays(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	ctx := context.Background()
	if err := o.store.DeleteBucket(ctx, "ws_acme", "bk_hr"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.store.Memory(ctx, hrLead("ws_acme"), "mm_derived", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a memory made from a deleted bucket is read by nobody, got %v", err)
	}
	res := o.ask(t, hrLead("ws_acme"), `{"context": {"text": "promotion gateway retry"}}`)
	for _, id := range ids(res) {
		if id == "mm_promotion" || id == "ev_review" || id == "mm_derived" {
			t.Fatalf("nothing from the deleted bucket is recalled, got %v", ids(res))
		}
	}
}

func TestTheStoreRefusesAHiddenPlaceAsItRefusesAMissingOne(t *testing.T) {
	o := newOffice(t)
	o.workspace(t, "ws_acme")
	o.ingest(t, hrLead("ws_acme"), engineering())
	o.ingest(t, hrLead("ws_acme"), personnel())
	write := func(id, place string) error {
		_, err := o.store.Write(context.Background(), engineer("ws_acme"), store.Batch{Events: []record.Event{{
			Bucket: "bk_eng", ID: id, Type: "slack.message", Place: place, Content: said("", "probe"),
		}}})
		return err
	}
	hidden, nothing := write("ev_p1", "pl_hr"), write("ev_p2", "pl_nowhere")
	if hidden == nil || nothing == nil {
		t.Fatalf("both are refused, got %v and %v", hidden, nothing)
	}
	if strings.ReplaceAll(hidden.Error(), "pl_hr", "X") != strings.ReplaceAll(nothing.Error(), "pl_nowhere", "X") {
		t.Fatalf("refused alike:\n%v\n%v", hidden, nothing)
	}
}

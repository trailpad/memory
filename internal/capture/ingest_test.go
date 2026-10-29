package capture_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trailpad/memory/internal/capture"
	"github.com/trailpad/memory/internal/profile"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/postgres"
	"github.com/trailpad/memory/internal/store/postgres/pgtest"
)

func example[T any](t *testing.T, name string) T {
	t.Helper()
	raw, err := os.ReadFile("../../spec/examples/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func entity(id string, class record.Class, typ record.Type) record.Entity {
	return record.Entity{ID: id, Class: class, Type: typ, Name: id}
}

type world struct {
	capture *capture.Capture
	store   *postgres.Store
}

func setup(t *testing.T) world {
	t.Helper()
	s := pgtest.ForTest(t)
	profiles := profile.NewRegistry()
	crm, err := profile.Parse(mustRead(t, "../../spec/examples/profile.crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profiles.Add(crm); err != nil {
		t.Fatal(err)
	}
	return world{capture: &capture.Capture{Store: s, Profiles: profiles}, store: s}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// specWorld is every example in the spec, plus the records they point at.
func specWorld(t *testing.T) store.Batch {
	return store.Batch{
		Entities: []record.Entity{
			entity("pl_pr_4521", record.PlaceClass, "github.pull_request"),
			example[record.Entity](t, "entity.place.json"),
			entity("pl_payments_weekly", record.PlaceClass, "meeting"),
			entity("ac_team_payments", record.ActorClass, "team"),
			example[record.Entity](t, "entity.actor.json"),
			entity("ac_ben", record.ActorClass, "person"),
			entity("ac_review_bot", record.ActorClass, "bot"),
			entity("ac_extractor_model", record.ActorClass, "agent"),
			entity("ac_capture_agent", record.ActorClass, "agent"),
			entity("sb_repo_shop", record.SubjectClass, "code.repository"),
			example[record.Entity](t, "entity.subject.json"),
			entity("sb_vendor_gateway", record.SubjectClass, "topic"),
			{ID: "en_account_acme", Type: "crm.account", Name: "Acme"},
			example[record.Entity](t, "entity.deal.json"),
		},
		Events: []record.Event{
			example[record.Event](t, "event.review-comment.json"),
			example[record.Event](t, "event.meeting.json"),
			{ID: "ev_02K", Type: "slack.thread.message", Content: []record.Utterance{{Speaker: "ac_ben", Text: "Confirmed, five strikes and we are out."}}},
		},
		Memories: []record.Memory{
			example[record.Memory](t, "memory.retry-limit.json"),
			{ID: "mm_retry_limit_v2", Type: "constraint", Statement: "Retries stop after two attempts."},
		},
		Appraisals: []record.Appraisal{example[record.Appraisal](t, "appraisal.json")},
		Links:      []record.Link{example[record.Link](t, "link.json")},
	}
}

func TestTheSpecsWorldIsStoredAndReadBack(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	b := specWorld(t)
	if _, err := w.capture.Ingest(ctx, b); err != nil {
		t.Fatal(err)
	}

	got, err := w.store.Event(ctx, "ev_01J")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content[0].Text != b.Events[0].Content[0].Text || got.Place != "pl_pr_4521_thread_3" || len(got.Participants) != 3 {
		t.Fatalf("event read back differently: %+v", got)
	}
	m, err := w.store.Memory(ctx, "mm_retry_limit", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Statement != b.Memories[0].Statement || len(m.Evidence) != 2 {
		t.Fatalf("memory read back differently: %+v", m)
	}
	if m.Recall == nil || m.Recall.Count != 0 {
		t.Fatalf("a writer cannot set how often a memory was used, got %+v", m.Recall)
	}
	deal, err := w.store.Entity(ctx, "en_deal_884")
	if err != nil {
		t.Fatal(err)
	}
	if string(deal.Attributes["crm.stage"]) != `"negotiation"` {
		t.Fatalf("attributes read back as %s", deal.Attributes["crm.stage"])
	}

	again, err := w.capture.Ingest(ctx, specWorld(t))
	if err != nil {
		t.Fatalf("writing the same world twice must be harmless: %v", err)
	}
	if len(again.Unchanged) != len(b.Entities)+len(b.Events)+len(b.Memories)+len(b.Appraisals)+len(b.Links) {
		t.Fatalf("every record is unchanged the second time, got %d of them", len(again.Unchanged))
	}
}

func TestEventsNeverChange(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	edited := example[record.Event](t, "event.review-comment.json")
	edited.Content[0].Text = "Actually, four tries."
	_, err := w.capture.Ingest(ctx, store.Batch{Events: []record.Event{edited}})
	if !errors.Is(err, store.ErrChanged) {
		t.Fatalf("an edited event must be refused, got %v", err)
	}
}

func TestTheSameSourceIsCapturedOnce(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	// The same review comment arrives again from a second webhook, under a
	// new id, with a memory drawn from it.
	repeat := example[record.Event](t, "event.review-comment.json")
	repeat.ID = "ev_from_retry_webhook"
	memory := record.Memory{ID: "mm_new", Type: "lesson", Statement: "Check vendor limits before adding retries.",
		Evidence: []record.Evidence{{Event: "ev_from_retry_webhook", Role: record.Established}}}
	written, err := w.capture.Ingest(ctx, store.Batch{Events: []record.Event{repeat}, Memories: []record.Memory{memory}})
	if err != nil {
		t.Fatal(err)
	}
	if written.Stored["ev_from_retry_webhook"] != "ev_01J" {
		t.Fatalf("the repeat is the event already captured, got %v", written.Stored)
	}
	m, err := w.store.Memory(ctx, "mm_new", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Evidence[0].Event != "ev_01J" {
		t.Fatalf("evidence points at the stored event, got %s", m.Evidence[0].Event)
	}
	if _, err := w.store.Event(ctx, "ev_from_retry_webhook"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("no second copy is stored")
	}
}

func TestMemoriesKeepEveryVersion(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	before := time.Now()
	time.Sleep(20 * time.Millisecond)

	changed := example[record.Memory](t, "memory.retry-limit.json")
	changed.Statement = "Retries to the payment gateway stop after two attempts."
	changed.Recall = &record.Rehearsal{Count: 99}
	if _, err := w.capture.Ingest(ctx, store.Batch{Memories: []record.Memory{changed}}); err != nil {
		t.Fatal(err)
	}

	now, err := w.store.Memory(ctx, "mm_retry_limit", nil)
	if err != nil {
		t.Fatal(err)
	}
	if now.Statement != changed.Statement {
		t.Fatalf("current version, got %q", now.Statement)
	}
	if now.Recall.Count != 0 {
		t.Fatalf("a writer cannot set how often a memory was used, got %d", now.Recall.Count)
	}
	then, err := w.store.Memory(ctx, "mm_retry_limit", &before)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(then.Statement, "three attempts") {
		t.Fatalf("what the store believed before the change, got %q", then.Statement)
	}
}

func TestNothingIsStoredWhenAReferenceIsMissing(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	b := store.Batch{
		Entities: []record.Entity{entity("ac_ana", record.ActorClass, "person")},
		Events: []record.Event{{ID: "ev_1", Type: "slack.thread.message", Place: "pl_nowhere",
			Participants: []record.Participant{{Actor: "ac_ana", Role: record.Author}}}},
	}
	_, err := w.capture.Ingest(ctx, b)
	var problems record.Problems
	if !errors.As(err, &problems) || len(problems) != 1 || problems[0].Field != "events[0].place" {
		t.Fatalf("the missing place is named, got %v", err)
	}
	if _, err := w.store.Entity(ctx, "ac_ana"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("nothing from a refused batch is stored")
	}
}

func TestRecordsFollowTheirProfiles(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	b := store.Batch{Events: []record.Event{
		{ID: "ev_1", Type: "crm.call", Profiles: []string{"crm@1"},
			Attributes: record.Attributes{"crm.stage": json.RawMessage(`"maybe"`)}},
		{ID: "ev_2", Type: "care.visit", Profiles: []string{"care@1"}},
	}}
	_, err := w.capture.Ingest(ctx, b)
	var problems record.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("want problems, got %v", err)
	}
	fields := map[string]bool{}
	for _, p := range problems {
		fields[p.Field] = true
	}
	if !fields["events[0].attributes.crm.stage"] || !fields["events[1].profiles"] {
		t.Fatalf("a bad attribute and an unknown profile are both named, got %v", problems)
	}
}

func TestAHandleBelongsToOneActor(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	impostor := entity("ac_other", record.ActorClass, "person")
	impostor.Identities = []record.Identity{{Platform: "github", Handle: "anaruiz"}}
	_, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{impostor}})
	if err == nil || !strings.Contains(err.Error(), "already belongs to ac_ana") {
		t.Fatalf("joining a handle to a second actor must be refused, got %v", err)
	}
}

func TestJudgementsAndLinksAreNotRewritten(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	judged := example[record.Appraisal](t, "appraisal.json")
	v := 2
	judged.Valence = &v
	if _, err := w.capture.Ingest(ctx, store.Batch{Appraisals: []record.Appraisal{judged}}); !errors.Is(err, store.ErrChanged) {
		t.Fatalf("a changed judgement is a new appraisal, not an edit; got %v", err)
	}
	link := example[record.Link](t, "link.json")
	link.Type = "refines"
	if _, err := w.capture.Ingest(ctx, store.Batch{Links: []record.Link{link}}); !errors.Is(err, store.ErrChanged) {
		t.Fatalf("a changed link must be refused; got %v", err)
	}
}

func TestEntitiesTakeTheirNewestDescription(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	ana := example[record.Entity](t, "entity.actor.json")
	ana.Name = "Ana Ruiz Pérez"
	written, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{ana}})
	if err != nil {
		t.Fatal(err)
	}
	if len(written.Unchanged) != 0 {
		t.Fatal("a renamed entity is a change")
	}
	got, err := w.store.Entity(ctx, "ac_ana")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != ana.Name {
		t.Fatalf("name %q", got.Name)
	}
}

func TestAMemoryDidNotExistBeforeItWasRecorded(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Hour)
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.store.Memory(ctx, "mm_retry_limit", &before); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("asked as of an hour ago, got %v", err)
	}
}

func TestEvidenceAndJudgementsMustPointAtSomething(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	b := store.Batch{
		Memories: []record.Memory{{ID: "mm_1", Type: "lesson", Statement: "x",
			Evidence: []record.Evidence{{Event: "ev_never", Role: record.Established}}}},
		Appraisals: []record.Appraisal{{ID: "ap_1", Target: "mm_1", By: "ac_nobody", Intensity: ptr(2)}},
		Links:      []record.Link{{ID: "ln_1", From: "mm_1", Type: "refines", To: "mm_gone"}},
	}
	_, err := w.capture.Ingest(ctx, b)
	var problems record.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("want problems, got %v", err)
	}
	fields := map[string]bool{}
	for _, p := range problems {
		fields[p.Field] = true
	}
	for _, want := range []string{"memories[0].evidence[0].event", "appraisals[0].by", "links[0].to"} {
		if !fields[want] {
			t.Errorf("no problem on %s; got %v", want, problems)
		}
	}
	if fields["appraisals[0].target"] {
		t.Error("a target defined in the same batch resolves")
	}
}

func ptr[T any](v T) *T { return &v }

func problemFields(t *testing.T, err error) map[string]string {
	t.Helper()
	var problems record.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("want problems, got %v", err)
	}
	out := map[string]string{}
	for _, p := range problems {
		out[p.Field] = p.Message
	}
	return out
}

func TestReferencesMustPointAtTheRightKind(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	_, err := w.capture.Ingest(ctx, store.Batch{
		Events: []record.Event{{ID: "ev_x", Type: "slack.thread.message", Place: "ac_ana",
			Participants: []record.Participant{{Actor: "sb_file_retry_go", Role: record.Author}}}},
		Appraisals: []record.Appraisal{{ID: "ap_x", Target: "ev_01J", By: "mm_retry_limit", Intensity: ptr(2)}},
		Entities: []record.Entity{{ID: "sb_x", Class: record.SubjectClass, Type: "code.file", Parent: "ev_01J",
			Audience: &record.Audience{Members: []string{"ac_ghost"}}}},
	})
	fields := problemFields(t, err)
	for field, want := range map[string]string{
		"events[0].place":                 "is an actor, not a place",
		"events[0].participants[0].actor": "is a subject, not an actor",
		"appraisals[0].by":                "is a memory, not an actor",
		"entities[0].parent":              "is an event, not an entity",
		"entities[0].audience.members[0]": "not in this batch or the store",
	} {
		if !strings.Contains(fields[field], want) {
			t.Errorf("%s: want %q, got %q", field, want, fields[field])
		}
	}
}

func TestAnIdNamesOneRecord(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	fields := problemFields(t, func() error {
		_, err := w.capture.Ingest(ctx, store.Batch{Memories: []record.Memory{
			{ID: "mm_twice", Type: "fact", Statement: "a"},
			{ID: "mm_twice", Type: "fact", Statement: "b"},
		}})
		return err
	}())
	if !strings.Contains(fields["memories[1].id"], "more than once") {
		t.Fatalf("a repeated id in one batch is named, got %v", fields)
	}
	_, err := w.capture.Ingest(ctx, store.Batch{Memories: []record.Memory{{ID: "ev_01J", Type: "fact", Statement: "x"}}})
	if !errors.Is(err, postgres.ErrKind) {
		t.Fatalf("a memory cannot take an event's id, got %v", err)
	}
}

func TestDuplicatesResolveWhateverTheirOrderInTheBatch(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	team := entity("ac_team_old", record.ActorClass, "team")
	team.Source = &record.Source{System: "slack", NativeID: "S01TEAM"}
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{team}}); err != nil {
		t.Fatal(err)
	}
	member := entity("ac_member", record.ActorClass, "person")
	member.MemberOf = []string{"ac_team_new"}
	again := entity("ac_team_new", record.ActorClass, "team")
	again.Name = "Payments"
	again.Source = &record.Source{System: "slack", NativeID: "S01TEAM"}
	written, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{member, again}})
	if err != nil {
		t.Fatal(err)
	}
	if written.Stored["ac_team_new"] != "ac_team_old" {
		t.Fatalf("the team is the one already captured, got %v", written.Stored)
	}
	got, err := w.store.Entity(ctx, "ac_member")
	if err != nil {
		t.Fatal(err)
	}
	if got.MemberOf[0] != "ac_team_old" {
		t.Fatalf("membership written before the team in the batch still points at the stored team, got %v", got.MemberOf)
	}
	stored, _ := w.store.Entity(ctx, "ac_team_old")
	if stored.Name != "Payments" {
		t.Fatalf("a duplicate's newer description is kept, got %q", stored.Name)
	}
}

func TestAnEntitysSourceCanBeAddedLater(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	plain := entity("ac_a", record.ActorClass, "person")
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{plain}}); err != nil {
		t.Fatal(err)
	}
	plain.Source = &record.Source{System: "github", NativeID: "123"}
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{plain}}); err != nil {
		t.Fatal(err)
	}
	other := entity("ac_b", record.ActorClass, "person")
	other.Source = &record.Source{System: "github", NativeID: "123"}
	written, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{other}})
	if err != nil {
		t.Fatal(err)
	}
	if written.Stored["ac_b"] != "ac_a" {
		t.Fatalf("the source added later identifies the entity, got %v", written.Stored)
	}
}

func TestADroppedHandleIsReleased(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	ana := example[record.Entity](t, "entity.actor.json")
	ana.Identities = []record.Identity{{Platform: "github", Handle: "ana-r"}}
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{ana}}); err != nil {
		t.Fatal(err)
	}
	someone := entity("ac_other", record.ActorClass, "person")
	someone.Identities = []record.Identity{{Platform: "github", Handle: "anaruiz"}}
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{someone}}); err != nil {
		t.Fatalf("a handle Ana gave up can be claimed, got %v", err)
	}
}

func TestEventsCompareNumbersExactly(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	first := record.Event{ID: "ev_n", Type: "metrics.sample",
		Attributes: record.Attributes{"metrics.count": json.RawMessage(`9007199254740992`)}}
	if _, err := w.capture.Ingest(ctx, store.Batch{Events: []record.Event{first}}); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Attributes = record.Attributes{"metrics.count": json.RawMessage(`9007199254740993`)}
	if _, err := w.capture.Ingest(ctx, store.Batch{Events: []record.Event{second}}); !errors.Is(err, store.ErrChanged) {
		t.Fatalf("a different large number is a change, got %v", err)
	}
}

func TestEarlierVersionsKeepTheirTies(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	changed := example[record.Memory](t, "memory.retry-limit.json")
	changed.Participants = []record.Participant{{Actor: "ac_ben", Role: record.Decider}}
	changed.Evidence = []record.Evidence{{Event: "ev_02K", Role: record.Established}}
	if _, err := w.capture.Ingest(ctx, store.Batch{Memories: []record.Memory{changed}}); err != nil {
		t.Fatal(err)
	}
	var ties, evidence int
	pool := w.store.Pool()
	if err := pool.QueryRow(ctx, `select count(*) from involvements where record_id = 'mm_retry_limit' and version = 1 and entity_id = 'ac_ana'`).Scan(&ties); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from evidence where memory_id = 'mm_retry_limit' and version = 1 and event_id = 'ev_01J'`).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if ties != 1 || evidence != 1 {
		t.Fatalf("version 1 keeps Ana and its evidence, got %d ties and %d evidence rows", ties, evidence)
	}
}

func TestAFailedBatchLeavesNothingBehind(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	edited := example[record.Event](t, "event.review-comment.json")
	edited.Content[0].Text = "Four tries."
	_, err := w.capture.Ingest(ctx, store.Batch{
		Entities: []record.Entity{entity("ac_new", record.ActorClass, "person")},
		Events:   []record.Event{edited},
	})
	if !errors.Is(err, store.ErrChanged) {
		t.Fatalf("the edited event fails inside the store, got %v", err)
	}
	if _, err := w.store.Entity(ctx, "ac_new"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("the entity written earlier in the same batch was rolled back")
	}
}

func TestTwoWritersAtOnce(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	run := func(batches ...store.Batch) []error {
		errs := make([]error, len(batches))
		var wg sync.WaitGroup
		for i, b := range batches {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = w.capture.Ingest(ctx, b)
			}()
		}
		wg.Wait()
		return errs
	}

	t.Run("update the same memory", func(t *testing.T) {
		a := example[record.Memory](t, "memory.retry-limit.json")
		a.Statement = "Two attempts."
		b := example[record.Memory](t, "memory.retry-limit.json")
		b.Statement = "Four attempts."
		for _, err := range run(store.Batch{Memories: []record.Memory{a}}, store.Batch{Memories: []record.Memory{b}}) {
			if err != nil {
				t.Fatalf("both updates land as versions, got %v", err)
			}
		}
		var versions int
		_ = w.store.Pool().QueryRow(ctx, `select count(*) from memories where id = 'mm_retry_limit'`).Scan(&versions)
		if versions != 3 {
			t.Fatalf("want three versions, got %d", versions)
		}
	})

	t.Run("capture the same source twice", func(t *testing.T) {
		event := func(id string) store.Batch {
			return store.Batch{Events: []record.Event{{ID: id, Type: "slack.thread.message",
				Source: &record.Source{System: "slack", NativeID: "msg-77"}}}}
		}
		var errs []error
		for i := 0; i < 5; i++ {
			errs = append(errs, run(event("ev_a"+strconv.Itoa(i)), event("ev_b"+strconv.Itoa(i)))...)
		}
		for _, err := range errs {
			if err != nil {
				t.Fatalf("a repeated delivery resolves to the stored event, got %v", err)
			}
		}
		var copies int
		_ = w.store.Pool().QueryRow(ctx, `select count(*) from events where source_native_id = 'msg-77'`).Scan(&copies)
		if copies != 1 {
			t.Fatalf("want one stored copy, got %d", copies)
		}
	})
}

func TestASourceRepeatedInOneBatchIsOneRecord(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	src := &record.Source{System: "slack", NativeID: "msg-88"}
	written, err := w.capture.Ingest(ctx, store.Batch{
		Entities: []record.Entity{entity("ac_unrelated", record.ActorClass, "person")},
		Events: []record.Event{
			{ID: "ev_e1", Type: "slack.thread.message", Source: src},
			{ID: "ev_e2", Type: "slack.thread.message", Source: src},
		},
	})
	if err != nil {
		t.Fatalf("the second copy resolves to the first, got %v", err)
	}
	if written.Stored["ev_e2"] != "ev_e1" {
		t.Fatalf("want ev_e2 stored as ev_e1, got %v", written.Stored)
	}
}

func TestDeduplicationCannotMakeARecordItsOwnParent(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	a := entity("sb_a", record.SubjectClass, "code.file")
	a.Source = &record.Source{System: "github", NativeID: "file-1"}
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{a}}); err != nil {
		t.Fatal(err)
	}
	b := entity("sb_b", record.SubjectClass, "code.file")
	b.Source = a.Source
	b.Parent = "sb_a"
	_, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{b}})
	if fields := problemFields(t, err); fields["entities[0].parent"] == "" {
		t.Fatalf("want a problem on parent, got %v", fields)
	}
}

func TestAnEntityKeepsItsClass(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	if _, err := w.capture.Ingest(ctx, specWorld(t)); err != nil {
		t.Fatal(err)
	}
	ana := example[record.Entity](t, "entity.actor.json")
	ana.Class = record.SubjectClass
	ana.Type = "code.file"
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{ana}}); !errors.Is(err, postgres.ErrKind) {
		t.Fatalf("an actor cannot become a subject, got %v", err)
	}

	impostor := entity("sb_impostor", record.SubjectClass, "code.file")
	impostor.Source = &record.Source{System: "github", NativeID: "ana-source"}
	ana = example[record.Entity](t, "entity.actor.json")
	ana.Source = impostor.Source
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{ana}}); err != nil {
		t.Fatal(err)
	}
	_, err := w.capture.Ingest(ctx, store.Batch{
		Entities: []record.Entity{impostor},
		Events: []record.Event{{ID: "ev_by_ana", Type: "slack.thread.message",
			Participants: []record.Participant{{Actor: "ac_ana", Role: record.Author}}}},
	})
	if !errors.Is(err, postgres.ErrKind) {
		t.Fatalf("a subject sharing Ana's source cannot overwrite her, got %v", err)
	}
	if got, _ := w.store.Entity(ctx, "ac_ana"); got.Class != record.ActorClass {
		t.Fatalf("Ana is still an actor, got %q", got.Class)
	}
}

func TestAnAliasRacingTheStoredRecord(t *testing.T) {
	w := setup(t)
	ctx := context.Background()
	src := &record.Source{System: "github", NativeID: "user-9"}
	stored := entity("ac_canonical", record.ActorClass, "person")
	stored.Source = src
	if _, err := w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{stored}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		alias := entity("ac_alias"+strconv.Itoa(i), record.ActorClass, "person")
		alias.Source = src
		alias.Name = "alias " + strconv.Itoa(i)
		update := stored
		update.Name = "update " + strconv.Itoa(i)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for j, e := range []record.Entity{alias, update} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[j] = w.capture.Ingest(ctx, store.Batch{Entities: []record.Entity{e}})
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatalf("round %d: both writes land, got %v", i, err)
			}
		}
	}
}

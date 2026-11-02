package record_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/trailpad/memory/internal/record"
)

func TestTypeFallsBackThroughItsParents(t *testing.T) {
	got := record.Type("github.pull_request.review_comment").Lineage()
	want := []record.Type{"github.pull_request.review_comment", "github.pull_request", "github"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if p := record.Type("slack.thread.message").Platform(); p != "slack" {
		t.Fatalf("platform %q", p)
	}
}

func TestImpreciseTimeKeepsItsPrecision(t *testing.T) {
	var at record.Instant
	if err := json.Unmarshal([]byte(`{"at":"2026-03-01T00:00:00Z","precision":"month"}`), &at); err != nil {
		t.Fatal(err)
	}
	if at.Precision != record.Month {
		t.Fatalf("precision %q", at.Precision)
	}
	if want := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC); !at.Until().Equal(want) {
		t.Fatalf("a time known to the month runs until %v, got %v", want, at.Until())
	}
	written, _ := json.Marshal(at)
	if string(written) != `{"at":"2026-03-01T00:00:00Z","precision":"month"}` {
		t.Fatalf("written back as %s", written)
	}
}

func TestPreciseTimeIsWrittenAsAString(t *testing.T) {
	var at record.Instant
	if err := json.Unmarshal([]byte(`"2026-04-19T14:02:11+02:00"`), &at); err != nil {
		t.Fatal(err)
	}
	written, _ := json.Marshal(at)
	if string(written) != `"2026-04-19T14:02:11+02:00"` {
		t.Fatalf("written back as %s", written)
	}
}

func TestUnjudgedIsNotNeutral(t *testing.T) {
	var a record.Appraisal
	if err := json.Unmarshal([]byte(`{"id":"ap","target":"ev","by":"ac","intensity":3}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.Valence != nil {
		t.Fatal("a missing valence must read as unjudged, not zero")
	}
	written, _ := json.Marshal(a)
	var back map[string]any
	_ = json.Unmarshal(written, &back)
	if _, ok := back["valence"]; ok {
		t.Fatalf("an unjudged valence is written back as %v", back["valence"])
	}
}

func TestTimesThatCannotBeRead(t *testing.T) {
	for _, raw := range []string{
		`"2026-04-21T09:00:00"`,
		`"yesterday"`,
		`{"at":"2026-04-21T09:00:00Z","precision":"week"}`,
		`{"at":"21 April","precision":"day"}`,
		`42`,
	} {
		var at record.Instant
		if err := json.Unmarshal([]byte(raw), &at); err == nil {
			t.Errorf("%s was read as %v", raw, at.At)
		}
	}
}

func TestEachPrecisionSpansItsUnit(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	cases := map[record.Precision]time.Time{
		record.Second: start,
		record.Minute: start.Add(time.Minute),
		record.Day:    start.AddDate(0, 0, 1),
		record.Month:  start.AddDate(0, 1, 0),
		record.Year:   start.AddDate(1, 0, 0),
	}
	for precision, want := range cases {
		if got := (record.Instant{At: start, Precision: precision}).Until(); !got.Equal(want) {
			t.Errorf("%s: until %v, want %v", precision, got, want)
		}
	}
}

func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	bad := record.Event{Type: "Meeting", Participants: []record.Participant{{}}}
	fields := map[string]bool{}
	for _, p := range bad.Validate() {
		fields[p.Field] = true
	}
	for _, want := range []string{"id", "bucket", "type", "participants[0].actor", "participants[0].role"} {
		if !fields[want] {
			t.Errorf("no problem on %s: %v", want, fields)
		}
	}
}

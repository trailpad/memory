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

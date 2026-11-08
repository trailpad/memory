package embed

import (
	"github.com/trailpad/memory/internal/record"
	"testing"
)

func TestEventText(t *testing.T) {
	event := record.Event{Title: "Title", Content: []record.Utterance{{Text: "First"}, {Text: "Second"}}, Why: "Reason"}
	if got := EventText(event); got != "Title\nFirst\nSecond\nReason" {
		t.Fatalf("embedding text: %q", got)
	}
}

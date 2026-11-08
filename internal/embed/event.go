package embed

import (
	"strings"

	"github.com/trailpad/memory/internal/record"
)

func EventText(e record.Event) string {
	parts := []string{}
	if e.Title != "" {
		parts = append(parts, e.Title)
	}
	for _, u := range e.Content {
		parts = append(parts, u.Text)
	}
	if e.Why != "" {
		parts = append(parts, e.Why)
	}
	return strings.Join(parts, "\n")
}

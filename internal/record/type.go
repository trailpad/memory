package record

import (
	"regexp"
	"strings"
)

// Type names what a record, entity, role or attribute is. Types are
// namespaced with dots: "github.pull_request.review_comment".
type Type string

var namespaced = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)

// Valid reports whether the type is written as dot separated lowercase words.
func (t Type) Valid() bool { return namespaced.MatchString(string(t)) }

// Platform is the part before the first dot.
func (t Type) Platform() string {
	head, _, _ := strings.Cut(string(t), ".")
	return head
}

// Namespaced reports whether the type has a namespace, which every type,
// role and attribute added by a profile must have.
func (t Type) Namespaced() bool { return strings.Contains(string(t), ".") }

// Lineage is the type followed by each parent it falls back to, most specific
// first: github.pull_request.review_comment, github.pull_request, github.
func (t Type) Lineage() []Type {
	parts := strings.Split(string(t), ".")
	out := make([]Type, 0, len(parts))
	for n := len(parts); n > 0; n-- {
		out = append(out, Type(strings.Join(parts[:n], ".")))
	}
	return out
}

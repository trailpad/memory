package store

import "context"

type LinkCandidate struct {
	From, To, Approach string
	Shared, Path       []string
	Value              float64
	Frequency, Total   int
	Type               string
}

type Linker interface {
	LinkCandidates(context.Context, Access, Scope, []string, []string) ([]LinkCandidate, error)
}

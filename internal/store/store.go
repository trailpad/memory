// Package store keeps records. It knows how to write and read them, not how
// to rank them.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/trailpad/memory/internal/record"
)

var (
	// ErrNotFound is returned when no record has the id.
	ErrNotFound = errors.New("not found")
	// ErrChanged is returned when an event, appraisal or link is written
	// again with different content. Those are evidence and never change; a
	// correction is a new record.
	ErrChanged = errors.New("already recorded with different content")
)

// Batch is records written together, in one transaction. References between
// them may point either into the batch or at records already stored.
type Batch struct {
	Entities   []record.Entity    `json:"entities,omitempty"`
	Events     []record.Event     `json:"events,omitempty"`
	Memories   []record.Memory    `json:"memories,omitempty"`
	Appraisals []record.Appraisal `json:"appraisals,omitempty"`
	Links      []record.Link      `json:"links,omitempty"`
	// Lineage is each event's and memory's type followed by every type it
	// falls back to, read with the profiles the record names, by position in
	// the batch. Profile versions never change, so it is worked out once at
	// capture. Missing, the store uses the type's namespace parents.
	Lineage Lineage `json:"-"`
}

// Lineage holds type chains by position: Events[i] is for Batch.Events[i].
type Lineage struct {
	Events   [][]record.Type
	Memories [][]record.Type
}

// TypeLineage is the chain recorded for position i, or the type's namespace
// parents when none was given. Given reports which.
func TypeLineage(chains [][]record.Type, i int, t record.Type) (lineage []record.Type, given bool) {
	if i < len(chains) && len(chains[i]) > 0 {
		return chains[i], true
	}
	return t.Lineage(), false
}

// Written says what a batch did to each record.
type Written struct {
	// Stored maps each id in the batch to the id it is stored under. They
	// differ when a record was already captured from the same source under
	// another id.
	Stored map[string]string `json:"stored"`
	// Unchanged lists ids whose record was already stored as written.
	Unchanged []string `json:"unchanged,omitempty"`
}

// Store keeps records. Every method acts within one workspace and keeps to
// what the access allows.
type Store interface {
	Write(ctx context.Context, access Access, batch Batch) (Written, error)
	Entity(ctx context.Context, access Access, id string) (record.Entity, error)
	Event(ctx context.Context, access Access, id string) (record.Event, error)
	// Memory is the version current at asOf (the store's clock), or the
	// current one when asOf is nil.
	Memory(ctx context.Context, access Access, id string, asOf *time.Time) (record.Memory, error)
	// Kinds says what kind of record each stored id is. Ids nothing has, or
	// that name a record the access may not read, are left out alike. A
	// memory counts when any of its versions may be read, since a memory can
	// be made from an earlier version.
	Kinds(ctx context.Context, access Access, ids []string) (map[string]record.Kind, error)
}

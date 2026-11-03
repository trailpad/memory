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
	ErrChanged  = errors.New("already recorded with different content")
)

// Batch is records written together, in one transaction. References between
// them may point either into the batch or at records already stored.
type Batch struct {
	Entities   []record.Entity    `json:"entities,omitempty"`
	Events     []record.Event     `json:"events,omitempty"`
	Appraisals []record.Appraisal `json:"appraisals,omitempty"`
	Links      []record.Link      `json:"links,omitempty"`
	Lineage    Lineage            `json:"-"`
}

// Lineage holds type chains by position: Events[i] is for Batch.Events[i].
type Lineage struct {
	Events [][]record.Type
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

// EventQuery picks events by when they happened. From is inclusive and To
// exclusive. PartOf lists the parts of one event; TopLevel lists only events
// that are part of nothing. Buckets narrows the listing to those buckets.
// After continues a listing from the event with that id.
type EventQuery struct {
	From, To *time.Time
	Buckets  []string
	PartOf   string
	TopLevel bool
	After    string
	Limit    int
}

// Store keeps records. Every method acts within one workspace and keeps to
// what the access allows.
type Store interface {
	Write(ctx context.Context, access Access, batch Batch) (Written, error)
	Entity(ctx context.Context, access Access, id string) (record.Entity, error)
	Event(ctx context.Context, access Access, id string) (record.Event, error)
	// Events lists the events in a span by when they happened, earliest
	// first. Undated events are never listed.
	Events(ctx context.Context, access Access, q EventQuery) ([]record.Event, error)
	// Entities reads several entities at once, by id. Ids the access may not
	// read are left out, exactly as ids nothing has.
	Entities(ctx context.Context, access Access, ids []string) (map[string]record.Entity, error)
	RecordTypes(ctx context.Context, access Access, ids []string) (map[string]record.RecordType, error)
}

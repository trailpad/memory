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

// Store keeps records.
type Store interface {
	Write(ctx context.Context, batch Batch) (Written, error)
	Entity(ctx context.Context, id string) (record.Entity, error)
	Event(ctx context.Context, id string) (record.Event, error)
	// Memory is the version current at asOf (the store's clock), or the
	// current one when asOf is nil.
	Memory(ctx context.Context, id string, asOf *time.Time) (record.Memory, error)
	// Kinds says what kind of record each stored id is. Ids nothing has are
	// left out.
	Kinds(ctx context.Context, ids []string) (map[string]record.Kind, error)
}

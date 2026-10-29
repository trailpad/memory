package store

import (
	"context"
	"time"

	"github.com/trailpad/memory/internal/record"
)

// Ref names a record recall can return: a memory or an event.
type Ref struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

const (
	MemoryKind = "memory"
	EventKind  = "event"
)

// Scope is which records a recall may see. Every candidate query applies it,
// so a record outside it is never found, counted or returned.
type Scope struct {
	// AsOf answers as the store stood then, on the recorded clock. Nil is now.
	AsOf *time.Time
	// ValidAt keeps only memories true at that time. Nil keeps retired ones.
	ValidAt *time.Time
	// Kinds limits results to memories or events. Empty allows both.
	Kinds []string
	// Types keeps records whose type is one of these or falls back to one,
	// as read with the profiles each record names. Empty allows every type.
	Types []record.Type
	// OccurredFrom and OccurredTo bound when a record happened: an event's
	// occurred_at, a memory's valid_from or else when it was first recorded.
	OccurredFrom, OccurredTo *time.Time
	// UsedBy counts only uses of a memory made at or before it. Nil counts
	// all.
	UsedBy *time.Time
	// Buckets keeps records in these buckets. Empty allows every bucket the
	// access may read; it never adds one it may not.
	Buckets []string
}

// Scored is a record and how well it matched, higher is better. Scores
// compare only within one list.
type Scored struct {
	Ref   Ref
	Score float64
}

// Hop is an entity reached by walking up the parent chain, and how far.
type Hop struct {
	Entity string
	Depth  int
}

// Touch is one way a record involves a cue: the record involves Entity in
// Role, and Cue is Entity or one of its ancestors, Depth parents up.
type Touch struct {
	Ref    Ref
	Entity string
	Role   record.Role
	Cue    string
	Depth  int
}

// Found is a candidate with what recall needs to score it.
type Found struct {
	Ref    Ref
	Memory *record.Memory
	Event  *record.Event
	// Happened is when the record took place: an event's occurred_at or
	// recorded time, a memory's valid_from or first recorded time.
	Happened time.Time
}

// Recaller is what recall asks of a store. It finds and describes
// candidates; ranking them is recall's job. Nothing the access may not read
// is found, counted, walked through or returned.
type Recaller interface {
	// Text is records whose words match, best text match first.
	Text(ctx context.Context, access Access, scope Scope, text string, limit int) ([]Scored, error)
	// Ancestors is each entity and every parent above it, the entity itself
	// at depth 0.
	Ancestors(ctx context.Context, access Access, ids []string) (map[string][]Hop, error)
	// Frequency is how many records in scope involve each cue or something
	// under it, and how many records are in scope at all.
	Frequency(ctx context.Context, access Access, scope Scope, cues []string) (map[string]int, int, error)
	// Touches is how records involve the cues. With only set, it reports on
	// those records. Without, it picks up to limit records that share the
	// most distinct cues.
	Touches(ctx context.Context, access Access, scope Scope, cues []string, only []Ref, limit int) ([]Touch, error)
	// Records reads candidates as they stood in scope.
	Records(ctx context.Context, access Access, scope Scope, refs []Ref) ([]Found, error)
	// Entities reads entities by id. Ids nothing has are left out.
	Entities(ctx context.Context, access Access, ids []string) (map[string]record.Entity, error)
	// Rehearse counts a use of each current memory by the access's
	// principal.
	Rehearse(ctx context.Context, access Access, memories []string, at time.Time) error
}

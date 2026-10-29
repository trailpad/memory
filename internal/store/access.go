package store

import (
	"context"
	"errors"
)

// ErrNoAccess is returned when a request names no workspace. The store never
// guesses one.
var ErrNoAccess = errors.New("no workspace given")

// ErrBucket is returned when a record names a bucket the writer may not
// write into, or one that does not exist or was deleted. The three are not
// told apart.
var ErrBucket = errors.New("bucket cannot be written")

// ErrMove is returned when a record is written again in another bucket.
// Records never move between buckets.
var ErrMove = errors.New("records cannot move between buckets")

// Access is who a request acts as and which buckets it may use, worked out
// once when the request starts. Every store method requires it, apart from
// what a request asks for (Scope), so a filter can narrow what is read but
// never widen it. The zero value reads and writes nothing.
type Access struct {
	Workspace string
	// Principal is whose uses of a memory are counted and recorded.
	Principal string
	// Read is every bucket the principal may read. A record is readable when
	// every bucket in its requirement set is here.
	Read []string
	// Write is every bucket the principal may write records into.
	Write []string
}

// Check refuses an access that names no workspace.
func (a Access) Check() error {
	if a.Workspace == "" {
		return ErrNoAccess
	}
	return nil
}

// Readable reports whether every bucket required is one the access may read.
func (a Access) Readable(requires []string) bool {
	read := make(map[string]bool, len(a.Read))
	for _, b := range a.Read {
		read[b] = true
	}
	for _, b := range requires {
		if !read[b] {
			return false
		}
	}
	return true
}

// External is what a workspace stands for outside the engine. It describes;
// it never grants.
type External struct {
	System string `json:"system"`
	Type   string `json:"type"`
	ID     string `json:"id"`
}

// Workspace is the boundary between customers.
type Workspace struct {
	ID       string    `json:"id"`
	External *External `json:"external,omitempty"`
}

// Binding is the source container a bucket mirrors, by the source's
// permanent identifiers.
type Binding struct {
	System       string `json:"system"`
	Installation string `json:"installation"`
	Container    string `json:"container"`
}

// Bucket is where records live.
type Bucket struct {
	ID         string   `json:"id"`
	Name       string   `json:"name,omitempty"`
	Binding    *Binding `json:"binding,omitempty"`
	Quarantine bool     `json:"quarantine,omitempty"`
}

// QuarantineBucket is the id of each workspace's quarantine.
const QuarantineBucket = "quarantine"

// Spaces creates workspaces and buckets. Who may call it is decided above
// the store.
type Spaces interface {
	// CreateWorkspace creates a workspace with its quarantine bucket.
	CreateWorkspace(ctx context.Context, w Workspace) error
	CreateBucket(ctx context.Context, workspace string, b Bucket) error
	// DeleteBucket marks a bucket deleted. Its id is never reused, and
	// nothing that required it can be read again.
	DeleteBucket(ctx context.Context, workspace, id string) error
}

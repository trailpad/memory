// Package access proves who is calling and works out what they may read and
// write (SPEC §13.6). Nothing a caller states is taken on trust: the service
// is proven by its key and the person by a token an issuer signed.
package access

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"time"

	"github.com/trailpad/memory/internal/store"
)

// Actions a service credential may be given.
const (
	Read  = "read"
	Write = "write"
	Sync  = "sync"
)

var (
	// ErrUnauthenticated is any failure to prove the caller. It says nothing
	// more, so a probe learns nothing about which part failed.
	ErrUnauthenticated = errors.New("not authenticated")
	// ErrForbidden is a proven caller asking for an action its credential
	// does not allow.
	ErrForbidden = errors.New("not allowed")
	// ErrLinked is a source account already linked to another principal.
	ErrLinked = errors.New("account is linked to another principal")
)

type Service struct {
	Workspace     string
	ID            string
	Actions       []string
	Installations []Installation
}

func (s Service) Can(action string) bool { return slices.Contains(s.Actions, action) }

// Installation is a source installation a connector is registered for, and
// the namespace its members' accounts live in.
type Installation struct {
	System       string `json:"system"`
	Installation string `json:"installation"`
	Accounts     string `json:"accounts"`
}

// Installed returns the service's registration for an installation of a
// source system.
func (s Service) Installed(system, installation string) (Installation, bool) {
	for _, in := range s.Installations {
		if in.System == system && in.Installation == installation {
			return in, true
		}
	}
	return Installation{}, false
}

type Issuer struct {
	Issuer    string
	Service   string
	PublicKey ed25519.PublicKey
	Attests   []string
}

// Account is a user at a source, keyed by the source's permanent ids.
type Account struct {
	System       string `json:"system"`
	Installation string `json:"installation"`
	Account      string `json:"account"`
}

// Directory is where services, issuers, principals and grants are kept.
type Directory interface {
	ServiceByKey(ctx context.Context, hash []byte) (Service, error)
	Issuer(ctx context.Context, workspace, issuer string) (Issuer, error)
	TokenTTL(ctx context.Context, workspace string) (time.Duration, error)
	// Person returns the principal for a subject at an issuer, creating it on
	// first sign-in, and links the accounts the issuer proved.
	Person(ctx context.Context, workspace, issuer, sub string, accounts []Account) (string, error)
	// Grants returns the live buckets a principal may read and write.
	Grants(ctx context.Context, workspace, principal string, at time.Time) (read, write []string, err error)
}

// Caller is a proven request: the service, whom it acts for, and the access
// worked out once for the whole request.
type Caller struct {
	Service Service
	Person  bool
	Access  store.Access
}

type Authority struct {
	Directory Directory
	// Audience is this engine's name, which every person token must carry.
	Audience string
	Now      func() time.Time
}

func (a *Authority) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Authenticate proves the service from its key and, when a token is given,
// the person it acts for. Without a token the service acts as itself.
func (a *Authority) Authenticate(ctx context.Context, key, token string) (Caller, error) {
	if key == "" {
		return Caller{}, ErrUnauthenticated
	}
	service, err := a.Directory.ServiceByKey(ctx, HashKey(key))
	if errors.Is(err, store.ErrNotFound) {
		return Caller{}, ErrUnauthenticated
	}
	if err != nil {
		return Caller{}, err
	}
	caller := Caller{Service: service}
	principal := service.ID
	if token != "" {
		claims, err := a.verify(ctx, service, token)
		if err != nil {
			return Caller{}, err
		}
		if principal, err = a.Directory.Person(ctx, service.Workspace, claims.Issuer, claims.Subject, claims.Accounts); err != nil {
			return Caller{}, err
		}
		caller.Person = true
	}
	read, write, err := a.Directory.Grants(ctx, service.Workspace, principal, a.now())
	if err != nil {
		return Caller{}, err
	}
	if !service.Can(Read) {
		read = nil
	}
	if !service.Can(Write) {
		write = nil
	}
	caller.Access = store.Access{Workspace: service.Workspace, Principal: principal, Read: read, Write: write}
	return caller, nil
}

// NewKey makes a service key. Only its hash is stored.
func NewKey() (key string, hash []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	key = "mk_" + base64.RawURLEncoding.EncodeToString(b)
	return key, HashKey(key)
}

func HashKey(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

// Package httpapi serves the engine as JSON over HTTP. Every request is
// authenticated first, and its access is worked out once and used for the
// whole request.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trailpad/memory/internal/access"
	"github.com/trailpad/memory/internal/capture"
	"github.com/trailpad/memory/internal/recall"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
	"github.com/trailpad/memory/internal/store/postgres"
)

// PersonHeader carries the token of the person a service acts for.
const PersonHeader = "Memory-Person"

// MaxBody is the largest request body accepted.
const MaxBody = 8 << 20

// Connectors is what a connector changes: buckets bound to its sources and
// who reads them.
type Connectors interface {
	BindBucket(ctx context.Context, svc access.Service, b store.Bucket) (string, error)
	SyncReaders(ctx context.Context, svc access.Service, bucket string, accounts []access.Account, at time.Time) error
}

type Server struct {
	Auth       *access.Authority
	Capture    *capture.Capture
	Recall     *recall.Service
	Store      store.Store
	Connectors Connectors
	Log        *slog.Logger
	Now        func() time.Time
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.Handle("POST /v1/records", s.as(access.Write, s.write))
	mux.Handle("GET /v1/entities/{id}", s.as(access.Read, s.entity))
	mux.Handle("GET /v1/events", s.as(access.Read, s.events))
	mux.Handle("GET /v1/events/{id}", s.as(access.Read, s.event))
	mux.Handle("GET /v1/memories/{id}", s.as(access.Read, s.memory))
	mux.Handle("POST /v1/recall", s.as(access.Read, s.recall))
	mux.Handle("POST /v1/memories/use", s.as(access.Read, s.use))
	mux.Handle("PUT /v1/buckets", s.as(access.Sync, s.bindBucket))
	mux.Handle("PUT /v1/buckets/{id}/readers", s.as(access.Sync, s.syncReaders))
	return mux
}

type handler func(w http.ResponseWriter, r *http.Request, c access.Caller) error

// as authenticates the request and refuses it unless the service's
// credential allows the action.
func (s *Server) as(action string, h handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		caller, err := s.Auth.Authenticate(r.Context(), strings.TrimSpace(key), r.Header.Get(PersonHeader))
		if err == nil && !caller.Service.Can(action) {
			err = access.ErrForbidden
		}
		if err == nil {
			r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
			err = h(w, r, caller)
		}
		if err != nil {
			s.fail(w, r, err)
		}
	})
}

func (s *Server) write(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var b store.Batch
	if err := decode(r, &b); err != nil {
		return err
	}
	written, err := s.Capture.Ingest(r.Context(), c.Access, b)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, written)
}

func (s *Server) entity(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.Store.Entity(r.Context(), c.Access, r.PathValue("id"))
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, e)
}

func (s *Server) event(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.Store.Event(r.Context(), c.Access, r.PathValue("id"))
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, e)
}

// Event listings are paged: a full page names the event to continue after.
const (
	DefaultEventPage = 200
	MaxEventPage     = 1000
)

func (s *Server) events(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	query := r.URL.Query()
	q := store.EventQuery{PartOf: query.Get("part_of"), After: query.Get("after"), Buckets: query["bucket"], Limit: DefaultEventPage}
	var problems record.Problems
	at := func(field string) *time.Time {
		v := query.Get(field)
		if v == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			problems = append(problems, record.Problem{Field: field, Message: "must be an RFC 3339 time"})
			return nil
		}
		return &t
	}
	q.From, q.To = at("from"), at("to")
	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		problems = append(problems, record.Problem{Field: "to", Message: "must be after from"})
	}
	switch query.Get("top_level") {
	case "", "false":
	case "true":
		q.TopLevel = true
	default:
		problems = append(problems, record.Problem{Field: "top_level", Message: "must be true or false"})
	}
	if q.TopLevel && q.PartOf != "" {
		problems = append(problems, record.Problem{Field: "part_of", Message: "cannot be combined with top_level"})
	}
	if v := query.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxEventPage {
			problems = append(problems, record.Problem{Field: "limit", Message: fmt.Sprintf("must be from 1 to %d", MaxEventPage)})
		}
		q.Limit = n
	}
	if len(problems) > 0 {
		return problems
	}
	events, err := s.Store.Events(r.Context(), c.Access, q)
	if err != nil {
		return err
	}
	entities, err := s.involved(r.Context(), c, events)
	if err != nil {
		return err
	}
	page := struct {
		Events   []record.Event  `json:"events"`
		Entities []record.Entity `json:"entities"`
		Next     string          `json:"next,omitempty"`
	}{Events: events, Entities: entities}
	if page.Events == nil {
		page.Events = []record.Event{}
	}
	if len(events) == q.Limit {
		page.Next = events[len(events)-1].ID
	}
	return reply(w, http.StatusOK, page)
}

// involved is every entity the events name, and the places those sit in, so
// a listing can be shown without a read per face and per place.
func (s *Server) involved(ctx context.Context, c access.Caller, events []record.Event) ([]record.Entity, error) {
	wanted := map[string]bool{}
	for i := range events {
		for _, ref := range events[i].Refs() {
			switch ref.Target {
			case record.TargetEntity, record.TargetPlace, record.TargetActor, record.TargetSubject:
				wanted[*ref.ID] = true
			}
		}
	}
	var out []record.Entity
	for range 2 {
		ids := make([]string, 0, len(wanted))
		for id, pending := range wanted {
			if pending {
				ids = append(ids, id)
				wanted[id] = false
			}
		}
		if len(ids) == 0 {
			break
		}
		found, err := s.Store.Entities(ctx, c.Access, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range slices.Sorted(maps.Keys(found)) {
			e := found[id]
			out = append(out, e)
			if _, seen := wanted[e.Parent]; e.Parent != "" && !seen {
				wanted[e.Parent] = true
			}
		}
	}
	if out == nil {
		out = []record.Entity{}
	}
	return out, nil
}

func (s *Server) memory(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var asOf *time.Time
	if v := r.URL.Query().Get("as_of"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return record.Problems{{Field: "as_of", Message: "must be an RFC 3339 time"}}
		}
		asOf = &t
	}
	m, err := s.Store.Memory(r.Context(), c.Access, r.PathValue("id"), asOf)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, m)
}

func (s *Server) recall(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var req recall.Request
	if err := decode(r, &req); err != nil {
		return err
	}
	res, err := s.Recall.Recall(r.Context(), c.Access, req)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, res)
}

func (s *Server) use(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var body struct {
		Memories []string `json:"memories"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if err := s.Recall.Use(r.Context(), c.Access, body.Memories); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) bindBucket(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var b store.Bucket
	if err := decode(r, &b); err != nil {
		return err
	}
	if b.Binding == nil {
		return record.Problems{{Field: "binding", Message: "is required"}}
	}
	id, err := s.Connectors.BindBucket(r.Context(), c.Service, b)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, map[string]string{"id": id})
}

func (s *Server) syncReaders(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var body struct {
		Readers []access.Account `json:"readers"`
	}
	if err := decode(r, &body); err != nil {
		return err
	}
	if err := s.Connectors.SyncReaders(r.Context(), c.Service, r.PathValue("id"), body.Readers, s.now()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return errTooLarge
		}
		return record.Problems{{Field: "body", Message: "is not valid JSON: " + err.Error()}}
	}
	return nil
}

var errTooLarge = errors.New("request body is too large")

func reply(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

type failure struct {
	Error    string          `json:"error"`
	Problems record.Problems `json:"problems,omitempty"`
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var problems record.Problems
	var problem record.Problem
	switch {
	case errors.As(err, &problems):
		_ = reply(w, http.StatusUnprocessableEntity, failure{Error: "invalid", Problems: problems})
	case errors.As(err, &problem):
		_ = reply(w, http.StatusUnprocessableEntity, failure{Error: "invalid", Problems: record.Problems{problem}})
	case errors.Is(err, access.ErrUnauthenticated):
		_ = reply(w, http.StatusUnauthorized, failure{Error: err.Error()})
	case errors.Is(err, access.ErrForbidden):
		_ = reply(w, http.StatusForbidden, failure{Error: err.Error()})
	case errors.Is(err, store.ErrNotFound):
		_ = reply(w, http.StatusNotFound, failure{Error: err.Error()})
	case errors.Is(err, store.ErrBucket):
		_ = reply(w, http.StatusForbidden, failure{Error: err.Error()})
	case errors.Is(err, store.ErrChanged), errors.Is(err, store.ErrMove),
		errors.Is(err, postgres.ErrTaken), errors.Is(err, postgres.ErrKind):
		_ = reply(w, http.StatusConflict, failure{Error: err.Error()})
	case errors.Is(err, errTooLarge):
		_ = reply(w, http.StatusRequestEntityTooLarge, failure{Error: err.Error()})
	default:
		s.Log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		_ = reply(w, http.StatusInternalServerError, failure{Error: "internal error"})
	}
}

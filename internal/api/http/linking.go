package httpapi

import (
	"net/http"
	"time"

	"github.com/trailpad/memory/internal/access"
	"github.com/trailpad/memory/internal/recall"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

func (s *Server) eventLinks(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var req struct {
		Buckets []string   `json:"buckets"`
		From    *time.Time `json:"from"`
		To      *time.Time `json:"to"`
		recall.LinkOptions
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	p := req.LinkOptions.Validate()
	if req.From == nil {
		p = append(p, record.Problem{Field: "from", Message: "is required"})
	}
	if req.To == nil {
		p = append(p, record.Problem{Field: "to", Message: "is required"})
	}
	if req.From != nil && req.To != nil && !req.From.Before(*req.To) {
		p = append(p, record.Problem{Field: "to", Message: "must be after from"})
	}
	if len(p) > 0 {
		return p
	}
	ctx := store.WithLinkTruncation(r.Context())
	r = r.WithContext(ctx)
	limit := req.AnchorsLimit
	if limit == 0 {
		limit = 200
	}
	q := store.EventQuery{From: req.From, To: req.To, Buckets: req.Buckets, Limit: limit + 1, After: req.After}
	events, err := s.Store.Events(r.Context(), c.Access, q)
	if err != nil {
		return err
	}
	next := ""
	if len(events) > limit {
		events = events[:limit]
		next = events[len(events)-1].ID
	}

	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	links, err := s.Recall.Links(r.Context(), c.Access, store.Scope{Buckets: req.Buckets, OccurredFrom: req.From, OccurredTo: req.To}, ids, req.LinkOptions, true)
	if err != nil {
		return err
	}
	refs := []store.Ref{}
	used := map[string]bool{}
	for _, l := range links {
		for _, id := range []string{l.From, l.To} {
			if !used[id] {
				used[id] = true
				refs = append(refs, store.Ref{ID: id})
			}
		}
	}
	found, err := s.Recall.Store.Records(r.Context(), c.Access, store.Scope{Buckets: req.Buckets, OccurredFrom: req.From, OccurredTo: req.To}, refs)
	if err != nil {
		return err
	}
	events = []record.Event{}
	for _, f := range found {
		if f.Event != nil {
			events = append(events, *f.Event)
		}
	}
	entities, err := s.involved(r.Context(), c, events)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, struct {
		Events    []record.Event     `json:"events"`
		Entities  []record.Entity    `json:"entities"`
		Links     []recall.EventLink `json:"links"`
		Next      string             `json:"next,omitempty"`
		Truncated []string           `json:"truncated,omitempty"`
	}{events, entities, links, next, store.LinkTruncation(r.Context())})
}

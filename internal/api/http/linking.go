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
	events := []record.Event{}
	q := store.EventQuery{From: req.From, To: req.To, Buckets: req.Buckets, Limit: MaxEventPage}
	for {
		page, err := s.Store.Events(r.Context(), c.Access, q)
		if err != nil {
			return err
		}
		events = append(events, page...)
		if len(page) < q.Limit {
			break
		}
		q.After = page[len(page)-1].ID
	}
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	links, err := s.Recall.Links(r.Context(), c.Access, store.Scope{Buckets: req.Buckets, OccurredFrom: req.From, OccurredTo: req.To}, ids, req.LinkOptions, true)
	if err != nil {
		return err
	}
	entities, err := s.involved(r.Context(), c, events)
	if err != nil {
		return err
	}
	return reply(w, http.StatusOK, struct {
		Events   []record.Event     `json:"events"`
		Entities []record.Entity    `json:"entities"`
		Links    []recall.EventLink `json:"links"`
	}{events, entities, links})
}

// Package capture is the write path: it checks records against the spec and
// their profiles, makes sure every reference resolves, then stores them.
package capture

import (
	"context"
	"fmt"

	"github.com/trailpad/memory/internal/profile"
	"github.com/trailpad/memory/internal/record"
	"github.com/trailpad/memory/internal/store"
)

type Capture struct {
	Store    store.Store
	Profiles *profile.Registry
}

// Ingest stores a batch, or returns record.Problems naming everything wrong
// with it and stores nothing. A reference to a record the access may not
// read is reported exactly as one to a record that does not exist.
func (c *Capture) Ingest(ctx context.Context, access store.Access, b store.Batch) (store.Written, error) {
	if err := access.Check(); err != nil {
		return store.Written{}, err
	}
	var problems record.Problems
	add := func(prefix string, found record.Problems) {
		for _, p := range found {
			problems = append(problems, record.Problem{Field: prefix + "." + p.Field, Message: p.Message})
		}
	}
	resolver := func(prefix string, refs []string) *profile.Resolver {
		r, err := c.Profiles.For(refs)
		if err != nil {
			add(prefix, record.Problems{{Field: "profiles", Message: err.Error()}})
			return nil
		}
		return r
	}

	defined := map[string]record.Kind{}
	define := func(prefix, id string, kind record.Kind) {
		if _, twice := defined[id]; twice && id != "" {
			add(prefix, record.Problems{{Field: "id", Message: fmt.Sprintf("%s appears more than once in this batch", id)}})
		}
		defined[id] = kind
	}
	type holder struct {
		prefix string
		refs   []record.Ref
	}
	var holders []holder

	for i := range b.Entities {
		e := &b.Entities[i]
		at := fmt.Sprintf("entities[%d]", i)
		add(at, e.Validate())
		if r := resolver(at, e.Profiles); r != nil {
			add(at, r.CheckAttributes("attributes", e.Attributes))
		}
		define(at, e.ID, record.Kind{Record: "entity", Class: e.Class})
		holders = append(holders, holder{at, e.Refs()})
	}
	for i := range b.Events {
		e := &b.Events[i]
		at := fmt.Sprintf("events[%d]", i)
		add(at, e.Validate())
		lineage := e.Type.Lineage()
		if r := resolver(at, e.Profiles); r != nil {
			add(at, r.CheckAttributes("attributes", e.Attributes))
			lineage = r.EventLineage(e.Type)
		}
		b.Lineage.Events = append(b.Lineage.Events, lineage)
		define(at, e.ID, record.Kind{Record: "event"})
		holders = append(holders, holder{at, e.Refs()})
	}
	for i := range b.Memories {
		m := &b.Memories[i]
		at := fmt.Sprintf("memories[%d]", i)
		add(at, m.Validate())
		lineage := m.Type.Lineage()
		if r := resolver(at, m.Profiles); r != nil {
			add(at, r.CheckAttributes("attributes", m.Attributes))
			lineage = r.MemoryLineage(m.Type)
		}
		b.Lineage.Memories = append(b.Lineage.Memories, lineage)
		define(at, m.ID, record.Kind{Record: "memory"})
		holders = append(holders, holder{at, m.Refs()})
	}
	for i := range b.Appraisals {
		a := &b.Appraisals[i]
		at := fmt.Sprintf("appraisals[%d]", i)
		add(at, a.Validate())
		if r := resolver(at, a.Profiles); r != nil {
			add(at, r.CheckDimensions("dimensions", a.Dimensions))
		}
		define(at, a.ID, record.Kind{Record: "appraisal"})
		holders = append(holders, holder{at, a.Refs()})
	}
	for i := range b.Links {
		l := &b.Links[i]
		at := fmt.Sprintf("links[%d]", i)
		add(at, l.Validate())
		resolver(at, l.Profiles)
		define(at, l.ID, record.Kind{Record: "link"})
		holders = append(holders, holder{at, l.Refs()})
	}
	if len(problems) > 0 {
		return store.Written{}, problems
	}

	var outside []string
	for _, h := range holders {
		for _, r := range h.refs {
			if _, ok := defined[*r.ID]; !ok {
				outside = append(outside, *r.ID)
			}
		}
	}
	stored, err := c.Store.Kinds(ctx, access, outside)
	if err != nil {
		return store.Written{}, err
	}
	for _, h := range holders {
		for _, r := range h.refs {
			kind, ok := defined[*r.ID]
			if !ok {
				kind, ok = stored[*r.ID]
			}
			field := h.prefix + "." + r.Field
			switch {
			case !ok && !r.MayDangle:
				problems = append(problems, record.Problem{Field: field, Message: fmt.Sprintf("%s is not in this batch or the store", *r.ID)})
			case ok && !r.Target.Accepts(kind):
				problems = append(problems, record.Problem{Field: field, Message: fmt.Sprintf("%s is %s, not %s", *r.ID, describe(kind), article(string(r.Target)))})
			}
		}
	}
	if len(problems) > 0 {
		return store.Written{}, problems
	}
	return c.Store.Write(ctx, access, b)
}

func describe(k record.Kind) string {
	if k.Record == "entity" && k.Class != "" {
		return article(string(k.Class))
	}
	return article(k.Record)
}

func article(word string) string {
	switch word[0] {
	case 'a', 'e', 'i', 'o', 'u':
		return "an " + word
	}
	return "a " + word
}

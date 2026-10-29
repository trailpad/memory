package record

import (
	"fmt"
	"strings"
)

// Problem is one rule a record breaks.
type Problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (p Problem) Error() string { return p.Field + ": " + p.Message }

// Problems is every rule a record breaks, so a writer can fix them in one go.
type Problems []Problem

func (ps Problems) Error() string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Error()
	}
	return strings.Join(out, "; ")
}

// Err is nil when there are no problems.
func (ps Problems) Err() error {
	if len(ps) == 0 {
		return nil
	}
	return ps
}

type check struct{ found Problems }

func (c *check) add(field, format string, args ...any) {
	c.found = append(c.found, Problem{Field: field, Message: fmt.Sprintf(format, args...)})
}

func (c *check) id(field, value string) {
	if strings.TrimSpace(value) == "" {
		c.add(field, "is required")
	}
}

func (c *check) typ(field string, value Type) {
	if value == "" {
		c.add(field, "is required")
	} else if !value.Valid() {
		c.add(field, "%q is not dot separated lowercase words", value)
	}
}

func (c *check) scale(field string, value *int, low, high int) {
	if value != nil && (*value < low || *value > high) {
		c.add(field, "%d is outside %d to %d", *value, low, high)
	}
}

func (c *check) unit(field string, value *float64) {
	if value != nil && (*value < 0 || *value > 1) {
		c.add(field, "%v is outside 0 to 1", *value)
	}
}

func (c *check) order(field string, from, until *Instant) {
	if from != nil && until != nil && until.At.Before(from.At) {
		c.add(field, "is before %s", from.At.Format("2006-01-02T15:04:05Z07:00"))
	}
}

func (c *check) attributes(field string, values Attributes) {
	for key := range values {
		if !key.Valid() || !key.Namespaced() {
			c.add(field+"."+string(key), "attribute keys are namespaced, like crm.amount")
		}
	}
}

func (c *check) participants(field string, values []Participant) {
	for i, p := range values {
		at := fmt.Sprintf("%s[%d]", field, i)
		c.id(at+".actor", p.Actor)
		c.typ(at+".role", p.Role)
	}
}

func (c *check) subjects(field string, values []SubjectRef) {
	for i, s := range values {
		at := fmt.Sprintf("%s[%d]", field, i)
		c.id(at+".subject", s.Subject)
		c.typ(at+".role", s.Role)
	}
}

func (c *check) involves(field string, values []Involvement) {
	for i, v := range values {
		at := fmt.Sprintf("%s[%d]", field, i)
		c.id(at+".entity", v.Entity)
		c.typ(at+".role", v.Role)
	}
}

// Validate checks the rules the spec states for an event.
func (e Event) Validate() Problems {
	var c check
	c.id("id", e.ID)
	c.typ("type", e.Type)
	c.order("ended_at", e.OccurredAt, e.EndedAt)
	c.participants("participants", e.Participants)
	c.subjects("subjects", e.Subjects)
	c.involves("involves", e.Involves)
	for i, u := range e.Content {
		if strings.TrimSpace(u.Text) == "" {
			c.add(fmt.Sprintf("content[%d].text", i), "is required")
		}
	}
	if e.PartOf != "" && e.PartOf == e.ID {
		c.add("part_of", "an event cannot be part of itself")
	}
	if e.InReplyTo != "" && e.InReplyTo == e.ID {
		c.add("in_reply_to", "an event cannot reply to itself")
	}
	c.attributes("attributes", e.Attributes)
	return c.found
}

// Validate checks the rules the spec states for a memory.
func (m Memory) Validate() Problems {
	var c check
	c.id("id", m.ID)
	c.typ("type", m.Type)
	if strings.TrimSpace(m.Statement) == "" {
		c.add("statement", "is required")
	}
	c.order("valid_until", m.ValidFrom, m.ValidUntil)
	c.unit("confidence", m.Confidence)
	for i, ev := range m.Evidence {
		at := fmt.Sprintf("evidence[%d]", i)
		c.id(at+".event", ev.Event)
		if !ev.Role.valid() {
			c.add(at+".role", "%q is not one of established, reinforced, refined, contradicted, retired", ev.Role)
		}
	}
	c.participants("participants", m.Participants)
	c.subjects("subjects", m.Subjects)
	c.involves("involves", m.Involves)
	if m.Recall != nil && m.Recall.Count < 0 {
		c.add("recall.count", "cannot be negative")
	}
	c.attributes("attributes", m.Attributes)
	return c.found
}

// Validate checks the rules the spec states for an entity.
func (e Entity) Validate() Problems {
	var c check
	c.id("id", e.ID)
	c.typ("type", e.Type)
	switch e.Class {
	case "", PlaceClass, ActorClass, SubjectClass:
	default:
		c.add("class", "%q is not place, actor or subject; leave it out for a kind a profile declares", e.Class)
	}
	if e.Class == "" && !e.Type.Namespaced() {
		c.add("type", "a kind declared by a profile has a namespaced type, like crm.deal")
	}
	if e.Parent != "" && e.Parent == e.ID {
		c.add("parent", "an entity cannot be its own parent")
	}
	for i, id := range e.Identities {
		at := fmt.Sprintf("identities[%d]", i)
		c.id(at+".platform", id.Platform)
		c.id(at+".handle", id.Handle)
	}
	c.attributes("attributes", e.Attributes)
	return c.found
}

// Validate checks the rules the spec states for an appraisal.
func (a Appraisal) Validate() Problems {
	var c check
	c.id("id", a.ID)
	c.id("target", a.Target)
	c.id("by", a.By)
	c.scale("valence", a.Valence, -2, 2)
	c.scale("intensity", a.Intensity, 1, 5)
	c.scale("importance", a.Importance, 1, 5)
	c.scale("surprise", a.Surprise, 1, 5)
	c.unit("confidence", a.Confidence)
	for key := range a.Dimensions {
		if !key.Valid() || !key.Namespaced() {
			c.add("dimensions."+string(key), "dimensions a profile adds are namespaced, like crm.risk")
		}
	}
	if a.Valence == nil && a.Intensity == nil && a.Importance == nil && a.Surprise == nil && len(a.Dimensions) == 0 {
		c.add("valence", "an appraisal judges at least one thing")
	}
	return c.found
}

// Validate checks the rules the spec states for a link.
func (l Link) Validate() Problems {
	var c check
	c.id("id", l.ID)
	c.id("from", l.From)
	c.id("to", l.To)
	c.typ("type", l.Type)
	if l.From != "" && l.From == l.To {
		c.add("to", "a record cannot link to itself")
	}
	return c.found
}

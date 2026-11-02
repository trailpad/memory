package record

import "fmt"

// Target is the kind of record a reference must point at.
type Target string

const (
	TargetEntity    Target = "entity"
	TargetPlace     Target = "place"
	TargetActor     Target = "actor"
	TargetSubject   Target = "subject"
	TargetEvent     Target = "event"
	TargetAppraisal Target = "appraisal"
	TargetRecord    Target = "record"
)

// Ref is one field that holds the id of another record.
type Ref struct {
	Field  string
	ID     *string
	Target Target
	// MayDangle is set where the spec lets the target arrive later: a message
	// before its thread, a record before the judgements made of it.
	MayDangle bool
}

type refs []Ref

func (r *refs) add(field string, id *string, target Target) {
	if *id != "" {
		*r = append(*r, Ref{Field: field, ID: id, Target: target})
	}
}

func (r *refs) later(field string, id *string, target Target) {
	if *id != "" {
		*r = append(*r, Ref{Field: field, ID: id, Target: target, MayDangle: true})
	}
}

func (r *refs) source(s *Source) {
	if s != nil {
		r.add("source.by", &s.By, TargetActor)
	}
}

func (r *refs) roles(participants []Participant, subjects []SubjectRef, involves []Involvement) {
	for i := range participants {
		r.add(fmt.Sprintf("participants[%d].actor", i), &participants[i].Actor, TargetActor)
	}
	for i := range subjects {
		r.add(fmt.Sprintf("subjects[%d].subject", i), &subjects[i].Subject, TargetSubject)
	}
	for i := range involves {
		r.add(fmt.Sprintf("involves[%d].entity", i), &involves[i].Entity, TargetEntity)
	}
}

// Refs is every reference an entity holds, pointing into the entity so a
// writer can rewrite them in place.
func (e *Entity) Refs() []Ref {
	var r refs
	r.add("parent", &e.Parent, TargetEntity)
	for i := range e.MemberOf {
		r.add(fmt.Sprintf("member_of[%d]", i), &e.MemberOf[i], TargetActor)
	}
	if e.Audience != nil {
		for i := range e.Audience.Members {
			r.add(fmt.Sprintf("audience.members[%d]", i), &e.Audience.Members[i], TargetActor)
		}
	}
	r.source(e.Source)
	return r
}

// Refs is every reference an event holds.
func (e *Event) Refs() []Ref {
	var r refs
	r.add("place", &e.Place, TargetPlace)
	r.roles(e.Participants, e.Subjects, e.Involves)
	for i := range e.Content {
		r.add(fmt.Sprintf("content[%d].speaker", i), &e.Content[i].Speaker, TargetActor)
	}
	r.later("part_of", &e.PartOf, TargetEvent)
	r.later("in_reply_to", &e.InReplyTo, TargetEvent)
	r.source(e.Source)
	return r
}

// Refs is every reference an appraisal holds.
func (a *Appraisal) Refs() []Ref {
	var r refs
	r.add("target", &a.Target, TargetRecord)
	r.add("by", &a.By, TargetActor)
	r.source(a.Source)
	return r
}

// Refs is every reference a link holds.
func (l *Link) Refs() []Ref {
	var r refs
	r.add("from", &l.From, TargetRecord)
	r.add("to", &l.To, TargetRecord)
	r.source(l.Source)
	return r
}

// Kind is what a stored or batched record is, as far as references care.
type Kind struct {
	Record string // entity, event, appraisal, link
	Class  Class  // for entities
}

// Accepts reports whether a reference of this target may point at a record
// of this kind.
func (t Target) Accepts(k Kind) bool {
	switch t {
	case TargetRecord:
		return true
	case TargetEntity:
		return k.Record == "entity"
	case TargetPlace:
		return k.Record == "entity" && k.Class == PlaceClass
	case TargetActor:
		return k.Record == "entity" && k.Class == ActorClass
	case TargetSubject:
		return k.Record == "entity" && k.Class == SubjectClass
	case TargetEvent:
		return k.Record == "event"
	case TargetAppraisal:
		return k.Record == "appraisal"
	}
	return false
}

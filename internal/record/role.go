package record

// Role is the part an actor or entity plays in an event or memory.
type Role = Type

const (
	Author    Role = "author"
	Speaker   Role = "speaker"
	Addressee Role = "addressee"
	Decider   Role = "decider"
	Approver  Role = "approver"
	Reviewer  Role = "reviewer"
	Assignee  Role = "assignee"
	Present   Role = "present"
	Mentioned Role = "mentioned"
	Affected  Role = "affected"
	Observer  Role = "observer"
	About     Role = "about"
)

// EvidenceRole is what an event did for a memory.
type EvidenceRole string

const (
	Established  EvidenceRole = "established"
	Reinforced   EvidenceRole = "reinforced"
	Refined      EvidenceRole = "refined"
	Contradicted EvidenceRole = "contradicted"
	Retired      EvidenceRole = "retired"
)

func (r EvidenceRole) valid() bool {
	switch r {
	case Established, Reinforced, Refined, Contradicted, Retired:
		return true
	}
	return false
}

package record

// Source says where a record itself came from.
type Source struct {
	System string `json:"system,omitempty"`
	// Installation is which account or install of the system the record
	// came from. The same native id in two installations is two records.
	Installation string   `json:"installation,omitempty"`
	NativeID     string   `json:"native_id,omitempty"`
	URL          string   `json:"url,omitempty"`
	RecordedAt   *Instant `json:"recorded_at,omitempty"`
	Method       string   `json:"method,omitempty"`
	By           string   `json:"by,omitempty"`
}

// Class is which of the spec's named classes an entity is. An entity of a type
// declared by a profile has none.
type Class string

const (
	PlaceClass   Class = "place"
	ActorClass   Class = "actor"
	SubjectClass Class = "subject"
)

// Audience is who could see what happened in a place.
type Audience struct {
	Visibility string   `json:"visibility,omitempty"`
	Members    []string `json:"members,omitempty"`
}

// Medium is how a place carries what happens in it.
type Medium struct {
	Sync      *bool  `json:"sync,omitempty"`
	Formality string `json:"formality,omitempty"`
	Record    string `json:"record,omitempty"`
}

// Identity is an actor's handle on one platform.
type Identity struct {
	Platform string `json:"platform"`
	Handle   string `json:"handle"`
}

// Entity is anything that exists apart from a single event: a place, an
// actor, a subject, or a type a profile declares. The place and actor fields
// are optional on any entity.
type Entity struct {
	Bucket     string     `json:"bucket"`
	ID         string     `json:"id"`
	Class      Class      `json:"class,omitempty"`
	Type       Type       `json:"type"`
	Name       string     `json:"name,omitempty"`
	Parent     string     `json:"parent,omitempty"`
	URL        string     `json:"url,omitempty"`
	Audience   *Audience  `json:"audience,omitempty"`
	Medium     *Medium    `json:"medium,omitempty"`
	Image      string     `json:"image,omitempty"`
	Identities []Identity `json:"identities,omitempty"`
	MemberOf   []string   `json:"member_of,omitempty"`
	Attributes Attributes `json:"attributes,omitempty"`
	Profiles   []string   `json:"profiles,omitempty"`
	Source     *Source    `json:"source,omitempty"`
}

type Participant struct {
	Actor string `json:"actor"`
	Role  Role   `json:"role"`
}

type SubjectRef struct {
	Subject string `json:"subject"`
	Role    Role   `json:"role"`
}

type Involvement struct {
	Entity string `json:"entity"`
	Role   Role   `json:"role"`
}

// Utterance is one thing said or done in an event, verbatim.
type Utterance struct {
	Speaker string   `json:"speaker,omitempty"`
	Text    string   `json:"text"`
	At      *Instant `json:"at,omitempty"`
}

type Event struct {
	Title        string        `json:"title,omitempty"`
	Bucket       string        `json:"bucket"`
	ID           string        `json:"id"`
	Type         Type          `json:"type"`
	Why          string        `json:"why,omitempty"`
	Recall       *Rehearsal    `json:"recall,omitempty"`
	OccurredAt   *Instant      `json:"occurred_at,omitempty"`
	EndedAt      *Instant      `json:"ended_at,omitempty"`
	Place        string        `json:"place,omitempty"`
	Participants []Participant `json:"participants,omitempty"`
	Content      []Utterance   `json:"content,omitempty"`
	Subjects     []SubjectRef  `json:"subjects,omitempty"`
	Involves     []Involvement `json:"involves,omitempty"`
	PartOf       string        `json:"part_of,omitempty"`
	InReplyTo    string        `json:"in_reply_to,omitempty"`
	Appraisals   []Appraisal   `json:"appraisals,omitempty"`
	Attributes   Attributes    `json:"attributes,omitempty"`
	Profiles     []string      `json:"profiles,omitempty"`
	Source       *Source       `json:"source,omitempty"`
}

type Rehearsal struct {
	Count  int      `json:"count"`
	LastAt *Instant `json:"last_at,omitempty"`
}

// Appraisal is one judgement of how something landed, by someone, at a time.
// A nil scale means that part was not judged.
type Appraisal struct {
	Bucket     string       `json:"bucket"`
	ID         string       `json:"id"`
	Target     string       `json:"target"`
	By         string       `json:"by"`
	At         *Instant     `json:"at,omitempty"`
	Valence    *int         `json:"valence,omitempty"`
	Intensity  *int         `json:"intensity,omitempty"`
	Importance *int         `json:"importance,omitempty"`
	Surprise   *int         `json:"surprise,omitempty"`
	Dimensions map[Type]int `json:"dimensions,omitempty"`
	Confidence *float64     `json:"confidence,omitempty"`
	Method     string       `json:"method,omitempty"`
	Profiles   []string     `json:"profiles,omitempty"`
	Source     *Source      `json:"source,omitempty"`
}

// Link is a typed relation between two records.
type Link struct {
	Bucket   string   `json:"bucket"`
	ID       string   `json:"id"`
	From     string   `json:"from"`
	Type     Type     `json:"type"`
	To       string   `json:"to"`
	At       *Instant `json:"at,omitempty"`
	Profiles []string `json:"profiles,omitempty"`
	Source   *Source  `json:"source,omitempty"`
}

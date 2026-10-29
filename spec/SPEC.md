# Memory

Draft 0.1

A record format and a recall contract for things that happened, what was learned from them, and how to bring the right ones back.

## 1. Why

Most memory stores keep a sentence and a vector. That loses what makes a memory useful later:

- **When** it happened, and when it stopped being true.
- **Where** it happened. A Slack thread, a pull request review, and a meeting are different places with different weight, audience and tone.
- **Who** was there, and in what role: who said it, who decided, who was only mentioned.
- **What was said**, word for word, by whom.
- **How it landed**: good or bad, how strongly, how much it mattered.
- **What it is about**: files, tickets, customers, products.
- **What it led to**, and what it replaced.

People remember along all of these at once. A memory comes back best when the cues present at recall match the cues present when it was formed (the encoding specificity principle, Tulving and Thomson, 1973). A store that drops the cues cannot recall that way.

This spec defines the record so the cues are kept, and defines recall so they are used.

## 2. Two kinds of record

Memory keeps two kinds of record, following the episodic and semantic split in human memory.

| Record | Is | Example |
|---|---|---|
| **Event** | Something that happened, once, at a time and a place | "In the Tuesday planning meeting, Ana said retries must stop after three attempts." |
| **Memory** | Something learned that holds until it does not, drawn from one or more events | "Retries stop after three attempts." |

Events never change after capture; they are evidence. Memories change: they are reinforced, refined, contradicted, superseded. Every memory points at the events it was drawn from, so any memory can be traced back to the meeting, thread or review that produced it, and any event can show every memory it produced.

Around them sit the things both refer to:

| Thing | Is |
|---|---|
| **Place** | Where an event happened: a channel, a thread, a pull request, a meeting, a session |
| **Actor** | Who: a person, a team, an agent, a bot |
| **Subject** | What it is about: a file, a ticket, a customer, a product, a topic |
| **Appraisal** | A judgement of how something landed, made by someone, at a time |
| **Link** | A typed relation between any two records |

Place, actor and subject are the three kinds of **entity** this spec names. They are not the only ones: any other kind of thing can be declared and attached the same way (§11).

## 3. Shared conventions

- Every record has an `id` (opaque string) and a `type`.
- Types are namespaced: `github.pull_request.review_comment`, `slack.thread.message`, `meeting.utterance`. The part before the first dot is the **platform**. Unknown types are kept and recalled as their nearest known parent (`github.pull_request.review_comment` falls back to `github.pull_request`, then `github`).
- Times are RFC 3339 with an offset. A time may carry `precision` (`second`, `minute`, `day`, `month`, `year`) because "sometime in March" is a real memory. A precise time is written as a string; an imprecise one as `{ "at": "2026-03-01T00:00:00Z", "precision": "month" }`.
- Places, actors, subjects and declared kinds are all **entities** and share one shape (§11.1). An entity says which of the named kinds it is with `class` (`place`, `actor`, `subject`); an entity of a kind declared by a profile has no `class`.
- Any field may be absent. Absent means **unknown**, never a default. An event with no appraisal is unjudged, not neutral.
- Every record carries `source`: where the record itself came from (§9).
- A record written with types, roles or attributes from profiles lists them in `profiles` (`["crm@1"]`), so it still reads correctly after a profile changes.

## 4. Event

```json
{
  "id": "ev_01J...",
  "type": "github.pull_request.review_comment",
  "occurred_at": "2026-04-19T14:02:11Z",
  "ended_at": null,
  "place": "pl_pr_4521_thread_3",
  "participants": [
    { "actor": "ac_ana", "role": "author" },
    { "actor": "ac_ben", "role": "addressee" },
    { "actor": "ac_review_bot", "role": "mentioned" }
  ],
  "content": [
    { "speaker": "ac_ana", "text": "This should stop after three tries; the gateway bans us at five.", "at": "2026-04-19T14:02:11Z" }
  ],
  "subjects": [
    { "subject": "sb_file_retry_go", "role": "about" },
    { "subject": "sb_vendor_gateway", "role": "affected" }
  ],
  "part_of": "ev_review_4521_round_2",
  "in_reply_to": "ev_01H...",
  "appraisals": ["ap_..."],
  "source": { "...": "§9" }
}
```

| Field | Meaning |
|---|---|
| `occurred_at`, `ended_at` | When it happened. Meetings and sessions have both; a message has only the first. |
| `place` | Where (§5). Required in spirit: an event with no place loses most of its cues. |
| `participants` | Who took part, each an actor with a role from the roles below. One actor can take part in many events with a different role in each. |
| `content` | What was said or done, as utterances with a speaker. Kept verbatim; summaries are memories, not content. |
| `subjects` | What it is about, with a role. |
| `part_of` | The larger event this belongs to. A meeting contains utterances; a review round contains comments; a session contains turns. |
| `in_reply_to` | The event this answers. |

Roles: `author`, `speaker`, `addressee`, `decider`, `approver`, `reviewer`, `assignee`, `present`, `mentioned`, `affected`, `observer`. A role says how much a person's presence should count as a cue: the decider of an event is a strong cue for it, someone merely mentioned is weak.

The Activity Vocabulary spreads the same idea across separate properties. Records in that format map in directly:

| Activity Vocabulary | Role here |
|---|---|
| `actor` | `author`, `speaker` or `decider`, by activity type |
| `to`, `cc` | `addressee` |
| `tag` of type `Mention` | `mentioned` |
| `attributedTo` | `author` when no `actor` is given |
| `audience` | the place's `audience` (§5), not a role |
| `context` | `part_of` |
| `inReplyTo` | `in_reply_to` |
| `location` | `place` |

### Composite events

A meeting is an event with `ended_at`, a place of type `meeting`, attendees as participants with the role `present`, and each utterance as a child event with `part_of` pointing at it. The same shape covers a pull request review round, a Slack thread, a coding session, an incident. Recall can return the composite, its children, or the memories drawn from either.

## 5. Place

Places nest. Where something was said is a path, not a label.

```json
{
  "id": "pl_pr_4521_thread_3",
  "class": "place",
  "type": "github.pull_request.review_thread",
  "name": "Review thread on retry.go",
  "parent": "pl_pr_4521",
  "url": "https://github.com/...",
  "audience": { "visibility": "team", "members": ["ac_ana", "ac_ben"] },
  "medium": { "sync": false, "formality": "work", "record": "written" }
}
```

Example paths:

```
github > org acme > repository shop > pull request 4521 > review thread on retry.go
slack > workspace acme > #payments > thread from 19 Apr
meeting > series "Payments weekly" > 21 Apr 2026
claude_code > session in shop > turn 14
email > thread "Gateway limits"
```

| Field | Meaning |
|---|---|
| `type` | Namespaced place type. It decides the icon and the default weight of what happens there. |
| `parent` | The enclosing place. Recall can match at any level: "anything in #payments" includes its threads. |
| `audience` | Who could see what happened here. It is the access rule for recall (§12.6). |
| `medium.sync` | Whether people were present at once (meeting, call) or not (thread, review). |
| `medium.formality` | `casual`, `work`, `formal`, `binding`. A decision in a design review weighs more than one in a DM. |
| `medium.record` | `written`, `transcribed`, `recalled`. A written comment is exact; notes after a call are someone's recollection. |

## 6. Actor

```json
{
  "id": "ac_ana",
  "class": "actor",
  "type": "person",
  "name": "Ana Ruiz",
  "image": "https://...",
  "identities": [
    { "platform": "github", "handle": "anaruiz" },
    { "platform": "slack", "handle": "U02ABC" },
    { "platform": "email", "handle": "ana@acme.example" }
  ],
  "member_of": ["ac_team_payments"]
}
```

- `type`: `person`, `team`, `agent`, `bot`, `organisation`, or a namespaced type from a profile.
- `identities` join one actor across platforms, so Ana in Slack and Ana on GitHub are one cue. Joining identities is a claim with a source and a confidence, never silent.
- An actor is the one record of who someone is. Their part in a given event or memory is a participant: the actor plus the role they hold there (`participants: [{ actor, role }]`).
- An agent (a coding assistant, a review bot) is an actor too. What an agent said is evidence like anything else, with its own weight.

## 7. Subject

What events and memories are about.

```json
{ "id": "sb_file_retry_go", "class": "subject", "type": "code.file", "name": "internal/retry/retry.go", "parent": "sb_repo_shop" }
```

Types are namespaced like places: `code.repository`, `code.file`, `code.symbol`, `ticket.issue`, `customer`, `product.feature`, `topic`. Subjects nest (`code.symbol` inside `code.file` inside `code.repository`) so recall can widen from a function to its file to its folder.

## 8. Memory

```json
{
  "id": "mm_retry_limit",
  "type": "constraint",
  "statement": "Retries to the payment gateway stop after three attempts.",
  "why": "The gateway bans a client after five failed calls in a minute.",
  "valid_from": "2026-04-19T14:02:11Z",
  "valid_until": null,
  "confidence": 0.9,
  "evidence": [
    { "event": "ev_01J...", "role": "established" },
    { "event": "ev_02K...", "role": "reinforced" }
  ],
  "subjects": [{ "subject": "sb_file_retry_go", "role": "about" }],
  "participants": [{ "actor": "ac_ana", "role": "decider" }],
  "appraisals": ["ap_..."],
  "recall": { "count": 12, "last_at": "2026-10-20T09:00:00Z" },
  "source": { "...": "§9" }
}
```

| Field | Meaning |
|---|---|
| `type` | What kind of learning: `decision`, `constraint`, `convention`, `lesson`, `preference`, `fact`, `intent`, `commitment`. Namespaced extensions allowed. |
| `statement` | One sentence that stands alone. |
| `why` | The reason, so it can be judged when circumstances change. |
| `valid_from`, `valid_until` | When it held in the world. |
| `evidence` | The events it came from, each with a role: `established`, `reinforced`, `refined`, `contradicted`, `retired`. |
| `recall` | How often and how lately it was brought back. Written by the store on recall, never by the caller. |

A memory has two clocks, as in bi-temporal databases: `valid_from` / `valid_until` (when it was true) and `source.recorded_at` (when the store learned it). "What did we believe on 1 May?" and "what was true on 1 May?" are different questions and both must be answerable.

## 9. Source and appraisal

### Source

```json
{
  "system": "github",
  "native_id": "PRRC_kwDO...",
  "url": "https://github.com/...",
  "recorded_at": "2026-04-19T14:05:00Z",
  "method": "webhook",
  "by": "ac_capture_agent"
}
```

`method`: `webhook`, `import`, `manual`, `extracted`, `inferred`. A memory extracted by a model says so; a person typing it in says so.

### Appraisal

How something landed is a judgement, not a property. Different people judge the same event differently, and judgements change.

```json
{
  "id": "ap_...",
  "target": "ev_01J...",
  "by": "ac_extractor_model",
  "at": "2026-04-19T14:05:00Z",
  "valence": -1,
  "intensity": 4,
  "importance": 5,
  "surprise": 3,
  "dimensions": { "crm.risk": 4 },
  "confidence": 0.7,
  "method": "model",
  "profiles": ["crm@1"]
}
```

| Field | Scale | Meaning |
|---|---|---|
| `valence` | -2 to 2 | Bad to good |
| `intensity` | 1 to 5 | How strongly it was felt (arousal) |
| `importance` | 1 to 5 | How much it matters for what comes after |
| `surprise` | 1 to 5 | How unexpected it was; surprising events are worth recalling first |
| `dimensions` | per profile | Judgements a profile declares (§11.4), keyed by namespaced name |

An appraisal judges at least one of these.

A record's effective appraisal is a reduction of its appraisals (the latest from a person outranks a model's; several people average). The reduction is the store's, and recall can ask for a specific appraiser instead ("how did the customer feel about it?").

## 10. Link

```json
{ "id": "ln_7", "from": "mm_retry_limit_v2", "type": "supersedes", "to": "mm_retry_limit", "at": "2026-06-01T00:00:00Z", "source": { "...": "§9" } }
```

| Between | Types |
|---|---|
| memory → memory | `supersedes`, `refines`, `contradicts`, `depends_on`, `example_of` |
| event → event | `in_reply_to`, `part_of`, `follows`, `caused`, `duplicates` |
| memory → event | carried as `evidence` (§8) |
| any → subject, place, actor | carried on the record itself |

Links are records with a source, so "who said these two contradict?" has an answer.

## 11. Extending

Events and memories in other fields carry things this spec does not name: a deal and its amount in sales, a patient and a dose in care, a device and a firmware version in support, a song and a venue in a diary. The model has to take them without a new version of the spec and without a change to the engine.

### 11.1 Entities

Place, actor and subject are kinds of one thing, an **entity**: something that exists apart from any one event and that events and memories can involve. Any new concept is a new entity type.

```json
{ "id": "en_deal_884", "type": "crm.deal", "name": "Acme renewal 2027", "parent": "en_account_acme",
  "attributes": { "crm.amount": { "value": 120000, "unit": "EUR" }, "crm.stage": "negotiation" }, "profiles": ["crm@1"] }
```

An entity has an `id`, a namespaced `type`, a `name`, an optional `class` (§3), an optional `parent` (any entity, so concepts nest), optional `attributes` (§11.3) and a `source`. The fields §5 and §6 give places and actors are optional on any entity.

### 11.2 Involvement

An event or memory involves entities through one general list, each with a role:

```json
"involves": [
  { "entity": "en_deal_884", "role": "crm.at_stake" },
  { "entity": "en_contract_v3", "role": "changed" }
]
```

`place`, `participants` and `subjects` are the named, well-known parts of the same relation: the engine stores all of them as involvements and treats them alike. So a new kind of entity is a cue from the moment it is written: it can be matched, filtered on, weighted by rarity and role, and followed by spreading (§12.3), with no change to recall.

### 11.3 Attributes

Records and entities can carry values the spec does not name, under namespaced keys:

```json
"attributes": {
  "crm.amount": { "value": 120000, "unit": "EUR" },
  "care.dose_mg": 50,
  "support.firmware": "4.2.1"
}
```

Keys are namespaced so two vocabularies never collide. An attribute with no profile (§11.4) is stored and returned but not indexed; one declared in a profile can be filtered and sorted on in recall (`filters.attributes`).

### 11.4 Profiles

A profile is a document that declares a vocabulary for a field. It is how a new concept gets meaning and not only storage.

```json
{
  "profile": "crm",
  "version": "1",
  "entity_types": [
    { "type": "crm.account", "icon": "building" },
    { "type": "crm.deal", "parent_type": "crm.account", "icon": "handshake" }
  ],
  "event_types": [
    { "type": "crm.call", "falls_back_to": "meeting", "medium": { "sync": true, "record": "transcribed" } }
  ],
  "memory_types": [{ "type": "crm.objection", "falls_back_to": "fact", "half_life_days": 180 }],
  "roles": [{ "role": "crm.buyer", "cue_weight": 0.9 }, { "role": "crm.at_stake", "cue_weight": 1.0 }],
  "attributes": [
    { "key": "crm.amount", "schema": { "type": "object", "properties": { "value": { "type": "number" }, "unit": { "type": "string" } } }, "filterable": true },
    { "key": "crm.stage", "schema": { "enum": ["lead", "negotiation", "won", "lost"] }, "filterable": true }
  ],
  "appraisals": [{ "dimension": "crm.risk", "scale": [1, 5] }],
  "links": [{ "type": "crm.blocks", "from": "memory", "to": "entity" }]
}
```

A profile can declare:

| Declares | So that |
|---|---|
| entity, event and memory types | they have a parent type to fall back to, an icon, a default weight and decay; an entity type can also say which named kind (`class`) it is |
| roles | they have a cue weight, the way `decider` outweighs `mentioned` |
| attributes | they are validated and can be filtered on |
| appraisal dimensions | judgements beyond valence, intensity, importance and surprise (risk, trust, pain) |
| link types | relations beyond the ones in §10 |

Rules:

- Everything a profile adds is namespaced by the profile's name. The unnamespaced names belong to this spec.
- Unknown types, roles and attributes are kept, never rejected. They work as plain cues until a profile gives them weight.
- Profiles are versioned. A record states the profiles it was written with, so an old record still reads correctly after a profile changes.
- This spec's own entities, roles and memory types are themselves a profile, the core one, so extensions and the core follow one set of rules.

### 11.5 What stays fixed

Extensions add kinds of things, roles, values and judgements. They do not change the two record kinds (event and memory), the two clocks, evidence, access, or the recall contract. Those are what let any two implementations, and any two fields, recall the same way.

## 12. Recall

Recall is the reason the store exists. It must be excellent at one job: given what someone is looking at right now, bring back what they would want to know, and say why.

### 12.1 The question is a context

A recall request describes a situation, not only a string. The situation is the cue set.

```json
{
  "context": {
    "text": "add a retry to the gateway client",
    "place": "pl_pr_4600",
    "participants": ["ac_ben"],
    "subjects": ["sb_file_retry_go"],
    "involves": [],
    "at": "2026-10-29T10:00:00Z"
  },
  "filters": {
    "types": ["constraint", "decision"],
    "valence": { "max": -1 },
    "occurred": { "from": "2026-01-01", "to": "2026-07-01" },
    "valid_at": "now",
    "places": { "under": "pl_slack_payments" },
    "attributes": { "crm.stage": ["negotiation"] }
  },
  "as_of": null,
  "limit": 10,
  "expand": { "evidence": true, "neighbours": 1 },
  "explain": true
}
```

- `context`: what the caller is looking at. Every part is a cue.
- `filters`: hard limits. A filtered-out record is never returned, whatever its score.
- `valid_at`: only memories true at that time. Defaults to now; `null` includes retired memories.
- `as_of`: answer as the store would have answered at that time (the recorded-time clock).
- `expand`: also return the events behind each memory, and memories one link away.

### 12.2 Scoring

Each candidate gets a score from separate signals. Each signal is returned on its own when `explain` is set, so a caller can see why something came back and tune the weights.

| Signal | Meaning |
|---|---|
| `relevance` | Text match between the context text and the record, from lexical and semantic matching. Each is scaled so its best match scores 1, and a record keeps its stronger showing, so neither an exact identifier nor a paraphrase is missed. |
| `cue` | Overlap between the context's cues and the record's cues (every involved entity: places, participants, subjects and any declared kind, at every level of nesting). Each shared cue counts by how rare it is: a file touched by two memories is a strong cue, a person on every event is almost none. Roles scale it: a shared decider counts more than a shared mention. |
| `recency` | How recently it happened, decaying with a half-life per memory type (a convention fades slower than a workaround). Each recall resets part of the decay, as rehearsal does. |
| `importance` | From the effective appraisal. |
| `affect` | Strongly felt and surprising records score higher when the context asks "what went wrong" or carries a valence filter. |
| `authority` | From the place's formality and the participants' roles: a binding decision in a design review outranks a passing remark in a DM. |
| `validity` | Down-weights memories contradicted but not yet retired, so the caller can see the conflict instead of a silent pick. |

Default combination: weighted sum after normalising each signal to 0..1. `cue` is the share of the context's own cues a record shares: sharing every one in the strongest role scores 1, so a thin match reads as thin even when nothing better exists. The weights are part of the request (`weights`) and have defaults per use, such as `review`, `planning` and `incident`.

### 12.3 Spreading from the cues

A direct match is not always the best answer. The store keeps every record and cue as a graph (events, memories, places, actors, subjects, links) and, from the cues in the context, runs a personalised PageRank to find what is strongly connected to them even without a word in common (as HippoRAG does). This is how "this file" reaches "the incident last spring that changed how we call the gateway".

### 12.4 What comes back

```json
{
  "results": [
    {
      "record": { "...": "memory or event" },
      "score": 0.83,
      "because": {
        "relevance": 0.61,
        "cue": { "score": 0.9, "shared": ["sb_file_retry_go", "pl_pr_4521"] },
        "recency": 0.4,
        "importance": 1.0,
        "path": ["sb_file_retry_go", "ev_01J...", "mm_retry_limit"]
      },
      "evidence": [{ "event": "ev_01J...", "place_path": ["github", "acme/shop", "PR 4521", "retry.go thread"], "quote": "This should stop after three tries..." }],
      "conflicts": []
    }
  ]
}
```

A result always carries enough to show the person why it came back: which cues it shared, the path it was reached by, and a quote from the event that established it.

### 12.5 Questions recall must answer

These are the acceptance tests for the recall contract.

1. What do we know that touches this file, ranked for a review?
2. What did this meeting produce, and what has happened to each item since?
3. What went badly last quarter, and where was it discussed?
4. What did Ana decide about payments, across GitHub, Slack and meetings?
5. What did we believe about retries on 1 May, and what was actually true then?
6. Which memories contradict each other right now?
7. What has not been recalled in a year but was once important? (candidates to retire or relearn)
8. Who should be asked about this subject? (actors ranked by their roles on related events)

### 12.6 Access

The audience of a place limits who may recall what happened there. A memory drawn from events in several places can be seen only by those who could see all of its evidence, unless someone with access promotes it to a wider audience; the promotion is itself a recorded event. Recall never returns, quotes or counts a record the caller cannot see.

### 12.7 Recall writes back

A recall that a caller marks as used updates the memory's `recall` count and time. Recall that is only browsing does not. Rehearsal is an input to ranking, so it has to mean "this was useful", not "this was on screen".

## 13. Capture

How events get in is outside this spec, but the format asks capture for:

- The most specific place type available (`github.pull_request.review_comment`, not `github`).
- Every actor with a role, not only the author.
- Content verbatim, with speakers.
- The parent event (`part_of`) for anything inside a meeting, thread, review or session.
- Appraisal left empty rather than guessed when nothing supports a judgement.

## 14. Showing it

The record carries what a timeline needs to be read at a glance:

- The place type gives each event an icon (a pull request, a Slack thread, a meeting, a coding session).
- Actors carry `image`, so an event can show the faces of who was there, and `content` can show what they said.
- Composite events open into their parts, so a meeting can be followed down to each memory it produced and forward to what happened to each one.
- Appraisal places events by how they landed; importance sizes them.

## 15. Not in this spec

- How records are stored, indexed or sharded.
- How a model extracts memories from events.
- Embedding models and their dimensions.
- The wire protocol. A reference service will define one; this document defines what it carries.

## 16. Open questions

1. Is `surprise` worth a field, or is it derivable from how far a memory moved the store?
2. Should appraisal of a composite event be derived from its parts, or judged on its own?
3. How are two actors merged when an identity join turns out wrong, without losing what each was credited with?
4. Default decay half-lives per memory type: what evidence sets them?
5. How much of the cue graph should a caller be able to query directly, beyond recall?
6. Who may publish a profile, and how are two profiles that claim the same meaning reconciled?

## References

- Tulving, E. and Thomson, D. M. (1973). Encoding specificity and retrieval processes in episodic memory. Psychological Review 80(5), 352 to 373.
- W3C Activity Vocabulary: actor, object, target, context, location, audience, inReplyTo. https://www.w3.org/TR/activitystreams-vocabulary/
- W3C PROV-O: entity, activity, agent, roles, invalidation. https://www.w3.org/TR/prov-o/
- Westermann, U. and Jain, R. (2007). Toward a Common Event Model for Multimedia Applications. IEEE MultiMedia. doi:10.1109/MMUL.2007.23
- Park, J. S. et al. (2023). Generative Agents: Interactive Simulacra of Human Behavior. https://arxiv.org/abs/2304.03442
- Gutiérrez, B. J. et al. (2024). HippoRAG: Neurobiologically Inspired Long-Term Memory for Large Language Models. https://arxiv.org/abs/2405.14831
- Graphiti temporal edges (valid_at, invalid_at). https://github.com/getzep/graphiti

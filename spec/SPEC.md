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
- Every record, and every entity, lives in one `bucket` (§13.2). The bucket decides who may read it; nothing in the record itself grants access.
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
| `audience` | Who could see what happened here, as the source described it. It is a fact about the place, not a permission: who may read the record of it is decided by its bucket (§13). |
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
- `identities` join one actor across platforms, so Ana in Slack and Ana on GitHub are one cue. Joining identities is a claim with a source and a confidence, never silent. It describes; it never signs anyone in or grants anything (§13.7).
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
| `depends_on` | Every record the memory was made from, with the version read: events and memories read while extracting it, retrieved context included, not only the ones it cites. The engine records what it served to the extraction; the writer may only add (§13.4). It decides who may read the memory. |
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

`installation` names which account or install of the system the record came from (one Slack workspace, one GitHub App installation). Two records are the same record only when workspace, system, installation and `native_id` all match; the same `native_id` in two customers' Slack workspaces is two records.

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

Every recall runs as a principal (§13.6) and sees only records it may read (§13.3). Records it may not read are removed before candidates are limited, counted or scored, so they never change what comes back, how it ranks, what is counted or what an explanation says. A caller can narrow a recall to some buckets; it can never widen it past what the principal may read.

### 12.7 Recall writes back

A recall that a caller marks as used updates the memory's `recall` count and time for that principal. Recall that is only browsing does not. Rehearsal is an input to ranking, so it has to mean "this was useful", not "this was on screen". Use is kept per principal: one person's use never changes how a memory ranks for another, because that would reveal what they were working on.

## 13. Access

Being part of something is not permission to read the record of it. Someone can attend a disciplinary meeting, or be mentioned in it, without being allowed to read its notes. So who took part (participants, roles, mentions, a place's `audience`) describes what happened and never grants anything. Access comes only from grants on buckets.

### 13.1 Concepts

| Concept | What it is |
|---|---|
| Workspace | The boundary between customers. Holds principals, groups, buckets, grants and settings. Nothing is read, matched, counted or deduplicated across workspaces. |
| Principal | Something that can act: a person, a bot, a service. Principals sign in; actors (§6) are who records talk about. The two are kept apart. |
| Group | A named set of principals, such as `hr_readers`. |
| Bucket | Where records live. Every record has exactly one. Reading a bucket is the first thing needed to read anything in it; some records need more (§13.3). |
| Grant | Permission for a principal or group to act on a bucket: `read`, `write` or `administer`. |
| Role | What a principal may do to the workspace itself: `owner` changes settings and trusted issuers and names other owners; `admin` manages principals, groups and grants. Neither role reads records. |

A workspace's `external` reference names what it stands for outside the engine, `{ system, type, id }`, such as `{ "system": "sourceant", "type": "workspace", "id": "ws_42" }`. It describes; it never grants, and it is not the `owner` role.

### 13.2 Buckets

- Every record names its `bucket`. Entities do too: a place's name can itself be a secret (`#layoffs-2027`). Principals and groups belong to the workspace, not to a bucket.
- One audience per bucket. A private source container (a private Slack channel, a private repository) gets its own bucket; broad shared spaces get broad buckets.
- A bucket bound to a source is keyed by the source's permanent identifiers, never its name: `{ "system": "slack", "installation": "T024BE7LD", "container": "C012ABCDE" }`. Renaming the channel changes a label and nothing else.
- A record whose bucket cannot be established goes to the workspace's quarantine bucket, which only principals granted it can read. It is never guessed into a broader one.
- A record cannot move between buckets, and cannot be copied into another. Moving a record is publishing it, and publishing is not in this version (§13.10).

### 13.3 Who may read a record

Every record has a requirement set: the buckets someone must be able to read to read it.

- An event or entity requires its own bucket.
- A link requires its own bucket and the requirement sets of both ends.
- An appraisal requires its own bucket and the requirement set of what it judges.
- A memory version requires its own bucket and the requirement sets of everything it was made from (§13.4).

A principal may read a record when it may read every bucket in the record's requirement set. Requirement sets are worked out when a record is written and stored with it, so reading never walks the graph to decide.

This one rule decides everything a principal sees. It is applied before candidates are limited, counted or ranked; it applies to every way of reading (recall, reading by id, expanding evidence, walking places and links, checking references while capturing); and it is checked against one snapshot of permissions taken when the request starts.

A record the principal may not read does not exist for it:

- Returned records keep only references the principal may read. A reference to anything else is removed, not replaced with a marker.
- Errors never confirm that a hidden record exists. Writing a reference to a record the writer may not read fails exactly as a reference to nothing does.
- Deduplication never answers with a record the writer may not read.

As-of questions (§12.1) choose which version of what was known; they never choose whose permissions apply. Permissions are always the current ones: someone who left yesterday cannot read last year's records by asking about last year.

### 13.4 Dependencies

What a memory says can reveal what it was made from, whatever it cites. A model that read a private thread and a public one can write a memory citing only the public one and still repeat the private one. So a memory's readers are decided by everything it was made from, and the engine does not take the writer's word for what that was.

- **Extraction runs in a session.** A service with the `extract` action opens a session, reads through the engine within it, and writes memories within it. The engine records every record it served in the session, with its version, and every memory written in the session depends on all of them. The writer can add to `depends_on` (for what it read elsewhere); it cannot remove what the engine recorded.
- **Inputs carry versions.** A dependency names a record and, for a memory, the version that was read: `{ "record": "mm_retry_limit", "version": 3 }`. Events never change, so they need none.
- **Requirement sets are captured, not recomputed.** A memory version's requirement set is fixed when it is written, from the requirement sets of the input versions it read. A later, more restricted version of an input does not change memories made from the earlier one; a memory made from the later one inherits the restriction.
- **New versions only add.** A new version of a memory keeps every requirement of the one before. Dropping a requirement would widen who may read it, which is publishing.
- **Deleted buckets stay in requirement sets.** A bucket's id is never reused, and nobody may read a deleted bucket, so a memory made from one becomes unreadable rather than wider.
- **People are accountable for what they type.** A memory written by a person outside a session (`method: manual`) requires its own bucket and what its writer declares. The engine cannot know what a person remembers; it records who wrote it.

### 13.5 Grants

A grant comes from one of three places, and each is kept apart so it can be changed and removed on its own:

| Origin | Set by | Removed when |
|---|---|---|
| Manual | A principal with `administer` on the bucket or the workspace | Revoked by such a principal |
| Synced | A connector, for buckets bound to its own installation (§13.8) | The source no longer lists the member, or synchronization goes stale |
| Open | An owner, by turning on open mode | Open mode is turned off |

- Reading, administering grants and publishing are separate rights. Administering a workspace does not let anyone read its records.
- Adding a principal to a group is granting it everything the group may do, and needs the same rights as granting each of those directly.
- An administrator giving themselves read, directly or by joining a group, follows the workspace setting: recorded, or approved by a second administrator.
- Changing a setting removes the grants it created: turning open mode off removes open grants; switching a source-bound bucket back to mirroring removes its manual readers. The removal is recorded.
- Every change to a grant is recorded with who made it, on whose behalf and when.
- A change takes effect from the next request. A request already running keeps the snapshot it started with.

### 13.6 Who is acting

Every request answers three questions, and each answer is proven, never stated.

**Which service is calling.** Each service has its own credential issued by the engine, scoped to one workspace, a fixed list of actions and, for a connector, its registered installations. Credentials can be revoked.

**Who it acts for.** A person is identified by a short-lived token signed by an issuer the workspace trusts (an app the person signed in to, such as SourceAnt). The engine verifies it itself and requires:

| Claim | Must be |
|---|---|
| `iss` | A trusted issuer of this workspace |
| `sub` | The person's stable id at that issuer, linked to a principal |
| `aud` | This engine |
| `workspace` | The workspace of the request |
| `azp` | The service making the request; a token issued to one service is refused from another |
| `exp` | In the future, and no further than the workspace allows |

Issuers publish their keys so they can be rotated, and removing an issuer revokes every token it signed. A service saying "this is Ana" without such a token is never enough.

**What it may do.** The answer is the intersection of what the service's credential allows, what the person delegated to it, and what the person's grants allow. A connector holding an administrator's token still cannot administer grants, because its credential does not allow that action. A service with no person behind it, such as a nightly bot, acts as its own principal with its own grants.

Delegation is a record in the workspace: `{ principal, service, buckets, actions, expires }`, created by the person themselves. The service an issuer is registered for (the app the person signed in to) acts with the person's own grants and needs none. Any other service acting for a person, such as an assistant, acts only within a delegation the person made for it, and without one acts for nobody.

### 13.7 Linking accounts

A source user (Slack user `U0ABC` in installation `T024BE7LD`) becomes a principal only through a verified link: the person signs in to a trusted issuer and proves the source account with that source's own sign-in. Links are kept apart from actors' `identities`:

- A link is keyed by source, installation and the source's permanent user id.
- One source account links to one principal. A second claim is refused until the first is removed.
- Matching names, emails or handles never links anything. An account named "CEO" maps to nobody until someone proves it is theirs.
- Writing an actor never changes a link.
- Removing a link removes, at once, every synced grant that came through it. The principal keeps manual grants, which were given to the principal, not to the source account.

### 13.8 Connectors

A connector brings a source's records and permissions in.

- It is registered with the engine for named installations, and its credential is limited to them. It can create buckets only for containers in those installations and change readers only of those buckets.
- It puts a record in a container's bucket only when every member of the container may read that record at the source. Where a source shows newcomers only what was said after they joined, or restricts single items inside a container, those records go to a bucket of their own (one per audience) or to quarantine. A connector that cannot tell sends them to quarantine.
- It reads membership from the source's API with its own source credential, never from what is said in a message.
- Synchronization replaces a bucket's synced members completely. A sync that fails or is partial changes nothing and renews nothing.
- Synced grants carry an expiry and are renewed only by a complete sync. A connector that stops syncing, or is removed, leaves grants that lapse rather than ones that last forever.
- Manual grants on a source-bound bucket are allowed only when the workspace setting allows readers beyond the source.

### 13.9 Settings

What changes between a company that shares everything and one with HR and legal teams is settings. What makes the engine safe is not.

| Setting | Level | Default |
|---|---|---|
| Open mode: every member reads buckets the workspace created itself | Workspace | Off |
| Source-bound buckets: `mirror` the source exactly, `mirror_and_manual` (the source's members plus readers added by hand), or `open` (every member) | Workspace, per connector | `mirror` |
| Administrators may grant themselves read, or need a second administrator | Workspace | May, and it is recorded |
| Longest a person token may live | Workspace | 15 minutes |
| How long a synced grant lasts without a complete sync | Workspace, per connector | 1 day |
| Trusted issuers | Workspace | None |

Open mode and `open` never reach the quarantine bucket. Only owners change settings.

Fixed, whatever the settings:

1. A caller cannot assert who it is or whom it acts for; both are proven.
2. Unknown or unproven access never grants.
3. Taking part in something never grants access to its record.
4. A memory is never readable by anyone who may not read what it was made from.
5. As-of questions use current permissions.
6. Reading never implies the right to publish or to grant.
7. Every way of reading and writing applies the same rule (§13.3).

### 13.10 Not in this version

Each needs rules of its own before it is safe:

- Publishing: writing a memory's statement into a wider bucket, without its evidence, by someone entitled to release every bucket it depends on. This is how a report from accounting reaches the CEO without the CEO reading accounting.
- Contradictions across buckets: finding them, telling the people who may read both sides, and an optional content-free flag for everyone else.
- Moving or copying records between buckets.
- Workspaces inside workspaces, and sharing between workspaces.
- Anonymous readers for public projects.

## 14. Capture

How events get in is outside this spec, but the format asks capture for:

- The most specific place type available (`github.pull_request.review_comment`, not `github`).
- Every actor with a role, not only the author.
- Content verbatim, with speakers.
- The parent event (`part_of`) for anything inside a meeting, thread, review or session.
- Appraisal left empty rather than guessed when nothing supports a judgement.
- The bucket of every record, chosen by the connector from the source's own permissions (§13.2), and quarantine when that cannot be established.
- Extraction inside a session, so the engine knows what each extracted memory was made from (§13.4).

## 15. Showing it

The record carries what a timeline needs to be read at a glance:

- The place type gives each event an icon (a pull request, a Slack thread, a meeting, a coding session).
- Actors carry `image`, so an event can show the faces of who was there, and `content` can show what they said.
- Composite events open into their parts, so a meeting can be followed down to each memory it produced and forward to what happened to each one.
- Appraisal places events by how they landed; importance sizes them.

## 16. Not in this spec

- How records are stored, indexed or sharded.
- How a model extracts memories from events.
- Embedding models and their dimensions.
- The wire protocol. A reference service will define one; this document defines what it carries.

## 17. Open questions

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

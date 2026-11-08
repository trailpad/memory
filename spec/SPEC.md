# Episodic memory

Draft 0.1

The engine stores events: what happened, when, why, who took part, and how it went. Semantic facts and the current contents of team knowledge live in a client application.

## Records

An event is something that happened once. Events, appraisals and links are immutable after capture; corrections are new records. Entities describe places, actors and subjects and may be updated.

Every record has an opaque `id` and a `bucket`. IDs are unique within a workspace across record types. Namespaced types use lowercase words separated by dots, such as `github.pull_request.review_comment`. Unknown types fall back through their namespace parents unless a versioned profile declares another fallback.

Absent fields mean unknown. In particular, no appraisal means unjudged, never neutral.

Times are [RFC 3339](https://www.rfc-editor.org/rfc/rfc3339#section-5.6) with an offset. A precise time is a string. An imprecise time is an object such as `{"at":"2026-03-01T00:00:00Z","precision":"month"}`. Precision is `second`, `minute`, `day`, `month` or `year`.

Schemas use [JSON Schema Draft 2020-12](https://json-schema.org/draft/2020-12/json-schema-core) and are in `schema/`. Standalone records are in `examples/`.

## Event

```json
{
  "bucket": "bk_shop",
  "id": "ev_retry_review",
  "type": "github.pull_request.review_comment",
  "occurred_at": "2026-04-19T14:02:11Z",
  "why": "The gateway rejected clients after five failed calls.",
  "place": "pl_review",
  "participants": [{"actor":"ac_ana","role":"author"}],
  "content": [{"speaker":"ac_ana","text":"I reduced the retry limit to three."}],
  "subjects": [{"subject":"sb_retry","role":"about"}]
}
```

| Field | Meaning |
|---|---|
| `occurred_at`, `ended_at` | When the event started and ended. Both are optional. |
| `why` | Optional reason for the event, at most 8192 Unicode characters. Included in text search. |
| `place` | Entity with class `place`, describing where it happened. |
| `participants` | Actors and their roles in this event. |
| `content` | What was said or done, as utterances with optional speaker and time. |
| `subjects` | Subject entities and their roles. |
| `involves` | Other entities and their roles. |
| `part_of` | Larger event containing this one. May arrive later. |
| `in_reply_to` | Event this answers. May arrive later. |
| `attributes` | Values under namespaced keys. |
| `profiles` | Versioned vocabularies, for example `crm@1`. |
| `source` | Where the captured record came from. |
| `appraisals` | Read only array of readable appraisal objects targeting this event. |
| `recall` | Read only rehearsal count and latest use, returned during recall for the caller. |

Write appraisals separately in the batch's `appraisals` array. Event appraisal and rehearsal fields are derived by the store and cannot be set by a writer.

A meeting is an event with an end time and participants with role `present`. Its utterances are child events whose `part_of` points at it. The same structure covers a review round, thread, session or incident.

Roles include `author`, `speaker`, `addressee`, `decider`, `approver`, `reviewer`, `assignee`, `present`, `mentioned`, `affected`, `observer`, `about` and `on_behalf_of`. The `on_behalf_of` participant identifies the actor another participant acted for. Record both the acting participant (for example, an agent as `author`) and the represented actor as `on_behalf_of`. This role describes participation and grants no access.

A role affects cue weight: a decider is a stronger cue than someone merely mentioned.

## Entities

Entities exist apart from any one event. Named classes are `place`, `actor` and `subject`. Other entity types declared by profiles may omit `class` and must use a namespaced type.

All entities have `bucket`, `id` and `type`. Optional fields include `name`, `parent`, `url`, `image`, `attributes`, `profiles` and `source`. Parents allow nested places and subjects, so a repository can be a cue for events in its review threads.

Places may carry `audience` and `medium`. Audience describes the source audience and grants no access. Medium has optional `sync`, `formality` (`casual`, `work`, `formal`, `binding`) and `record` (`written`, `transcribed`, `recalled`).

Actors may carry `identities` (platform and handle pairs) and `member_of` actor IDs. An actor is anyone or anything that took part in an event, with the participant role saying how. It is separate from the authenticated principal and grants no access.

The core actor types all have class `actor`:

| Type | Meaning |
|---|---|
| `person` | An individual human. |
| `agent` | Software acting on its own judgement, such as a coding agent. |
| `bot` | A scripted account that follows fixed rules. |
| `service` | A system that emits events, such as a calendar, CI pipeline or billing provider. |
| `group` | A formal or informal collective of actors, such as a team, mailing list or chat user group. |
| `organisation` | A company or institution. |

These types align with the [W3C Activity Streams 2.0 actor types](https://www.w3.org/TR/activitystreams-vocabulary/#actor-types): `person` maps to `Person`, `group` to `Group`, `organisation` to `Organization`, `service` to `Service`, and both `agent` and `bot` to `Application`. The distinction between `agent` and `bot` preserves whether software exercises judgement or follows fixed rules.

The legacy actor type `team` remains valid as an alias for `group`. Stored records retain their original data; entity reads, including entities returned by listings, return `group`. New integrations should write `group`.

## Source

A source may contain `system`, `installation`, `native_id`, `url`, `recorded_at`, `method` and `by` (an actor ID). The workspace, bucket, system, installation and native ID identify one captured record. Repeated capture into the same bucket resolves to the stored ID and rewrites references within the batch. The same source captured into two buckets creates two records. For example, one external delivery received by two scopes is stored separately in each scope's bucket.

The store also keeps its own capture time. `occurred_at` answers when an event happened; the store's recorded time answers when it became available to recall.

## Appraisals

An appraisal has `bucket`, `id`, `target` and `by`. It judges at least one dimension. Optional fields include `at`, `confidence` (0 to 1), `method`, `profiles` and `source`.

| Dimension | Scale | Meaning |
|---|---|---|
| `valence` | -2 to 2 | Bad to good |
| `intensity` | 1 to 5 | How strongly it was felt |
| `importance` | 1 to 5 | How much it matters afterwards |
| `surprise` | 1 to 5 | How unexpected it was |

Profiles may declare additional `dimensions` under namespaced keys and their scales. Multiple people can appraise one event. Event reads return all readable appraisals, ordered by appraisal time and ID, with undated appraisals last. No reduction is currently applied. Appraisals do not currently contribute a separate ranking signal.

An appraisal requires access to its own bucket and its target's requirements. Read responses remove references to appraisers the caller cannot read.

## Links

A link has `bucket`, `id`, `from`, `type` and `to`, plus optional `at`, `profiles` and `source`. Event relations include `in_reply_to`, `part_of`, `follows`, `caused` and `duplicates`. Links require access to both endpoints as well as their own bucket.

## Changes to team knowledge

A client application owns knowledge items and their current semantic contents. This engine records the actions that changed them.

| Event type | Occurrence |
|---|---|
| `knowledge.captured` | An actor captured a knowledge item. |
| `knowledge.updated` | An actor changed its contents. |
| `knowledge.status_changed` | An actor changed its status. |
| `knowledge.removed` | An actor removed it from the client application. |

Each event names the knowledge item as a subject entity with `class: "subject"` and `type: "knowledge.item"`. Its participant names the actor with role `author`. The item ID stays stable across its history. Removal does not erase prior events.

Use `knowledge.before` and `knowledge.after` attributes for the values before and after the action. Each value can be an object containing changed fields such as `text` or `status`. Use null for the absent side of capture or removal. These values describe a historical transition; they are not a current fact store. Use `why` for the reason and `occurred_at` for the action time.

See `examples/event.knowledge-updated.json` and `examples/entity.knowledge-item.json`.

## Profiles

A profile declares its `profile` name and numeric string `version`. It can declare `entity_types`, `event_types`, `roles`, `attributes`, `appraisals` and `links`. Extensions use the profile's namespace. Unknown attributes are kept as written; declared attribute schemas are validated. Appraisal dimensions require a declared scale.

Event type declarations support `falls_back_to`, `icon`, `medium` and `half_life_days`. Recall resolves declarations using the profiles recorded on each event, plus the core profile. Published versions cannot change. An undeclared event type has a half life of 365 days.

## HTTP contract

Service requests authenticate with `Authorization: Bearer <service-key>`, using the header syntax from [RFC 6750 section 2.1](https://www.rfc-editor.org/rfc/rfc6750#section-2.1). A service acting for a person also sends a signed person token in `Memory-Person`.

| Method and path | Behavior |
|---|---|
| `GET /healthz` | Health check without authentication. |
| `POST /v1/records` | Atomically writes `entities`, `events`, `appraisals` and `links` arrays. |
| `GET /v1/entities/{id}` | Reads one entity. |
| `GET /v1/events/{id}` | Reads one event with readable appraisal values. |
| `GET /v1/events` | Lists dated events with readable appraisal values and involved entities. |
| `POST /v1/events/links` | Lists dated events and ranked connections within a time window. |
| `POST /v1/recall` | Returns ranked events. |
| `POST /v1/events/use` | Records use of event IDs from `{"events":["ev_retry_review"]}`. Returns 204. |
| `PUT /v1/buckets` | Binds a source container to a bucket. |
| `PUT /v1/buckets/{id}/readers` | Replaces source accounts allowed to read a bound bucket. |

Batch writes return `stored`, a map from submitted to canonical IDs, and optional `unchanged` IDs. Rewriting an immutable record differently returns 409. Validation returns 422 with field problems. Hidden and missing records both return 404. Unknown request fields are rejected.

A recall response contains event results:

```json
{
  "results": [
    {
      "id": "ev_meeting",
      "record": {"id": "ev_meeting", "bucket": "work", "type": "meeting"},
      "score": 0.8
    }
  ]
}
```
 Status codes follow [HTTP Semantics, RFC 9110 section 15](https://www.rfc-editor.org/rfc/rfc9110#section-15).

Event listings are ordered by occurrence time, then ID. `from` is inclusive and `to` exclusive. Optional filters are repeated `bucket`, `part_of`, and `top_level=true`. `top_level` cannot be combined with `part_of`. `limit` defaults to 200 and is at most 1000. `after` continues from an event ID. The response has `events`, `entities`, and optional `next` when the page is full. Undated events can be read by ID or recalled but are excluded from listings.

## Recall

```json
{
  "context": {"text":"retry gateway","subjects":["sb_retry"]},
  "filters": {"types":["github.pull_request"]},
  "limit": 10,
  "explain": true
}
```

Context supports `text`, `place`, `participants`, `subjects`, `involves` and `at`. At least text or one entity cue is required. Filters support `types` (including declared fallback types), `buckets`, and `occurred` with `from` and `to`. `as_of` limits recall to events and uses recorded by that time. Context `at` selects the time used for recency scoring and limits use timestamps.

Events accept an optional `title` of at most 8192 characters. Text candidates match event title, content and `why`. Cue candidates share entities, including nested parents, weighted by role and rarity. Recency decays from occurrence time, or capture time if undated, using the event type's half life. The latest rehearsal can raise recency above that baseline.

Scores combine relevance, cue and recency with default weights 0.45, 0.35 and 0.2. `use` can select `review`, `planning` or `incident` presets; `weights` can override those three signals. Limit defaults to 10 and is at most 100. `explain` returns individual signals, shared cues and their path. Results carry `id`, `record` and `score`. Unknown request fields are rejected.

A recall response contains event results:

```json
{
  "results": [
    {
      "id": "ev_meeting",
      "record": {"id": "ev_meeting", "bucket": "work", "type": "meeting"},
      "score": 0.8
    }
  ]
}
```


Current recalls rehearse only returned events, after scoring them. Explicit use also rehearses events. Counts belong to the calling principal within its workspace. Historical requests (`as_of` or context `at`) do not add rehearsals. Hidden IDs and duplicate IDs cannot add extra uses. Listing and reading by ID do not rehearse.

Valence, place and attribute filters are not implemented and return validation errors. Vector retrieval and graph spreading are not implemented.

## Event linking

Linking ranks candidate event pairs and returns a separate explanation for each matching approach. Scores and approach scores are between 0 and 1. Only readable events, entities, explicit links and intermediate thread events contribute evidence. Bucket filters narrow the events being linked. Historical recall also limits evidence to records captured by `as_of`.

| Approach | Query and score | Evidence in `because` |
|---|---|---|
| `thread` | Follow `part_of` and `in_reply_to` parent chains through at most 16 levels with visited tracking, plus direct children and siblings under the same parent. Score 1. Readable intermediate events may be outside the occurrence window. | `score`, `path` of event IDs from the first endpoint to the second |
| `subject` | Join shared subject and involved entity IDs. Reuse recall rarity `log((total+1)/frequency)`, where frequency counts distinct readable events sharing that subject or involved entity across the requested buckets, without occurrence or type filters. Subjects above `common_subject_share` (default 0.2, allowed range [0,1]) contribute zero and do not discover candidates. Sum rarity for distinct shared IDs, divide by `log(total+1)`, and cap at 1. | `score`, `shared` entity IDs |
| `people` | Join shared participants. Decay by the absolute occurrence gap with a seven day half life. Requires dated events. | `score`, `shared` participant IDs |
| `time` | Match the same readable place within 24 hours. Decay by the absolute occurrence gap with a three hour half life. Requires dated events. | `score`, `gap_seconds` |
| `text` | Search the existing content and `why` tsvector using any of the anchor's searchable words. Take the smaller of the two directional `ts_rank_cd` scores, normalized by rank/(rank+1). Postgres rank has no corpus rarity weighting, so text linking is off by default. | `score` |
| `explicit` | Match stored links in either direction. Score 1 regardless of link type. | `score`, `links` containing each stored `{id, from, to, type}` |

Default weights are `thread: 1`, `explicit: 1`, `subject: 0.9`, `meaning: 0.6`, `people: 0.4`, `text: 0.3`, `time: 0.2`. The combined score is noisy-OR: `1 - product(1 - weight * approach_score)` over approaches that fired. Adding evidence never lowers a score. Request weights must be within [0,1], with at least one positive enabled weight. `approaches` selects approaches; omitted enables everything except text. Unknown names produce a 422 field problem.

Each approach returns at most 200 candidates per anchor, retaining an extra candidate to detect a budget cut. `truncated` lists approaches whose budgets cut candidates. Subject candidates are ordered by rarest shared subject first, then nearest occurrence time. Other structural candidates use nearest occurrence time; text uses strongest rank. Ties use endpoint IDs. Zero score pairs are omitted.

`POST /v1/events/links` requires RFC 3339 `from` and `to`, inclusive and exclusive respectively. Optional `buckets` narrows access. `anchors_limit` defaults to 200, with a maximum of 1000. `after` continues after the anchor event ID from `next`. Paging follows occurrence time and ID. `next` is omitted when no anchors remain, even if the current page found no links. Candidate endpoints may be outside the anchor page but must be inside the requested window. `events` contains only returned link endpoints; `entities` contains their readable involved entities. Undirected pairs use `from < to` and are ranked strongest first. A pair can recur on another anchor page. `limit` caps links, defaults to 500 and is at most 10000. `min_score` is an inclusive threshold, default 0. Neither linking nor neighbour expansion rehearses the linked events.

```json
{
  "buckets": ["bk_shop"],
  "from": "2026-10-21T00:00:00Z",
  "to": "2026-10-24T00:00:00Z",
  "approaches": ["thread", "subject"],
  "weights": {"subject": 0.9},
  "anchors_limit": 200,
  "common_subject_share": 0.2,
  "min_score": 0.1,
  "limit": 500
}
```

Example response for a thread pair (event bodies abbreviated):

```json
{
  "events": [
    {"id":"ev_pr","bucket":"bk_shop","type":"github.pull_request","occurred_at":"2026-10-21T09:00:00Z"},
    {"id":"ev_review","bucket":"bk_shop","type":"github.pull_request.review","occurred_at":"2026-10-21T10:00:00Z","part_of":"ev_pr"}
  ],
  "entities": [],
  "links": [
    {"from":"ev_pr","to":"ev_review","score":1,
     "because":{"thread":{"score":1,"path":["ev_pr","ev_review"]}}}
  ]
}
```

Recall accepts `expand.neighbours` from 0 to 100 (default 0). A positive value adds up to that many ranked linked events to each result, using all approaches and default linking weights. Neighbours obey the recall's bucket, type, occurrence and historical filters. Their evidence is returned even when recall `explain` is false. Thread paths start at the recalled event. Type filters apply to returned endpoints; readable intermediate thread events of other types remain usable. Candidate selection is batched across all results with per anchor budgets and ranking, followed by one union read for neighbour records. A budget cut is reported in response `truncated`.

```json
{
  "context": {"subjects":["sb_retry_policy"]},
  "expand": {"neighbours":3}
}
```

Example expanded result:

```json
{
  "results": [
    {"id":"ev_pr","record":{"id":"ev_pr","bucket":"bk_shop","type":"github.pull_request"},"score":0.8,
     "neighbours":[
       {"id":"ev_review","record":{"id":"ev_review","bucket":"bk_shop","type":"github.pull_request.review","part_of":"ev_pr"},
        "score":1,
        "because":{"thread":{"score":1,"path":["ev_pr","ev_review"]}}}
     ]}
  ]
}
```

## Meaning and vector storage

`meaning` linking searches nearest event embeddings. Cosine similarity maps to `clamp((cosine - floor) / (1 - floor), 0, 1)`. The default floor is 0.6. `meaning_floor` overrides it per linking request; the value must be at least 0 and below 1. Evidence contains `{score, cosine, model}`. Recall embeds context text at read time and adds the `meaning` signal with default weight 0.6 when available. If disabled or unavailable, recall continues and lists `meaning` in `skipped`. Explain responses include `because.meaning`. Lexical recall remains enabled independently of text linking.

Set `MEMORY_EMBEDDING_URL`, `MEMORY_EMBEDDING_KEY`, `MEMORY_EMBEDDING_MODEL` and `MEMORY_EMBEDDING_DIMENSIONS` to enable embeddings. The URL is an OpenAI compatible base URL; the client posts to its `/embeddings` endpoint with model, input array and dimensions. Response vectors follow data order, including responses without index or usage. Count and dimensions must match. The key is never logged. An unset URL disables meaning without requiring pgvector. `MEMORY_EMBEDDING_FLOOR` configures the default floor.

The `Embedder` interface exposes model, dimensions and batch embedding. `VectorIndex` exposes upsert, nearest and delete using engine values: workspace ID, event ID, bucket ID, occurrence time, record type, model, vectors and plain filters. Neither SQL nor shared transactions cross this boundary. The optional `BatchVectorIndex` batches nearest searches; both bundled backends implement it, and Postgres executes them in one candidate query. `MEMORY_VECTOR_STORE` selects `postgres` (default) or `memory`. Another backend, such as MariaDB, implements the same interface and constructor selection, then runs the reusable vector conformance suite. MariaDB is not implemented here.

The vector backend prefilters readable bucket IDs for efficiency. The engine over-fetches a bounded candidate pool and rechecks every returned ID against Postgres access and scope rules before scoring. Vector results never grant access. Extremely selective access requirements may return fewer than k results after this recheck.

The Postgres backend initializes the extension and vector table idempotently at startup only when embeddings are configured. It requires pgvector 0.8 or later, supports 1 to 2000 dimensions using `vector`, and indexes cosine distance with HNSW. Model and dimensions are stored per vector; queries ignore other spaces. Approximate searches use transaction-local iterative relaxed scans with a scan tuple budget of 1000000 and exactly re-sort the selected candidates by cosine distance.

Event capture atomically queues an embedding job without calling the provider. The memoryd worker reads committed events in batches of 32, using title, content and why. It writes vectors after commit, retries failures with exponential backoff capped at one hour, and marks successful jobs complete. Upserts are idempotent, and persistent pending jobs repair interrupted work. A model or dimension change requeues events at startup. The memory backend requeues all events after restart. Enabled health responses include `embedding_backlog`; disabled health responses retain status 204.

Example meaning request:

```json
{"from":"2026-10-21T00:00:00Z","to":"2026-10-22T00:00:00Z","anchors_limit":200,"approaches":["meaning"],"meaning_floor":0.6}
```

Example evidence:

```json
{"from":"ev_auth_a","to":"ev_auth_b","score":0.3,"because":{"meaning":{"score":0.5,"cosine":0.8,"model":"gemini-embedding-001"}}}
```

## Access

A workspace isolates IDs, source identities and records. A bucket groups records under one access rule. A deleted bucket stays registered and becomes unreadable.

Every record stores a requirement set. Entities and events require their own bucket. Appraisals add the target's requirements; links add both endpoints' requirements. A caller must read every required bucket. Queries apply this rule before counting, scoring, pagination or walking entity parents. Returned references to unreadable or missing records are removed.

Services authenticate with operator issued keys; only key hashes are stored. Credentials limit actions to `read`, `write` and `sync`. Operators create workspaces, services, trusted issuers and grants with `memoryd`. Installations restrict which source containers a service can bind. Binding grants that service access to its bucket. A service with no grants reads nothing.

A client application can be a trusted issuer for people. Person tokens are [JSON Web Tokens (RFC 7519)](https://www.rfc-editor.org/rfc/rfc7519) and use Ed25519 signatures ([RFC 8032 section 5.1](https://www.rfc-editor.org/rfc/rfc8032#section-5.1)). Tokens must match the configured audience, workspace, issuer and service, with a valid expiry and bounded lifetime. Proven source accounts link to one principal. Synced source readers expire without refresh. Manual grants remain until revoked. Actor identities are descriptive and never serve as credentials.

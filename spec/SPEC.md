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

A source may contain `system`, `installation`, `native_id`, `url`, `recorded_at`, `method` and `by` (an actor ID). The workspace, system, installation and native ID identify one captured record. Repeated capture resolves to the stored ID and rewrites references within the batch.

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

Text candidates match event content and `why`. Cue candidates share entities, including nested parents, weighted by role and rarity. Recency decays from occurrence time, or capture time if undated, using the event type's half life. The latest rehearsal can raise recency above that baseline.

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

Valence, place and attribute filters, and nonzero neighbour expansion, are not implemented and return validation errors. Vector retrieval and graph spreading are not implemented.

## Access

A workspace isolates IDs, source identities and records. A bucket groups records under one access rule. A deleted bucket stays registered and becomes unreadable.

Every record stores a requirement set. Entities and events require their own bucket. Appraisals add the target's requirements; links add both endpoints' requirements. A caller must read every required bucket. Queries apply this rule before counting, scoring, pagination or walking entity parents. Returned references to unreadable or missing records are removed.

Services authenticate with operator issued keys; only key hashes are stored. Credentials limit actions to `read`, `write` and `sync`. Operators create workspaces, services, trusted issuers and grants with `memoryd`. Installations restrict which source containers a service can bind. Binding grants that service access to its bucket. A service with no grants reads nothing.

A client application can be a trusted issuer for people. Person tokens are [JSON Web Tokens (RFC 7519)](https://www.rfc-editor.org/rfc/rfc7519) and use Ed25519 signatures ([RFC 8032 section 5.1](https://www.rfc-editor.org/rfc/rfc8032#section-5.1)). Tokens must match the configured audience, workspace, issuer and service, with a valid expiry and bounded lifetime. Proven source accounts link to one principal. Synced source readers expire without refresh. Manual grants remain until revoked. Actor identities are descriptive and never serve as credentials.

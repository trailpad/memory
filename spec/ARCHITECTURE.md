# Architecture

The Go service stores episodic events in PostgreSQL and exposes JSON over HTTP. A client application owns semantic facts and writes events describing what happened to them.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/memoryd` | HTTP service and operator commands for workspaces, services, issuers and grants |
| `internal/api/http` | Authentication, request decoding, HTTP responses and event listings |
| `internal/capture` | Record validation, profile validation and reference checks |
| `internal/record` | Events, entities, appraisals, links, sources and field validation |
| `internal/profile` | Versioned vocabularies, fallback types, role weights and event decay |
| `internal/store` | Persistence and candidate retrieval contracts |
| `internal/store/postgres` | Transactions, access predicates, migrations and search queries |
| `internal/recall` | Candidate selection, relevance, cues, recency and explanation |
| `spec/schema` | Embedded JSON Schemas shared with clients |

HTTP calls capture and recall; these depend on store interfaces. PostgreSQL implements those interfaces. Ranking has no HTTP dependency.

## Storage

| Table | Holds |
|---|---|
| `events` | Immutable event bodies, occurrence time, reason, type lineage and full text index |
| `entities` | Places, actors, subjects and extension entities |
| `record_ids` | Workspace scoped ownership of every record ID |
| `involvements` | Event ties to places, participants, subjects and other entities |
| `appraisals` | Immutable judgements targeting records |
| `links` | Immutable typed relations between records |
| `rehearsals` | Event uses with principal, use time and recorded time |
| `profiles` | Versioned vocabularies |
| `identities` | Actor handles on source platforms |
| `workspaces`, `buckets` | Isolation and record access boundaries |
| `principals`, `services`, `installations`, `issuers` | Authenticated callers and trusted sources |
| `subjects`, `account_links` | Issuer subjects and proven source accounts |
| `grants`, `synced_readers`, `access_log` | Access grants, expiring readers and access changes |

Every record carries a workspace, bucket and requirement set. Queries require that the caller can read every bucket in that set. Appraisals inherit target requirements; links inherit both endpoint requirements. Hidden records never enter candidate counts or scores, and hidden references are stripped from read responses.

Migration 0005 drops the semantic record tables, removes their involvement and rehearsal rows, restricts record categories to the remaining records, attaches rehearsals to events, and adds the bounded event reason column. Rollback recreates the former tables and indexes but cannot recover deleted data or uses. Earlier migrations are unchanged.

## Write path

Capture validates fields and profiles, checks referenced record types, then submits one transaction. The store checks writable buckets, takes ordered advisory locks, resolves source duplicates, rewrites references, computes requirements and writes the records. Repeating an unchanged event is harmless; changing an event under the same ID is refused.

An event's `why` is stored both in its JSON body and a bounded text column. Its search vector combines `why` with utterance text. Event bodies exclude derived appraisals and rehearsal counts. Appraisals are written separately and joined on reads, so a later judgement can attach without changing the event.

## Read and recall path

Direct event reads and listings load readable appraisals by target ID. Appraisals are also attached to recall results, respecting `as_of`. Listings return involved entities so clients can display participants and places without individual requests.

Recall finds lexical and entity cue candidates under the same access rule. Parent walks stop at unreadable entities and have bounded depth. Rarity and role weights determine cue strength. Relevance scales text matches against the best candidate. Recency decays from event occurrence time, with an event type specific half life and the latest eligible rehearsal.

Results are scored, sorted and limited before current recalls write rehearsal rows. Explicit event use writes the same rows. Historical requests only read them. Rehearsals are scoped to a workspace and principal, so one caller's use never strengthens another caller's events.

The implementation supports lexical retrieval, cue matching, recency, explanations and profile fallbacks. Vector retrieval, graph spreading and separate appraisal ranking signals remain future work.

## Running and testing

`compose.yaml` runs the service and PostgreSQL. `memoryd` applies pending migrations on startup. The operator commands configure authentication and access before clients write records.

HTTP tests exercise writes, listings, direct reads, recall, use and permissions through the public boundary. Storage tests isolate each database test in a fresh schema and check migration rollback and reapplication. Record and profile tests check the published schemas and examples.

# Architecture

Draft 0.1. How a reference implementation of the Memory spec is laid out, written in Go.

## 1. Shape

One service, one database, three jobs.

```
                 ┌──────────────────────────────────────────────┐
  capture  ───►  │  write      events, memories, places, actors, │
  (webhooks,     │             subjects, appraisals, links       │
  importers,     │                                               │
  extractors)    │  recall     context in, ranked and explained  │ ◄───  apps, agents,
                 │             records out                       │        timelines
                 │                                               │
                 │  maintain   decay, identity joins, conflicts, │
                 │             re-embedding, graph refresh       │
                 └───────────────────────┬──────────────────────┘
                                         │
                                ┌────────▼────────┐
                                │   PostgreSQL    │
                                │  rows, text     │
                                │  index, vectors │
                                │  (pgvector),    │
                                │  edges          │
                                └─────────────────┘
```

Constraints that drive it:

- **Events are append only.** A correction is a new event or a new appraisal, never an update. The write path is inserts.
- **Recall is the product.** Everything else exists to make recall fast, correct and explainable.
- **Access is applied inside the query**, never by filtering results afterwards, so counts, scores and pages never leak a record the caller cannot see.
- **One database to start.** PostgreSQL holds rows, full text search, vectors (pgvector, HNSW since 0.5.0) and the cue graph as an edge table. A separate graph or vector database is added only when a measured query needs it.
- **Extraction is a client, not part of the core.** A model that turns events into memories calls the same write API as any other client, so the store never depends on a model provider.

## 2. Repository layout

One Go module, `github.com/trailpad/memory`. Implementation lives under `internal/`, so the only things other code can import are the client and the spec's schemas; the engine's insides can change without breaking anyone.

```
memory/
├── spec/
│   ├── SPEC.md                  the record format and recall contract
│   ├── ARCHITECTURE.md          this document
│   └── schema/                  JSON Schema for every record, profile and recall;
│       ├── event.json             the spec in a form other languages can check
│       ├── memory.json
│       ├── entity.json            places, actors, subjects and declared kinds
│       ├── appraisal.json
│       ├── link.json
│       ├── profile.json
│       └── recall.json
│
├── cmd/
│   ├── memoryd/                 the service
│   └── memory/                  command line client for querying and administration
│
├── pkg/
│   └── client/                  the Go client; other languages generated from openapi
│
├── internal/
│   ├── record/                  the records as Go types; depends on nothing
│   │   ├── types.go             Event, Memory, Place, Actor, Subject, Entity,
│   │   │                        Appraisal, Link, Source
│   │   ├── kind.go              namespaced types and their fallback chain
│   │   ├── role.go              roles held by participants and involved entities
│   │   ├── attribute.go         namespaced values on records and entities
│   │   ├── time.go              instants with precision, the two clocks
│   │   └── validate.go          the rules the spec states
│   │
│   ├── profile/                 vocabularies for other fields (spec §11)
│   │   ├── core.json            the spec's own types and roles, as a profile
│   │   ├── registry.go          register and version profiles
│   │   └── resolve.go           type fallback, role cue weights, decay per type
│   │
│   ├── store/                   persistence behind one interface
│   │   ├── store.go             Put, Get, Link, Candidates
│   │   ├── memstore/            in memory, for tests and the conformance suite
│   │   └── postgres/
│   │       ├── migrations/
│   │       ├── write.go         idempotent on source system + native id
│   │       ├── read.go
│   │       ├── candidates.go    the SQL that finds recall candidates (§4)
│   │       └── index.go         indexes for attributes a profile marks filterable
│   │
│   ├── capture/                 the write path
│   │   ├── ingest.go            resolve references, dedupe
│   │   ├── identity.go          join actors across platforms, as recorded claims
│   │   └── activitystreams.go   map Activity Vocabulary records in
│   │
│   ├── appraise/
│   │   └── reduce.go            many appraisals to one effective appraisal
│   │
│   ├── recall/                  the recall pipeline (§3)
│   │   ├── request.go
│   │   ├── plan.go
│   │   ├── candidates.go
│   │   ├── spread.go            personalised PageRank from the context's cues
│   │   ├── rank.go              normalise, weigh, sort; weight presets
│   │   ├── expand.go            evidence events, quotes, place paths
│   │   ├── explain.go           the "because" block on each result
│   │   ├── rehearse.go          write back recall count when a result is used
│   │   └── signals/
│   │       ├── relevance.go     lexical and semantic, fused by rank
│   │       ├── cue.go           shared entities, weighted by rarity and role
│   │       ├── recency.go       decay per memory type, rehearsal
│   │       ├── importance.go
│   │       ├── affect.go
│   │       ├── authority.go     place formality and participants' roles
│   │       └── validity.go      contradicted but not retired
│   │
│   ├── access/
│   │   └── audience.go          who may see what, as SQL predicates
│   │
│   ├── embed/
│   │   └── embed.go             Embedder interface; providers are adapters,
│   │                            none required to run
│   │
│   ├── maintain/
│   │   ├── decay.go
│   │   ├── conflicts.go
│   │   ├── reembed.go
│   │   └── graph.go             cue rarity counts and the spreading graph
│   │
│   └── api/
│       ├── http/                JSON over HTTP, routes mirror the spec
│       └── openapi.yaml
│
└── conformance/                 the spec's acceptance tests as data
    ├── fixtures/                a fixed world of entities, events and memories
    ├── questions/               the recall questions with expected results
    └── run.go                   runs any implementation against them over HTTP
```

Packages depend only on packages below them:

```
cmd ─► api ─► capture, recall, maintain ─► appraise, access, embed, profile ─► store ─► record
```

`record` depends on nothing. `store` knows SQL but not ranking. `recall` knows ranking but not HTTP.

## 3. Recall pipeline

```
 request
    │
    ▼
 parse + defaults ──── weights preset (review, planning, incident, ...)
    │
    ▼
 plan ─────────────── which candidate sources this request needs
    │
    ▼
 candidates (parallel, each already limited by access and filters)
    ├── lexical        full text match on statement, why, content
    ├── semantic       nearest vectors
    ├── cue            records sharing the context's places, actors, subjects
    │                  (at every level of nesting)
    └── spread         personalised PageRank seeded by the same cues
    │
    ▼
 merge ────────────── one row per record, each source's rank kept
    │
    ▼
 signals ──────────── relevance, cue, recency, importance, affect,
    │                 authority, validity, each 0..1
    ▼
 rank ─────────────── weighted sum, sort, limit
    │
    ▼
 expand ───────────── evidence events, quotes, place paths, neighbours
    │
    ▼
 explain ──────────── per result: signals, shared cues, path reached by
    │
    ▼
 response
```

Every stage is a plain function over a slice of candidates, so each can be tested alone and swapped without touching the others.

## 4. Storage

Tables (one per record kind) plus the joins that make cues queryable:

| Table | Holds | Indexes |
|---|---|---|
| `record_ids` | every id, with its record kind and entity class | `id` primary key |
| `events` | events, append only | `occurred_at`, `place_id`, `part_of`, full text on content |
| `memories` | memories, one row per version | `valid_from`, `valid_until`, `recorded_at`, full text, vector |
| `entities` | places, actors, subjects and every declared kind, one table | `parent_id`, `type` |
| `involvements` | record, version, entity, role (places, participants, subjects and `involves` alike) | `(entity_id, role)`, `(record_id)` |
| `profiles` | declared vocabularies, versioned | `(name, version)` |
| `evidence` | memory, version, event, role | `(event_id)`, `(memory_id)` |
| `appraisals` | judgements | `(target_id, at)`, source unique |
| `links` | typed relations | `(from_id, type)`, `(to_id, type)`, source unique |
| `cue_counts` | how many records each cue touches | refreshed by `maintain/graph.go`, used for rarity |
| `identities` | platform handles to actors | `(platform, handle)` unique |

Attributes are a JSONB column on records and entities. When a profile marks an attribute filterable, the engine creates an expression index for that key, so a new field becomes fast to filter on without a code change.

Nesting (place inside place, subject inside subject) is stored as a parent column and queried with recursive CTEs. If those get slow, a materialised path column replaces them.

An id names exactly one record. `record_ids` enforces it across tables, so a reference never needs to say which table it points into, and capture can check that each reference points at the kind its field asks for (a `by` at an actor, a `place` at a place).

Both clocks are columns on `memories`: `valid_from` / `valid_until` for the world, `recorded_at` / `superseded_at` for the store. `as_of` recall filters on the second pair. A memory's involvements and evidence are kept per version, so an earlier version keeps the people and events it was tied to.

## 5. Write path

```
 client ─► api ─► capture.ingest
                    ├── validate (record, profile attributes and dimensions)
                    ├── check references (present, and of the right kind)
                    └── store.Write, one transaction
                          ├── lock every id and source the batch touches
                          ├── dedupe on source system + native id
                          ├── rewrite references to the stored ids
                          ├── write
                          └── queue: embed, cue counts
```

Locks are taken in sorted order, so two batches touching the same records wait for each other rather than deadlocking or racing to insert the same source twice. Deduplication finishes before any reference is rewritten, so the result does not depend on the order of records in a batch.

Writes return once the transaction commits. Embedding and cue counts follow shortly after, and recall works without them (the lexical and cue sources still answer).

## 6. How an application uses it

```
 application                         memory service
 ───────────                         ──────────────
 its own objects (what it shows,
 edits, reviews) ─── write ───────►  events + memories
                                     (application keeps its ids in source.native_id)

 a screen or agent needs context
 ─── recall(context) ─────────────►  ranked, explained records
 ◄──────────────────────────────────
 result used ─── rehearse(id) ────►  recall count, last recalled
```

The application keeps its own model and screens. The memory service is where its events and memories are also written, and where it asks what to bring back.

## 7. Testing

- **record, appraise, recall/signals, recall/rank**: unit tests on plain values.
- **store/postgres**: tests against a real PostgreSQL in a container, migrations up and down.
- **api**: tests over HTTP, the same boundary clients use.
- **conformance**: the spec's eight questions against a fixed fixture world. A change that alters an expected answer changes the spec, and is reviewed as one.
- **recall quality**: a held out set of real (anonymised) contexts and the records a person said were right, scored as recall at 10 and mean reciprocal rank, run before any change to weights or signals.

## 8. Order of work

1. `record`, `profile` (with the core profile) and `spec/schema`: types and JSON Schema agree on the spec's examples; validation.
2. `store/postgres` with migrations; `capture` without embeddings.
3. `recall` with lexical and cue sources only, `explain` from day one.
4. `conformance` fixtures and the first four questions.
5. `embed` and the semantic source.
6. `spread` (personalised PageRank).
7. `maintain`, `access` hardened, the remaining questions.
8. Clients.

Each step ships something that answers questions, so recall quality can be measured from step 3 on.

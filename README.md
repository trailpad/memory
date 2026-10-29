<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/banner-dark.png">
  <img alt="Trailpad Memory" src="docs/brand/banner-light.png" width="880">
</picture>

Memory is the engine Trailpad uses to remember what a team did and decided, and to bring the right part of it back when someone needs it.

It keeps two kinds of record. An **event** is something that happened once: a review comment, a message in a thread, a meeting. A **memory** is something learned from events that holds until it does not: "retries to the payment gateway stop after three attempts". Every memory points at the events it came from, so any answer can be traced back to where it was said.

Recall starts from what someone is looking at (a file, a pull request, a channel, a few words) and returns the memories and events that matter most, each with the reason it came back.

Every record lives in a bucket, and nobody reads a record without being allowed to read everything it was made from. Being in a meeting does not make someone a reader of its notes.

## Status

Pre-release. The record format, capture, recall and the storage side of access are in place. The service that serves it over HTTP, the access package that turns sign-ins and grants into permissions, and semantic search come next, in the order set out in the architecture.

## Documents

| Document | What it covers |
|---|---|
| [spec/SPEC.md](spec/SPEC.md) | The record format, how recall works, and who may read what |
| [spec/ARCHITECTURE.md](spec/ARCHITECTURE.md) | How the engine is built, and the order of work |
| [spec/schema](spec/schema) | JSON Schema for every record |
| [spec/examples](spec/examples) | Example records, checked against the schema and the Go types |

## Running the tests

Go 1.26 and a PostgreSQL 16 database. Each test runs in a schema of its own and drops it afterwards.

```sh
export MEMORY_TEST_DATABASE_URL=postgres://memory:memory@localhost:5432/memory_test?sslmode=disable
go test ./...
```

Without the variable, the storage tests are skipped. Set `MEMORY_REQUIRE_DATABASE=1` to make a missing database fail instead, as CI does.

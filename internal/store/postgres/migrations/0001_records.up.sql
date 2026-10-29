create extension if not exists pg_trgm with schema public;

-- Every id belongs to one record of one kind, so a reference can name any
-- record without saying which table it lives in.
create table record_ids (
  id     text primary key,
  kind   text not null check (kind in ('entity', 'event', 'memory', 'appraisal', 'link')),
  class  text check (class in ('place', 'actor', 'subject'))
);

create table entities (
  id          text primary key references record_ids (id),
  class       text check (class in ('place', 'actor', 'subject')),
  type        text not null,
  name        text,
  parent_id   text,
  body        jsonb not null,
  source_system    text,
  source_native_id text,
  recorded_at timestamptz not null
);
create index entities_parent on entities (parent_id);
create index entities_type on entities (type);
create unique index entities_source on entities (source_system, source_native_id) where source_native_id is not null;

create table identities (
  platform  text not null,
  handle    text not null,
  actor_id  text not null references entities (id),
  primary key (platform, handle)
);
create index identities_actor on identities (actor_id);

create table events (
  id           text primary key references record_ids (id),
  type         text not null,
  occurred_at  timestamptz,
  occurred_precision text,
  ended_at     timestamptz,
  place_id     text references entities (id),
  part_of      text,
  in_reply_to  text,
  body         jsonb not null,
  said         tsvector not null,
  source_system    text,
  source_native_id text,
  recorded_at  timestamptz not null
);
create index events_occurred on events (occurred_at);
create index events_place on events (place_id);
create index events_part_of on events (part_of);
create index events_said on events using gin (said);
create unique index events_source on events (source_system, source_native_id) where source_native_id is not null;

-- One row per version of a memory. The current version has no superseded_at.
create table memories (
  id            text not null references record_ids (id),
  version       integer not null,
  type          text not null,
  statement     text not null,
  why           text,
  valid_from    timestamptz,
  valid_until   timestamptz,
  confidence    real,
  body          jsonb not null,
  said          tsvector not null,
  recall_count  integer not null default 0,
  last_recalled_at timestamptz,
  source_system    text,
  source_native_id text,
  recorded_at   timestamptz not null,
  superseded_at timestamptz,
  primary key (id, version)
);
create unique index memories_current on memories (id) where superseded_at is null;
create index memories_valid on memories (valid_from, valid_until);
create index memories_said on memories using gin (said);
create index memories_statement_trgm on memories using gin (statement gin_trgm_ops);
create unique index memories_source on memories (source_system, source_native_id) where source_native_id is not null and superseded_at is null;

-- Every tie from a record to an entity: its place, participants, subjects and
-- anything else it involves. Recall treats them alike. Memory rows carry the
-- version they belong to, so earlier versions keep their ties; events are 0.
create table involvements (
  record_kind text not null check (record_kind in ('event', 'memory')),
  record_id   text not null,
  version     integer not null default 0,
  entity_id   text not null references entities (id),
  via         text not null check (via in ('place', 'participant', 'subject', 'involves')),
  role        text not null,
  primary key (record_kind, record_id, version, entity_id, via, role)
);
create index involvements_entity on involvements (entity_id, role);

create table evidence (
  memory_id text not null,
  version   integer not null,
  event_id  text not null references events (id),
  role      text not null,
  primary key (memory_id, version, event_id, role)
);
create index evidence_event on evidence (event_id);

create table appraisals (
  id          text primary key references record_ids (id),
  target_id   text not null,
  by_id       text not null,
  at          timestamptz,
  valence     smallint check (valence between -2 and 2),
  intensity   smallint check (intensity between 1 and 5),
  importance  smallint check (importance between 1 and 5),
  surprise    smallint check (surprise between 1 and 5),
  confidence  real,
  body        jsonb not null,
  source_system    text,
  source_native_id text,
  recorded_at timestamptz not null
);
create index appraisals_target on appraisals (target_id, at);
create unique index appraisals_source on appraisals (source_system, source_native_id) where source_native_id is not null;

create table links (
  id        text primary key references record_ids (id),
  from_id   text not null,
  type      text not null,
  to_id     text not null,
  at        timestamptz,
  body      jsonb not null,
  source_system    text,
  source_native_id text,
  recorded_at timestamptz not null
);
create index links_from on links (from_id, type);
create index links_to on links (to_id, type);
create unique index links_source on links (source_system, source_native_id) where source_native_id is not null;

create table profiles (
  name    text not null,
  version text not null,
  body    jsonb not null,
  recorded_at timestamptz not null default now(),
  primary key (name, version)
);

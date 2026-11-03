alter table events drop column why;
alter table record_ids drop constraint record_ids_kind_check;
alter table record_ids add constraint record_ids_kind_check check (kind in ('entity', 'event', 'memory', 'appraisal', 'link'));
alter table involvements drop constraint involvements_record_kind_check;
alter table involvements add constraint involvements_record_kind_check check (record_kind in ('event', 'memory'));
delete from rehearsals;
alter table rehearsals drop constraint rehearsals_workspace_id_event_id_fkey;
alter table rehearsals rename column event_id to memory_id;
alter table rehearsals add foreign key (workspace_id, memory_id) references record_ids (workspace_id, id);
drop index rehearsals_event;
create index rehearsals_memory on rehearsals (workspace_id, memory_id, principal, recorded_at);

create table memories (
  workspace_id text not null,
  id text not null,
  version integer not null,
  type text not null,
  statement text not null,
  why text,
  valid_from timestamptz,
  valid_until timestamptz,
  confidence real,
  body jsonb not null,
  said tsvector not null,
  source_system text,
  source_installation text,
  source_native_id text,
  recorded_at timestamptz not null,
  superseded_at timestamptz,
  bucket_id text not null,
  requires text[] not null,
  type_lineage text[] not null default '{}',
  primary key (workspace_id, id, version),
  foreign key (workspace_id, id) references record_ids (workspace_id, id),
  foreign key (workspace_id, bucket_id) references buckets (workspace_id, id)
);
create unique index memories_current on memories (workspace_id, id) where superseded_at is null;
create index memories_valid on memories (workspace_id, valid_from, valid_until);
create index memories_said on memories using gin (said);
create index memories_statement_trgm on memories using gin (statement gin_trgm_ops);
create unique index memories_source on memories (workspace_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null and superseded_at is null;
create index memories_type_lineage on memories using gin (type_lineage);
create index memories_requires on memories using gin (requires);

create table evidence (
  workspace_id text not null,
  memory_id text not null,
  version integer not null,
  event_id text not null,
  role text not null,
  primary key (workspace_id, memory_id, version, event_id, role),
  foreign key (workspace_id, event_id) references events (workspace_id, id)
);
create index evidence_event on evidence (workspace_id, event_id);

create table dependencies (
  workspace_id text not null,
  memory_id text not null,
  version integer not null,
  input_id text not null,
  input_version integer not null default 0,
  primary key (workspace_id, memory_id, version, input_id, input_version),
  foreign key (workspace_id, memory_id) references record_ids (workspace_id, id),
  foreign key (workspace_id, input_id) references record_ids (workspace_id, id)
);
create index dependencies_input on dependencies (workspace_id, input_id);

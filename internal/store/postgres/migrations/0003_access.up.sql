create table workspaces (
  id         text primary key,
  external   jsonb,
  created_at timestamptz not null default now()
);

-- A bucket keeps its row when deleted, so its id is never reused and
-- anything that required it stays unreadable.
create table buckets (
  workspace_id text not null references workspaces (id),
  id           text not null,
  name         text,
  system       text,
  installation text,
  container    text,
  quarantine   boolean not null default false,
  created_at   timestamptz not null default now(),
  deleted_at   timestamptz,
  primary key (workspace_id, id),
  check ((system is null and installation is null and container is null)
      or (system is not null and installation is not null and container is not null)),
  check (not (quarantine and system is not null))
);
create unique index buckets_binding on buckets (workspace_id, system, installation, container) where system is not null;
create unique index buckets_quarantine on buckets (workspace_id) where quarantine;

-- Records written before workspaces existed have no audience anyone
-- established, so they go to the quarantine of one workspace.
insert into workspaces (id) values ('default');
insert into buckets (workspace_id, id, name, quarantine) values ('default', 'quarantine', 'Quarantine', true);

alter table entities drop constraint entities_id_fkey;
alter table identities drop constraint identities_actor_id_fkey;
alter table events drop constraint events_id_fkey, drop constraint events_place_id_fkey;
alter table memories drop constraint memories_id_fkey;
alter table involvements drop constraint involvements_entity_id_fkey;
alter table evidence drop constraint evidence_event_id_fkey;
alter table appraisals drop constraint appraisals_id_fkey;
alter table links drop constraint links_id_fkey;
alter table rehearsals drop constraint rehearsals_memory_id_fkey;

alter table record_ids add column workspace_id text not null default 'default';
alter table entities add column workspace_id text not null default 'default';
alter table identities add column workspace_id text not null default 'default';
alter table events add column workspace_id text not null default 'default';
alter table memories add column workspace_id text not null default 'default';
alter table involvements add column workspace_id text not null default 'default';
alter table evidence add column workspace_id text not null default 'default';
alter table appraisals add column workspace_id text not null default 'default';
alter table links add column workspace_id text not null default 'default';
alter table rehearsals add column workspace_id text not null default 'default';

-- Uses made before uses were kept per principal belong to nobody.
alter table rehearsals add column principal text not null default '';

do $$
declare t text;
begin
  foreach t in array array['record_ids', 'entities', 'identities', 'events', 'memories', 'involvements', 'evidence', 'appraisals', 'links', 'rehearsals'] loop
    execute format('alter table %I alter column workspace_id drop default', t);
  end loop;
  -- A record's bucket and requirement set: the buckets someone must be
  -- able to read to read it.
  foreach t in array array['entities', 'events', 'memories', 'appraisals', 'links'] loop
    execute format('alter table %I add column bucket_id text not null default ''quarantine''', t);
    execute format('alter table %I alter column bucket_id drop default', t);
    execute format('alter table %I add column requires text[] not null default ''{quarantine}''', t);
    execute format('alter table %I alter column requires drop default', t);
    execute format('alter table %I add column source_installation text', t);
  end loop;
end $$;
alter table rehearsals alter column principal drop default;

-- Bodies are what reads return and what a recapture is compared with, so
-- they name the bucket too.
update entities set body = jsonb_set(body, '{bucket}', '"quarantine"');
update events set body = jsonb_set(body, '{bucket}', '"quarantine"');
update memories set body = jsonb_set(body, '{bucket}', '"quarantine"');
update appraisals set body = jsonb_set(body, '{bucket}', '"quarantine"');
update links set body = jsonb_set(body, '{bucket}', '"quarantine"');

alter table record_ids drop constraint record_ids_pkey, add primary key (workspace_id, id),
  add foreign key (workspace_id) references workspaces (id);
alter table entities drop constraint entities_pkey, add primary key (workspace_id, id),
  add foreign key (workspace_id, id) references record_ids (workspace_id, id),
  add foreign key (workspace_id, bucket_id) references buckets (workspace_id, id);
alter table identities drop constraint identities_pkey, add primary key (workspace_id, platform, handle),
  add foreign key (workspace_id, actor_id) references entities (workspace_id, id);
alter table events drop constraint events_pkey, add primary key (workspace_id, id),
  add foreign key (workspace_id, id) references record_ids (workspace_id, id),
  add foreign key (workspace_id, place_id) references entities (workspace_id, id),
  add foreign key (workspace_id, bucket_id) references buckets (workspace_id, id);
alter table memories drop constraint memories_pkey, add primary key (workspace_id, id, version),
  add foreign key (workspace_id, id) references record_ids (workspace_id, id),
  add foreign key (workspace_id, bucket_id) references buckets (workspace_id, id);
alter table involvements drop constraint involvements_pkey,
  add primary key (workspace_id, record_kind, record_id, version, entity_id, via, role),
  add foreign key (workspace_id, entity_id) references entities (workspace_id, id);
alter table evidence drop constraint evidence_pkey, add primary key (workspace_id, memory_id, version, event_id, role),
  add foreign key (workspace_id, event_id) references events (workspace_id, id);
alter table appraisals drop constraint appraisals_pkey, add primary key (workspace_id, id),
  add foreign key (workspace_id, id) references record_ids (workspace_id, id),
  add foreign key (workspace_id, bucket_id) references buckets (workspace_id, id);
alter table links drop constraint links_pkey, add primary key (workspace_id, id),
  add foreign key (workspace_id, id) references record_ids (workspace_id, id),
  add foreign key (workspace_id, bucket_id) references buckets (workspace_id, id);
alter table rehearsals add foreign key (workspace_id, memory_id) references record_ids (workspace_id, id);

drop index entities_parent, entities_type, entities_source, identities_actor,
  events_occurred, events_place, events_part_of, events_source,
  memories_current, memories_valid, memories_source,
  involvements_entity, evidence_event, appraisals_target, appraisals_source,
  links_from, links_to, links_source, rehearsals_memory;

create index entities_parent on entities (workspace_id, parent_id);
create index entities_type on entities (workspace_id, type);
create unique index entities_source on entities (workspace_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null;
create index identities_actor on identities (workspace_id, actor_id);
create index events_occurred on events (workspace_id, occurred_at);
create index events_place on events (workspace_id, place_id);
create index events_part_of on events (workspace_id, part_of);
create unique index events_source on events (workspace_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null;
create unique index memories_current on memories (workspace_id, id) where superseded_at is null;
create index memories_valid on memories (workspace_id, valid_from, valid_until);
create unique index memories_source on memories (workspace_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null and superseded_at is null;
create index involvements_entity on involvements (workspace_id, entity_id, role);
create index evidence_event on evidence (workspace_id, event_id);
create index appraisals_target on appraisals (workspace_id, target_id, at);
create unique index appraisals_source on appraisals (workspace_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null;
create index links_from on links (workspace_id, from_id, type);
create index links_to on links (workspace_id, to_id, type);
create unique index links_source on links (workspace_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null;
create index rehearsals_memory on rehearsals (workspace_id, memory_id, principal, recorded_at);

create index entities_requires on entities using gin (requires);
create index events_requires on events using gin (requires);
create index memories_requires on memories using gin (requires);
create index appraisals_requires on appraisals using gin (requires);
create index links_requires on links using gin (requires);

-- What each memory version was made from, as written. Requirement sets are
-- worked out from these when the version is written and never recomputed.
create table dependencies (
  workspace_id  text not null,
  memory_id     text not null,
  version       integer not null,
  input_id      text not null,
  input_version integer not null default 0,
  primary key (workspace_id, memory_id, version, input_id, input_version),
  foreign key (workspace_id, memory_id) references record_ids (workspace_id, id),
  foreign key (workspace_id, input_id) references record_ids (workspace_id, id)
);
create index dependencies_input on dependencies (workspace_id, input_id);

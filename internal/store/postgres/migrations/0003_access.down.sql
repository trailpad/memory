drop table dependencies;

drop index entities_requires, events_requires, memories_requires, appraisals_requires, links_requires;
drop index entities_parent, entities_type, entities_source, identities_actor,
  events_occurred, events_place, events_part_of, events_source,
  memories_current, memories_valid, memories_source,
  involvements_entity, evidence_event, appraisals_target, appraisals_source,
  links_from, links_to, links_source, rehearsals_memory;

alter table rehearsals drop constraint rehearsals_workspace_id_memory_id_fkey;
alter table identities drop constraint identities_workspace_id_actor_id_fkey;
alter table involvements drop constraint involvements_workspace_id_entity_id_fkey;
alter table evidence drop constraint evidence_workspace_id_event_id_fkey;
alter table events drop constraint events_workspace_id_place_id_fkey;
alter table entities drop constraint entities_workspace_id_id_fkey;
alter table events drop constraint events_workspace_id_id_fkey;
alter table memories drop constraint memories_workspace_id_id_fkey;
alter table appraisals drop constraint appraisals_workspace_id_id_fkey;
alter table links drop constraint links_workspace_id_id_fkey;

-- Ids are only unique within a workspace, so this fails rather than merge
-- records when more than one workspace holds the same id.
alter table record_ids drop constraint record_ids_pkey, drop constraint record_ids_workspace_id_fkey, add primary key (id);
alter table entities drop constraint entities_pkey, add primary key (id);
alter table identities drop constraint identities_pkey, add primary key (platform, handle);
alter table events drop constraint events_pkey, add primary key (id);
alter table memories drop constraint memories_pkey, add primary key (id, version);
alter table involvements drop constraint involvements_pkey, add primary key (record_kind, record_id, version, entity_id, via, role);
alter table evidence drop constraint evidence_pkey, add primary key (memory_id, version, event_id, role);
alter table appraisals drop constraint appraisals_pkey, add primary key (id);
alter table links drop constraint links_pkey, add primary key (id);

alter table entities drop column workspace_id, drop column bucket_id, drop column requires, drop column source_installation;
alter table events drop column workspace_id, drop column bucket_id, drop column requires, drop column source_installation;
alter table memories drop column workspace_id, drop column bucket_id, drop column requires, drop column source_installation;
alter table appraisals drop column workspace_id, drop column bucket_id, drop column requires, drop column source_installation;
alter table links drop column workspace_id, drop column bucket_id, drop column requires, drop column source_installation;
alter table record_ids drop column workspace_id;
alter table identities drop column workspace_id;
alter table involvements drop column workspace_id;
alter table evidence drop column workspace_id;
alter table rehearsals drop column workspace_id, drop column principal;

alter table entities add foreign key (id) references record_ids (id);
alter table identities add foreign key (actor_id) references entities (id);
alter table events add foreign key (id) references record_ids (id), add foreign key (place_id) references entities (id);
alter table memories add foreign key (id) references record_ids (id);
alter table involvements add foreign key (entity_id) references entities (id);
alter table evidence add foreign key (event_id) references events (id);
alter table appraisals add foreign key (id) references record_ids (id);
alter table links add foreign key (id) references record_ids (id);
alter table rehearsals add foreign key (memory_id) references record_ids (id);

create index entities_parent on entities (parent_id);
create index entities_type on entities (type);
create unique index entities_source on entities (source_system, source_native_id) where source_native_id is not null;
create index identities_actor on identities (actor_id);
create index events_occurred on events (occurred_at);
create index events_place on events (place_id);
create index events_part_of on events (part_of);
create unique index events_source on events (source_system, source_native_id) where source_native_id is not null;
create unique index memories_current on memories (id) where superseded_at is null;
create index memories_valid on memories (valid_from, valid_until);
create unique index memories_source on memories (source_system, source_native_id) where source_native_id is not null and superseded_at is null;
create index involvements_entity on involvements (entity_id, role);
create index evidence_event on evidence (event_id);
create index appraisals_target on appraisals (target_id, at);
create unique index appraisals_source on appraisals (source_system, source_native_id) where source_native_id is not null;
create index links_from on links (from_id, type);
create index links_to on links (to_id, type);
create unique index links_source on links (source_system, source_native_id) where source_native_id is not null;
create index rehearsals_memory on rehearsals (memory_id, recorded_at);

update entities set body = body - 'bucket';
update events set body = body - 'bucket';
update memories set body = body - 'bucket';
update appraisals set body = body - 'bucket';
update links set body = body - 'bucket';

drop table buckets;
drop table workspaces;

-- Each use of a memory is a row, so a recall as of an earlier time counts
-- only the uses made by then.
create table rehearsals (
  memory_id   text not null references record_ids (id),
  at          timestamptz not null,
  recorded_at timestamptz not null default clock_timestamp()
);
create index rehearsals_memory on rehearsals (memory_id, recorded_at);

-- The counters kept no times for earlier uses, so each becomes a use at the
-- last time recorded.
insert into rehearsals (memory_id, at, recorded_at)
select m.id, coalesce(m.last_recalled_at, m.recorded_at), coalesce(m.last_recalled_at, m.recorded_at)
from memories m, generate_series(1, m.recall_count)
where m.superseded_at is null;

alter table memories drop column recall_count, drop column last_recalled_at;

-- A record's type and every type it falls back to, worked out at capture
-- with the profiles the record names. Rows written before this carry their
-- namespace parents.
alter table memories add column type_lineage text[] not null default '{}';
alter table events add column type_lineage text[] not null default '{}';
update memories set type_lineage = array(
  select array_to_string((string_to_array(type, '.'))[1:n], '.')
  from generate_series(array_length(string_to_array(type, '.'), 1), 1, -1) n);
update events set type_lineage = array(
  select array_to_string((string_to_array(type, '.'))[1:n], '.')
  from generate_series(array_length(string_to_array(type, '.'), 1), 1, -1) n);
create index memories_type_lineage on memories using gin (type_lineage);
create index events_type_lineage on events using gin (type_lineage);

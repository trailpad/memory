drop index if exists memories_type_lineage, events_type_lineage;
alter table memories drop column type_lineage;
alter table events drop column type_lineage;
alter table memories add column recall_count integer not null default 0, add column last_recalled_at timestamptz;
update memories m set recall_count = r.count, last_recalled_at = r.last
from (select memory_id, count(*) as count, max(at) as last from rehearsals group by memory_id) r
where r.memory_id = m.id and m.superseded_at is null;
drop table if exists rehearsals;

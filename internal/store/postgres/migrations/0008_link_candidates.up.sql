create index involvements_entity_via on involvements (workspace_id, entity_id, via, record_type, record_id);
create index events_place_time on events (workspace_id, place_id, occurred_at, id);
create index events_reply on events (workspace_id, in_reply_to);

drop index entities_source;
create unique index entities_source on entities (workspace_id, bucket_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null;
drop index events_source;
create unique index events_source on events (workspace_id, bucket_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null;
drop index appraisals_source;
create unique index appraisals_source on appraisals (workspace_id, bucket_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null;
drop index links_source;
create unique index links_source on links (workspace_id, bucket_id, source_system, source_installation, source_native_id) nulls not distinct where source_native_id is not null;

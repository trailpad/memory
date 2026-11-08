create table embedding_jobs (
 workspace_id text not null, event_id text not null,
 model text not null default '', dimensions integer not null default 0,
 complete boolean not null default false, attempts integer not null default 0,
 retry_at timestamptz not null default now(),
 primary key(workspace_id,event_id),
 foreign key(workspace_id,event_id) references events(workspace_id,id) on delete cascade
);
insert into embedding_jobs(workspace_id,event_id) select workspace_id,id from events;
create index embedding_jobs_pending on embedding_jobs(retry_at) where not complete;
create function enqueue_embedding() returns trigger language plpgsql as $$
begin
 insert into embedding_jobs(workspace_id,event_id) values(new.workspace_id,new.id)
 on conflict(workspace_id,event_id) do update set complete=false,attempts=0,retry_at=now();
 return new;
end $$;
create trigger event_embedding after insert on events for each row execute function enqueue_embedding();

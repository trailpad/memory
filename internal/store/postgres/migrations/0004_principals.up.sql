alter table workspaces
  add column token_ttl  interval not null default interval '15 minutes',
  add column synced_ttl interval not null default interval '1 day';

create table principals (
  workspace_id text not null references workspaces (id),
  id           text not null,
  type         text not null check (type in ('person', 'service')),
  name         text,
  created_at   timestamptz not null default now(),
  primary key (workspace_id, id)
);

-- Only the hash of a key is kept, so a stolen database holds no usable key.
create table services (
  workspace_id  text not null,
  id            text not null,
  key_hash      bytea not null unique,
  actions       text[] not null,
  created_at    timestamptz not null default now(),
  revoked_at    timestamptz,
  primary key (workspace_id, id),
  foreign key (workspace_id, id) references principals (workspace_id, id)
);

-- A connector's installations: the source containers it may bind, and where
-- the accounts of their members live. A GitHub app installation's members
-- are github.com accounts; a Slack workspace's members are its own.
create table installations (
  workspace_id text not null,
  service_id   text not null,
  system       text not null,
  installation text not null,
  accounts     text not null,
  primary key (workspace_id, system, installation),
  foreign key (workspace_id, service_id) references services (workspace_id, id)
);

-- attests lists the source systems whose accounts this issuer proves, because
-- the person signed in to the issuer with that source's own sign-in.
create table issuers (
  workspace_id text not null references workspaces (id),
  issuer       text not null,
  service_id   text not null,
  public_key   bytea not null,
  attests      text[] not null default '{}',
  created_at   timestamptz not null default now(),
  removed_at   timestamptz,
  primary key (workspace_id, issuer),
  foreign key (workspace_id, service_id) references services (workspace_id, id)
);

create table subjects (
  workspace_id text not null,
  issuer       text not null,
  sub          text not null,
  principal_id text not null,
  created_at   timestamptz not null default now(),
  primary key (workspace_id, issuer, sub),
  foreign key (workspace_id, issuer) references issuers (workspace_id, issuer),
  foreign key (workspace_id, principal_id) references principals (workspace_id, id)
);

-- One source account belongs to one principal: the primary key refuses a
-- second claim.
create table account_links (
  workspace_id text not null,
  system       text not null,
  installation text not null,
  account      text not null,
  principal_id text not null,
  created_at   timestamptz not null default now(),
  primary key (workspace_id, system, installation, account),
  foreign key (workspace_id, principal_id) references principals (workspace_id, id)
);
create index account_links_principal on account_links (workspace_id, principal_id);

create table grants (
  workspace_id text not null,
  bucket_id    text not null,
  principal_id text not null,
  rights       text[] not null check (rights <@ array['read', 'write', 'administer']),
  created_by   text not null,
  created_at   timestamptz not null default now(),
  primary key (workspace_id, bucket_id, principal_id),
  foreign key (workspace_id, bucket_id) references buckets (workspace_id, id),
  foreign key (workspace_id, principal_id) references principals (workspace_id, id)
);
create index grants_principal on grants (workspace_id, principal_id);

-- Synced readers are source accounts, not principals: they read once the
-- account is linked, and stop the moment the link or the sync goes.
create table synced_readers (
  workspace_id text not null,
  bucket_id    text not null,
  system       text not null,
  installation text not null,
  account      text not null,
  expires_at   timestamptz not null,
  primary key (workspace_id, bucket_id, system, installation, account),
  foreign key (workspace_id, bucket_id) references buckets (workspace_id, id)
);
create index synced_readers_account on synced_readers (workspace_id, system, installation, account);

create table access_log (
  workspace_id text not null references workspaces (id),
  at           timestamptz not null default now(),
  actor        text not null,
  on_behalf_of text,
  change       text not null,
  detail       jsonb not null default '{}'
);
create index access_log_at on access_log (workspace_id, at);

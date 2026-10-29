drop table access_log;
drop table synced_readers;
drop table grants;
drop table account_links;
drop table subjects;
drop table issuers;
drop table installations;
drop table services;
drop table principals;

alter table workspaces drop column token_ttl, drop column synced_ttl;

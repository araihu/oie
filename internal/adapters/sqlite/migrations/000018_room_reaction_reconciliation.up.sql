CREATE TABLE room_reaction_privacy (
 account_id TEXT NOT NULL REFERENCES accounts(id),
 address TEXT NOT NULL,
 public INTEGER NOT NULL CHECK(public IN (0,1)),
 PRIMARY KEY(account_id,address)
);
ALTER TABLE message_reaction_observations ADD COLUMN archive_order INTEGER NOT NULL DEFAULT 0;
UPDATE message_reaction_observations SET archive_order=ordinal WHERE archive_id<>'';
CREATE INDEX reaction_observation_archive_order ON message_reaction_observations(account_id,archive_scope,occurred_at,archive_order) WHERE archive_id<>'';
PRAGMA user_version=18;

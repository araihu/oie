CREATE TABLE message_reaction_observations (
 ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 account_id TEXT NOT NULL REFERENCES accounts(id),
 address TEXT NOT NULL,
 kind TEXT NOT NULL,
 actor TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 archive_scope TEXT NOT NULL,
 archive_id TEXT NOT NULL,
 target_id TEXT NOT NULL,
 target_scope TEXT NOT NULL,
 target_author TEXT NOT NULL,
 fingerprint TEXT NOT NULL,
 occurred_at TEXT NOT NULL,
 payload TEXT NOT NULL,
 command_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX reaction_observation_target ON message_reaction_observations(account_id,address,target_id);
CREATE INDEX reaction_observation_identity ON message_reaction_observations(account_id,address,actor,provider_id);
CREATE INDEX reaction_observation_archive ON message_reaction_observations(account_id,archive_scope,archive_id);
CREATE TABLE message_reaction_commands (
 id TEXT PRIMARY KEY,
 revision_id TEXT NOT NULL,
 message_id TEXT NOT NULL REFERENCES messages(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 state TEXT NOT NULL CHECK(state IN ('queued','dispatching','submitted','unknown','failed')),
 payload TEXT NOT NULL,
 next_attempt_at TEXT NOT NULL
);
CREATE UNIQUE INDEX message_reaction_active ON message_reaction_commands(message_id) WHERE state IN ('queued','dispatching','unknown');
CREATE INDEX message_reaction_pending ON message_reaction_commands(state,next_attempt_at,id);
PRAGMA user_version=16;

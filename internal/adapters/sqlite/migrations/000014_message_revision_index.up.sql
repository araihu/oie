-- A bounded message-history lookup must not scan every retained revision.
CREATE INDEX message_revisions_entity ON message_revisions(entity_id);
PRAGMA user_version=14;

DELETE FROM changes WHERE kind='account.history_changed';
DROP TABLE history_status;
DROP TABLE archive_checkpoints;
DROP TABLE archive_aliases;
DROP INDEX messages_chronological;
ALTER TABLE messages DROP COLUMN created_at_ms;
PRAGMA user_version=2;

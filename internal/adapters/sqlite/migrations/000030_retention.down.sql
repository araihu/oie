DROP TRIGGER purge_message_dependencies;
DROP INDEX retention_operation_resource;
DROP INDEX retention_change_preview;
DROP INDEX retention_change_conversation;
DROP INDEX retention_change_message;
DROP INDEX retention_archive_alias_message;
DROP INDEX retention_message_age;
ALTER TABLE messages DROP COLUMN submitted_at_ms;
DROP TABLE accepted_direct_history;
DROP TABLE expired_message_references;
DROP TABLE expired_message_ids;
DROP VIEW effective_retention;
DROP TABLE conversation_retention;
DROP TABLE global_retention;
-- Messages removed by a retention policy cannot be recovered by a downgrade.
PRAGMA user_version=29;

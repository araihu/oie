DROP TABLE discarded_message_references;
DROP TABLE account_archive_retention;
ALTER TABLE global_retention DROP COLUMN archive_revision;
PRAGMA user_version=30;

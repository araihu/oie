DROP INDEX outbox_dispatch_due;
ALTER TABLE outbox DROP COLUMN wire_stage;
PRAGMA user_version=22;

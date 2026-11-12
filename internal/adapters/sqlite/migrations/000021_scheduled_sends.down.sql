DROP TABLE staged_attachments;
DROP INDEX outbox_schedule;
ALTER TABLE outbox DROP COLUMN scheduled_at;

DROP VIEW hidden_scheduled_messages;
DROP INDEX messages_timeline;
ALTER TABLE messages DROP COLUMN timeline_at_ms;

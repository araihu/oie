CREATE INDEX conversations_inbox_activity ON conversations(archived, activity_sequence DESC);
PRAGMA user_version=54;

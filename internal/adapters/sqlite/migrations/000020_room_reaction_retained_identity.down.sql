ALTER TABLE room_reaction_privacy ADD COLUMN changed_at TEXT NOT NULL DEFAULT '';
PRAGMA user_version=19;

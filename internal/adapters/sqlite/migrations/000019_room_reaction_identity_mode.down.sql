ALTER TABLE message_reaction_observations DROP COLUMN room_epoch;
ALTER TABLE room_reaction_privacy DROP COLUMN changed_at;
ALTER TABLE room_reaction_privacy DROP COLUMN epoch;
ALTER TABLE room_reaction_privacy DROP COLUMN identity_mode;
PRAGMA user_version=18;

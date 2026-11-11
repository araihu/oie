DROP TABLE room_reaction_privacy;
DROP INDEX reaction_observation_archive_order;
ALTER TABLE message_reaction_observations DROP COLUMN archive_order;
PRAGMA user_version=17;

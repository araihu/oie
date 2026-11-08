DELETE FROM changes WHERE kind='message.star_changed';
DELETE FROM operations WHERE kind='set_message_star';
DROP TABLE message_stars;
DROP TABLE message_star_revisions;
PRAGMA user_version=5;

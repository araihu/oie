DELETE FROM changes WHERE kind='message.preview_changed';
DROP TABLE preview_revisions;
DROP TABLE message_previews;
DROP TABLE media_files;
PRAGMA user_version=4;

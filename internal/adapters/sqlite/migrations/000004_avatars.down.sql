DELETE FROM changes WHERE kind='profile.avatar_changed';
DROP TABLE avatar_revisions;
DROP TABLE avatars;
PRAGMA user_version=3;

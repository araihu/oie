-- The recovered references are valid source metadata in the previous schema.
DROP VIEW room_archive_reference_conflicts;
DROP TRIGGER resolve_legacy_room_reply;
DROP VIEW verified_legacy_room_reply_targets;
DROP VIEW legacy_room_reply_candidates;
DROP TABLE legacy_room_archive_uids;
PRAGMA user_version=54;

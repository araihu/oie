ALTER TABLE room_reaction_privacy ADD COLUMN identity_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE room_reaction_privacy ADD COLUMN epoch INTEGER NOT NULL DEFAULT 0;
ALTER TABLE room_reaction_privacy ADD COLUMN changed_at TEXT NOT NULL DEFAULT '';
ALTER TABLE message_reaction_observations ADD COLUMN room_epoch INTEGER NOT NULL DEFAULT 0;

WITH latest AS (
 SELECT account_id,address,actor,row_number() OVER (
  PARTITION BY account_id,address ORDER BY (archive_id='') DESC,ordinal DESC
 ) AS position
 FROM message_reaction_observations WHERE kind='room'
)
INSERT INTO room_reaction_privacy(account_id,address,public,identity_mode)
SELECT account_id,address,0,CASE WHEN actor LIKE 'occupant:%' THEN 'semianonymous' ELSE 'nonanonymous' END
FROM latest WHERE position=1
ON CONFLICT(account_id,address) DO UPDATE SET identity_mode=
 CASE WHEN public=1 THEN 'nonanonymous' ELSE excluded.identity_mode END;
UPDATE room_reaction_privacy SET identity_mode='nonanonymous' WHERE public=1;
PRAGMA user_version=19;

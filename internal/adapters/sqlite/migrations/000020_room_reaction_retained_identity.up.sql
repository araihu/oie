-- Observations from both identity schemes predate the first verified transition
-- in these rooms. Their peer sets cannot safely be carried into the current mode.
UPDATE room_reaction_privacy SET epoch=1 WHERE epoch=0
AND EXISTS(SELECT 1 FROM message_reaction_observations o
 WHERE o.account_id=room_reaction_privacy.account_id AND o.address=room_reaction_privacy.address
 AND o.kind='room' AND COALESCE(json_extract(o.payload,'$.IsOwn'),0)=0 AND o.actor LIKE 'occupant:%')
AND EXISTS(SELECT 1 FROM message_reaction_observations o
 WHERE o.account_id=room_reaction_privacy.account_id AND o.address=room_reaction_privacy.address
 AND o.kind='room' AND COALESCE(json_extract(o.payload,'$.IsOwn'),0)=0 AND o.actor NOT LIKE 'occupant:%');
ALTER TABLE room_reaction_privacy DROP COLUMN changed_at;
PRAGMA user_version=20;

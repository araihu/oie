CREATE TEMP TABLE portable_keys AS
SELECT 0 AS incoming,* FROM main.xmpp_message_keys
UNION ALL SELECT 1,* FROM incoming.xmpp_message_keys;
CREATE INDEX portable_keys_lookup ON portable_keys(incoming,account,chat,kind,key_type,authority,remote_id);
CREATE INDEX portable_keys_message ON portable_keys(incoming,message_id);

CREATE TEMP TABLE portable_pairs(source TEXT,destination TEXT, PRIMARY KEY(source,destination)) WITHOUT ROWID;
INSERT OR IGNORE INTO portable_pairs
SELECT s.message_id,d.message_id FROM portable_keys s JOIN portable_keys d
USING(account,chat,kind,key_type,authority,remote_id)
WHERE s.incoming=1 AND d.incoming=0;

CREATE TEMP TABLE portable_message_matches(source TEXT,destination TEXT,PRIMARY KEY(source,destination)) WITHOUT ROWID;
INSERT INTO portable_message_matches
SELECT p.source,p.destination FROM portable_pairs p
JOIN incoming.messages s ON s.id=p.source JOIN main.messages d ON d.id=p.destination
-- Native sends retain their original operation and wire identity. Their
-- authenticated echoes go through the send reconciliation rules.
WHERE p.source=p.destination OR NOT (
 (s.is_own=1 AND EXISTS(SELECT 1 FROM main.messages native
      WHERE native.conversation_id=d.conversation_id AND native.account_id=d.account_id
        AND native.id=json_extract(s.payload,'$.OwnOriginID') AND native.is_own=1 AND native.provider_id=native.id))
 OR (d.is_own=1 AND EXISTS(SELECT 1 FROM incoming.messages native
      WHERE native.conversation_id=s.conversation_id AND native.account_id=s.account_id
        AND native.id=json_extract(d.payload,'$.OwnOriginID') AND native.is_own=1 AND native.provider_id=native.id))
 -- A relayed witness can already be bound to the original in its own
 -- profile. Its contested keys are reconciled individually with that send.
 OR EXISTS(SELECT 1 FROM incoming.message_canonical_aliases e JOIN incoming.messages native ON native.id=e.canonical_id
     WHERE e.alias_id=s.id AND s.is_own=1 AND native.id=json_extract(s.payload,'$.OwnOriginID')
       AND native.is_own=1 AND native.provider_id=native.id AND native.conversation_id=s.conversation_id)
 OR EXISTS(SELECT 1 FROM main.message_canonical_aliases e JOIN main.messages native ON native.id=e.canonical_id
     WHERE e.alias_id=d.id AND d.is_own=1 AND native.id=json_extract(d.payload,'$.OwnOriginID')
       AND native.is_own=1 AND native.provider_id=native.id AND native.conversation_id=d.conversation_id)
 OR (s.id=s.provider_id AND s.is_own=1) OR (d.id=d.provider_id AND d.is_own=1)
 OR (s.is_own=1 AND d.is_own=1 AND (
   -- These source rows carry immutable local star/edit histories. The
   -- existing echo merge maps their wire references to the visible original.
   EXISTS(SELECT 1 FROM main.message_canonical_aliases e WHERE e.alias_id=s.id AND e.canonical_id=d.id)
   OR EXISTS(SELECT 1 FROM incoming.message_canonical_aliases e WHERE e.alias_id=d.id AND e.canonical_id=s.id)
   OR EXISTS(SELECT 1 FROM incoming.message_correction_facts f
      WHERE f.message_id=s.id AND instr(f.scope,'xmpp:correct:room:')=1
        AND s.provider_id IN (oie_room_correction_provider_id(f.author,f.remote_id),
          oie_scoped_room_correction_provider_id(f.scope,f.author,f.remote_id))
        AND EXISTS(SELECT 1 FROM main.message_correction_facts g WHERE g.message_id=d.id
          AND g.scope=f.scope AND g.author=f.author AND g.remote_id=f.remote_id AND g.target_id=f.target_id))
 ))
);

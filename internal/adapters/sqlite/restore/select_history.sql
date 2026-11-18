-- A direct stanza can use the original send's ID but carry a distinct
-- structured composition. Keep its own references and origin evidence
-- without competing for the native message's provider key. Ambiguous legacy
-- observations likewise keep their own row without reserving a native key.
CREATE TEMP TABLE restore_direct_mention_echo_keys (
  id TEXT NOT NULL, provider_id TEXT NOT NULL, is_local INTEGER NOT NULL, ambiguous_origin INTEGER NOT NULL,
  PRIMARY KEY(is_local,id)
) WITHOUT ROWID;
INSERT INTO restore_direct_mention_echo_keys
SELECT b.id,oie_direct_mention_echo_provider_id(b.payload),0,0
FROM backup.messages b JOIN restore_messages x ON x.id=b.id
JOIN backup.conversations c ON c.id=b.conversation_id AND c.account_id=b.account_id
WHERE b.is_own=1 AND b.state='submitted' AND b.id<>b.provider_id
  AND b.provider_id=json_extract(b.payload,'$.OwnOriginID')
  AND json_extract(b.payload,'$.WireMentions')='present' AND json_extract(c.payload,'$.Kind')='direct'
  AND NOT EXISTS (SELECT 1 FROM restore_legacy_direct_origins old WHERE old.is_backup=1 AND old.id=b.id AND old.wire_id='')
UNION ALL
SELECT d.id,oie_direct_mention_echo_provider_id(d.payload),1,0
FROM backup.messages b JOIN restore_messages x ON x.id=b.id AND x.is_new=1
JOIN backup.conversations c ON c.id=b.conversation_id AND c.account_id=b.account_id
JOIN messages d ON d.account_id=b.account_id AND d.conversation_id=b.conversation_id
  AND d.provider_id=b.id AND d.is_own=1
WHERE b.is_own=1 AND b.id=b.provider_id AND json_extract(c.payload,'$.Kind')='direct'
  AND d.state='submitted' AND d.id<>d.provider_id
  AND d.provider_id=json_extract(d.payload,'$.OwnOriginID')
  AND json_extract(d.payload,'$.WireMentions')='present'
  AND oie_wire_mentions_match(b.payload,json_extract(d.payload,'$.WireMentions'),json_extract(d.payload,'$.WireMentionDigest'))=0
  AND NOT EXISTS (SELECT 1 FROM restore_legacy_direct_origins old WHERE old.is_backup=0 AND old.id=d.id AND old.wire_id='')
UNION ALL
SELECT old.id,'legacy-direct-echo:'||old.id,1-old.is_backup,1
FROM restore_legacy_direct_origins old
WHERE old.wire_id='' AND old.provider_id GLOB '????????-????-7???-[89ab]???-????????????'
  AND length(replace(old.provider_id,'-',''))=32 AND replace(old.provider_id,'-','') NOT GLOB '*[^0-9a-f]*';

CREATE TEMP VIEW restore_own_echo_aliases AS
SELECT echo_id,original_id FROM restore_witness_echoes
UNION
SELECT source_id,retained_id FROM restore_idless_direct_echoes
UNION
SELECT a.alias_id,a.canonical_id FROM message_canonical_aliases a
JOIN messages e ON e.id=a.alias_id AND e.is_own=1 AND e.state='submitted'
JOIN messages d ON d.id=a.canonical_id AND d.id=d.provider_id AND d.is_own=1
  AND d.account_id=e.account_id AND d.conversation_id=e.conversation_id
WHERE json_extract(e.payload,'$.OwnOriginID')=d.id;

-- A backup of a merged profile can carry the witness and a native copy of
-- its references. Preserve an existing destination owner of the scoped ID.
CREATE TEMP TABLE restore_conflicting_relayed_echo_refs (
  message_id TEXT NOT NULL, scope TEXT NOT NULL, remote_id TEXT NOT NULL, author TEXT NOT NULL,
  PRIMARY KEY(message_id,scope,remote_id,author)
) WITHOUT ROWID;
INSERT INTO restore_conflicting_relayed_echo_refs
WITH conflicts AS (
  SELECT pair.echo_id,pair.original_id,r.scope,r.remote_id,r.author
  FROM restore_own_echo_aliases pair
  JOIN backup.message_canonical_aliases edge ON edge.alias_id=pair.echo_id AND edge.canonical_id=pair.original_id
  JOIN restore_messages selected ON selected.id=pair.echo_id
  JOIN backup.messages echo ON echo.id=pair.echo_id
  JOIN backup.message_references r ON r.message_id=pair.echo_id
  WHERE NOT EXISTS (SELECT 1 FROM restore_idless_direct_echoes idless WHERE idless.source_id=pair.echo_id)
  AND EXISTS (
    SELECT 1 FROM message_references local
    JOIN messages owner ON owner.id=local.message_id AND owner.account_id=echo.account_id
      AND owner.conversation_id=echo.conversation_id
    LEFT JOIN restore_own_echo_aliases own ON own.echo_id=owner.id
    LEFT JOIN message_canonical_aliases linked ON linked.alias_id=owner.id
    WHERE local.scope=r.scope AND local.remote_id=r.remote_id
      AND COALESCE(own.original_id,linked.canonical_id,owner.id)<>pair.original_id
  )
)
SELECT echo_id,scope,remote_id,author FROM conflicts
UNION
SELECT original_id,scope,remote_id,author FROM conflicts;

-- The primary archive key identifies the packet carrying the rejected fact.
-- Leave any local owner in place and keep other source archive keys eligible.
CREATE TEMP TABLE restore_conflicting_relayed_echo_aliases (
  account_id TEXT NOT NULL, scope TEXT NOT NULL, archive_id TEXT NOT NULL,
  PRIMARY KEY(account_id,scope,archive_id)
) WITHOUT ROWID;
INSERT INTO restore_conflicting_relayed_echo_aliases
SELECT DISTINCT a.account_id,a.scope,a.archive_id
FROM restore_own_echo_aliases pair
JOIN backup.message_canonical_aliases edge ON edge.alias_id=pair.echo_id AND edge.canonical_id=pair.original_id
JOIN backup.messages echo ON echo.id=pair.echo_id
JOIN backup.archive_aliases a ON a.message_id IN (pair.echo_id,pair.original_id) AND a.account_id=echo.account_id
  AND a.scope=json_extract(echo.payload,'$.ArchiveScope') AND a.archive_id=json_extract(echo.payload,'$.ArchiveID')
WHERE EXISTS(SELECT 1 FROM restore_conflicting_relayed_echo_refs r WHERE r.message_id=pair.echo_id);

-- Supplemental protocol facts can change the display and shared-content index
-- without making a message revision. Compare the same keys as the insertions.
CREATE TEMP TABLE restore_message_metadata_changes (id TEXT PRIMARY KEY) WITHOUT ROWID;
INSERT INTO restore_message_metadata_changes
SELECT b.message_id FROM backup.message_references b
JOIN restore_messages r ON r.id=b.message_id AND r.is_new=0
WHERE NOT EXISTS (
  SELECT 1 FROM message_references d WHERE d.message_id=b.message_id AND d.scope=b.scope
    AND d.remote_id=b.remote_id AND d.author=b.author AND d.source_hash=b.source_hash
)
AND NOT EXISTS (
  SELECT 1 FROM restore_conflicting_relayed_echo_refs d WHERE d.message_id=b.message_id AND d.scope=b.scope
    AND d.remote_id=b.remote_id AND d.author=b.author
)
UNION
SELECT b.message_id FROM backup.message_replies b
JOIN restore_messages r ON r.id=b.message_id AND r.is_new=0
WHERE NOT EXISTS (
  SELECT 1 FROM message_replies d WHERE d.message_id=b.message_id AND d.body_hash=b.body_hash
)
UNION
SELECT b.message_id FROM backup.message_sender_identities b
JOIN restore_messages r ON r.id=b.message_id AND r.is_new=0
WHERE NOT EXISTS (
  SELECT 1 FROM message_sender_identities d WHERE d.message_id=b.message_id
    AND d.personal_address=b.personal_address AND d.source_hash=b.source_hash
);

-- Detect a history change before inserting any revision rows. Chats whose
-- newest item and unread count stay the same still need a refresh when
-- retained messages, edit history, or protocol facts are added.
UPDATE restore_chats SET history_changed=1
WHERE id IN (
  SELECT m.conversation_id FROM backup.messages m JOIN restore_messages r ON r.id=m.id AND r.is_new=1
  UNION
  SELECT b.conversation_id FROM restore_witness_echoes e JOIN backup.messages b ON b.id=e.original_id
  WHERE NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=e.echo_id AND a.canonical_id=e.original_id)
  UNION
  SELECT m.conversation_id FROM messages m JOIN restore_message_metadata_changes r ON r.id=m.id
  UNION
  SELECT m.conversation_id FROM backup.message_revisions b
  JOIN restore_messages r ON r.id=b.entity_id AND r.is_new=0
  JOIN messages m ON m.id=r.id
  WHERE NOT EXISTS(SELECT 1 FROM message_revisions d WHERE d.id=b.id)
  UNION
  SELECT m.conversation_id FROM backup.message_star_revisions b
  JOIN restore_messages r ON r.id=b.message_id AND r.is_new=0
  JOIN messages m ON m.id=r.id
  WHERE NOT EXISTS(SELECT 1 FROM message_star_revisions d WHERE d.id=b.id)
  UNION
  SELECT c.id FROM backup.message_retractions b
  JOIN conversations c ON c.account_id=b.account_id AND c.address=b.address
  WHERE NOT EXISTS(
    SELECT 1 FROM message_retractions d WHERE d.account_id=b.account_id
      AND d.kind=b.kind AND d.address=b.address AND d.scope=b.scope AND d.remote_id=b.remote_id
  )
  UNION
  SELECT c.id FROM backup.room_message_moderation b
  JOIN conversations c ON c.account_id=b.account_id AND c.address=b.room_address
  WHERE NOT EXISTS(
    SELECT 1 FROM room_message_moderation d
    WHERE d.account_id=b.account_id AND d.room_address=b.room_address AND d.stanza_id=b.stanza_id
  )
);

DELETE FROM shared_content_state WHERE message_id IN (
  SELECT id FROM restore_message_metadata_changes
  UNION
  SELECT a.canonical_id FROM message_canonical_aliases a
  JOIN restore_message_metadata_changes r ON r.id=a.alias_id
);
DROP TABLE restore_message_metadata_changes;

CREATE TEMP TABLE restore_accounts (id TEXT PRIMARY KEY, is_new INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_accounts
SELECT a.id, NOT EXISTS(SELECT 1 FROM accounts d WHERE d.id=a.id) FROM backup.accounts a
WHERE NOT EXISTS (SELECT 1 FROM deleted_account_ids d WHERE d.id=a.id)
  AND NOT EXISTS (
  SELECT 1 FROM operations o
  WHERE o.resource_id=a.id AND o.kind='register_account' AND o.result='null'
);

CREATE TEMP TABLE restore_chats (
  id TEXT PRIMARY KEY, is_new INTEGER NOT NULL, history_changed INTEGER NOT NULL DEFAULT 0,
  promote_room INTEGER NOT NULL DEFAULT 0, quotes_redacted INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
INSERT INTO restore_chats(id,is_new)
SELECT c.id, NOT EXISTS(SELECT 1 FROM conversations d WHERE d.id=c.id)
FROM backup.conversations c JOIN restore_accounts a ON a.id=c.account_id;

-- Opening a room can promote an empty direct placeholder under the same ID.
-- Skip an ancestor of that room so its old direct scope cannot import data.
DELETE FROM restore_chats
WHERE is_new=0 AND EXISTS (
  SELECT 1 FROM backup.conversations b
  JOIN conversations d ON d.id=b.id AND d.account_id=b.account_id AND d.address=b.address
  JOIN backup.conversation_revisions original ON original.id=b.revision_id AND original.entity_id=b.id
  WHERE b.id=restore_chats.id AND b.revision_id<>d.revision_id
    AND json_extract(b.payload,'$.Kind')='direct' AND json_extract(d.payload,'$.Kind')='room'
    AND json_extract(original.payload,'$.Kind')='direct'
    AND NOT EXISTS(SELECT 1 FROM backup.messages m WHERE m.conversation_id=b.id)
    AND NOT EXISTS(SELECT 1 FROM backup.staged_attachments f WHERE f.conversation_id=b.id)
    AND EXISTS (
      WITH RECURSIVE ancestors(id) AS (
        SELECT d.revision_id
        UNION
        SELECT parent.value FROM ancestors a
        JOIN conversation_revisions r ON r.id=a.id AND r.entity_id=d.id
        JOIN json_each(r.parents) parent ON parent.type='text'
      )
      SELECT 1 FROM ancestors a
      JOIN conversation_revisions known ON known.id=a.id AND known.entity_id=d.id
      WHERE known.id=b.revision_id AND json_extract(known.payload,'$.Kind')='direct'
    )
);

-- A room revision can promote its original destination placeholder when
-- the destination has no direct history or send intent in this scope.
UPDATE restore_chats SET promote_room=1, history_changed=1
WHERE is_new=0 AND EXISTS (
  SELECT 1 FROM backup.conversations b
  JOIN conversations d ON d.id=b.id AND d.account_id=b.account_id AND d.address=b.address
  JOIN conversation_revisions original ON original.id=d.revision_id AND original.entity_id=d.id
  WHERE b.id=restore_chats.id AND b.revision_id<>d.revision_id
    AND json_extract(d.payload,'$.Kind')='direct' AND json_extract(b.payload,'$.Kind')='room'
    AND json_extract(original.payload,'$.Kind')='direct'
    AND COALESCE(json_extract(d.payload,'$.Request'),'')=''
    AND json_extract(d.payload,'$.DirectHistoryCheck') IS NULL
    AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.conversation_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM staged_attachments f WHERE f.conversation_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM recovered_sends s WHERE s.conversation_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM accepted_direct_history h WHERE h.conversation_id=d.id)
    AND NOT EXISTS(SELECT 1 FROM expired_message_references e WHERE e.conversation_id=d.id)
    AND EXISTS (
      WITH RECURSIVE ancestors(id) AS (
        SELECT root.id FROM backup.conversation_revisions root
        WHERE root.id=b.revision_id AND root.entity_id=b.id AND json_extract(root.payload,'$.Kind')='room'
        UNION
        SELECT parent.value FROM ancestors a
        JOIN backup.conversation_revisions r ON r.id=a.id AND r.entity_id=b.id
        JOIN json_each(r.parents) parent ON parent.type='text'
      )
      SELECT 1 FROM ancestors a
      JOIN backup.conversation_revisions known ON known.id=a.id AND known.entity_id=b.id
      WHERE known.id=d.revision_id AND json_extract(known.payload,'$.Kind')='direct'
    )
);

CREATE TEMP VIEW restore_message_candidates AS
SELECT m.id,d.id IS NULL AS is_new,a.canonical_id,
       COALESCE(r.days,CASE json_extract(c.payload,'$.Kind') WHEN 'room' THEN g.rooms_days ELSE g.direct_days END) AS days,
       COALESCE(
         (SELECT COALESCE(root.submitted_at_ms,root.timeline_at_ms,root.created_at_ms)
          FROM backup.messages root WHERE root.id=a.canonical_id),
         m.submitted_at_ms,m.timeline_at_ms,m.created_at_ms
       ) AS age_ms
FROM backup.messages m
JOIN restore_chats chat ON chat.id=m.conversation_id
JOIN backup.conversations c ON c.id=chat.id AND c.account_id=m.account_id
LEFT JOIN messages d ON d.id=m.id
LEFT JOIN backup.outbox o ON o.message_id=m.id
LEFT JOIN backup.message_canonical_aliases a ON a.alias_id=m.id
LEFT JOIN conversation_retention r ON r.conversation_id=m.conversation_id
CROSS JOIN global_retention g
WHERE (d.id IS NOT NULL OR (
  -- A correction can follow an older retained root, but expiry evidence
  -- and the exclusion of active sends apply to every candidate.
  NOT EXISTS(SELECT 1 FROM expired_message_ids e WHERE e.id IN (m.id,a.canonical_id))
  AND NOT EXISTS (
    SELECT 1 FROM expired_message_references e
    JOIN backup.message_references ref ON ref.scope=e.scope AND ref.remote_id=e.remote_id AND ref.author=e.author
    WHERE e.conversation_id=m.conversation_id AND ref.message_id IN (m.id,a.canonical_id)
  )
))
AND (o.state IS NULL OR o.state NOT IN ('queued','dispatching','unknown'))
AND m.state NOT IN ('queued','dispatching','unknown');

CREATE TEMP TABLE restore_messages (id TEXT PRIMARY KEY, is_new INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_messages
SELECT m.id,m.is_new FROM restore_message_candidates m
WHERE m.is_new=0 OR m.days=0 OR m.age_ms>=@now_ms - 86400000 * m.days
   OR EXISTS (
       SELECT 1 FROM backup.message_stars s WHERE s.starred=1 AND (
         s.message_id=COALESCE(m.canonical_id,m.id)
         OR s.message_id IN (SELECT al.alias_id FROM backup.message_canonical_aliases al WHERE al.canonical_id=COALESCE(m.canonical_id,m.id))
       )
   );

-- Restore send evidence onto the original local row. An own archive echo
-- retained under another row ID would duplicate that message in the timeline.
CREATE TEMP VIEW restore_source_echoes AS
SELECT m.*,COALESCE(NULLIF(json_extract(m.payload,'$.OwnOriginID'),''), m.provider_id) AS wire_id
FROM backup.messages m JOIN restore_chats x ON x.id=m.conversation_id
JOIN backup.conversations c ON c.id=x.id AND c.account_id=m.account_id
WHERE m.is_own=1 AND m.state='submitted' AND m.id<>m.provider_id
  AND (json_extract(m.payload,'$.OwnOriginID')<>'' OR json_extract(c.payload,'$.Kind')='direct'
       OR json_extract(m.payload,'$.Historical')=0);

CREATE TEMP TABLE restore_suppressed_echoes (
  source_id TEXT PRIMARY KEY, retained_id TEXT NOT NULL
) WITHOUT ROWID;
-- The destination can retain its own original beyond the archive cutoff.
-- The verified echo may still contribute metadata for that retained body.
INSERT INTO restore_suppressed_echoes
SELECT m.id,d.id FROM restore_source_echoes m
JOIN messages d ON d.id=m.wire_id AND d.id=d.provider_id
  AND d.account_id=m.account_id AND d.conversation_id=m.conversation_id AND d.is_own=1
LEFT JOIN backup.message_canonical_aliases a ON a.alias_id=m.id
LEFT JOIN backup.outbox o ON o.message_id=m.id
WHERE NOT EXISTS(SELECT 1 FROM messages existing WHERE existing.id=m.id)
  AND (o.state IS NULL OR o.state NOT IN ('queued','dispatching','unknown'))
  AND NOT EXISTS(SELECT 1 FROM expired_message_ids e WHERE e.id IN (m.id,a.canonical_id))
  AND NOT EXISTS (
    SELECT 1 FROM expired_message_references e JOIN backup.message_references ref
      ON ref.scope=e.scope AND ref.remote_id=e.remote_id AND ref.author=e.author
    WHERE e.conversation_id=m.conversation_id AND ref.message_id IN (m.id,a.canonical_id)
  );
-- A witness can arrive before the original sending profile. Keep its source
-- row and revisions, but display the logical send under the original ID.
CREATE TEMP TABLE restore_witness_echoes (
  echo_id TEXT PRIMARY KEY, original_id TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO restore_witness_echoes
WITH echoes AS (
  SELECT d.id,d.account_id,d.conversation_id,d.provider_id,d.payload,
         json_extract(c.payload,'$.Kind') AS kind
  FROM messages d JOIN restore_chats x ON x.id=d.conversation_id
  JOIN conversations c ON c.id=x.id AND c.account_id=d.account_id
  WHERE d.is_own=1 AND d.state='submitted' AND d.id<>d.provider_id
  UNION ALL
  SELECT b.id,b.account_id,b.conversation_id,b.provider_id,b.payload,
         json_extract(c.payload,'$.Kind') AS kind
  FROM backup.messages b JOIN restore_messages selected ON selected.id=b.id AND selected.is_new=1
  JOIN backup.message_canonical_aliases a ON a.alias_id=b.id
  JOIN backup.conversations c ON c.id=b.conversation_id AND c.account_id=b.account_id
  WHERE b.is_own=1 AND b.state='submitted' AND b.id<>b.provider_id
    AND json_extract(b.payload,'$.OwnOriginID')=a.canonical_id
), pairs AS (
  SELECT e.id AS echo_id,b.id AS original_id,b.account_id,b.conversation_id,b.payload AS original_payload,
         json_extract(e.payload,'$.OriginalText') AS echo_body,
         COALESCE(NULLIF(json_extract(e.payload,'$.WireBodySHA256'),''),
                  oie_source_hash(json_extract(e.payload,'$.OriginalText'))) AS echo_hash
  FROM echoes e JOIN backup.messages b
    ON b.id=COALESCE(NULLIF(json_extract(e.payload,'$.OwnOriginID'),''),
                     CASE WHEN e.kind='direct' OR json_extract(e.payload,'$.Historical')=0 THEN e.provider_id END)
    AND b.account_id=e.account_id AND b.conversation_id=e.conversation_id
  JOIN restore_messages selected ON selected.id=b.id
  LEFT JOIN message_canonical_aliases d ON d.alias_id=e.id
  WHERE b.is_own=1 AND b.id=b.provider_id AND b.state IN ('submitted','failed')
    AND (d.alias_id IS NULL OR d.canonical_id=b.id)
    AND NOT EXISTS(SELECT 1 FROM backup.message_canonical_aliases a WHERE a.alias_id=b.id)
    AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=b.id)
    AND json_type(e.payload,'$.OriginalText')='text' AND json_type(b.payload,'$.OriginalText')='text'
)
SELECT echo_id,original_id FROM pairs p
WHERE (json_type(original_payload,'$.Attachments[0]') IS NULL AND echo_hash=COALESCE(
          NULLIF(json_extract(original_payload,'$.WireBodySHA256'),''),
          oie_source_hash(json_extract(original_payload,'$.OriginalText'))))
   OR (COALESCE(json_extract(original_payload,'$.AttachmentWireText'),'')<>''
       AND oie_source_hash(json_extract(original_payload,'$.AttachmentWireText'))=echo_hash)
   OR oie_attachment_wire_matches(json_extract(original_payload,'$.OriginalText'),json_extract(original_payload,'$.AttachmentWireText'),
          json_extract(original_payload,'$.WireBodySHA256'),echo_body)=1
   OR EXISTS (
     SELECT 1 FROM backup.message_revisions r WHERE r.entity_id=p.original_id
       AND json_extract(r.payload,'$.IsOwn')=1 AND json_extract(r.payload,'$.ProviderID')=p.original_id
       AND json_extract(r.payload,'$.AccountID')=p.account_id AND json_extract(r.payload,'$.ConversationID')=p.conversation_id
       AND json_extract(r.payload,'$.SendState') IN ('submitted','failed')
       AND json_extract(r.payload,'$.Attachments[0]') IS NOT NULL
       AND COALESCE(json_extract(r.payload,'$.AttachmentWireText'),'')<>''
       AND (oie_source_hash(json_extract(r.payload,'$.AttachmentWireText'))=p.echo_hash
            OR oie_attachment_wire_matches(json_extract(r.payload,'$.OriginalText'),json_extract(r.payload,'$.AttachmentWireText'),
                 json_extract(r.payload,'$.WireBodySHA256'),p.echo_body)=1)
   );

-- A relayed backup can carry both proven source rows and their canonical
-- edge. Keep that witness revision even if the native row is already local.
DELETE FROM restore_suppressed_echoes WHERE EXISTS (
  SELECT 1 FROM restore_witness_echoes e
  WHERE e.echo_id=restore_suppressed_echoes.source_id AND e.original_id=restore_suppressed_echoes.retained_id
);
DELETE FROM restore_messages WHERE id IN (SELECT source_id FROM restore_suppressed_echoes);

-- A separately retained witness or a re-export of the native send can
-- carry corrections for an exact root already in the destination.
CREATE TEMP TABLE restore_verified_correction_roots AS
WITH originals AS (
  SELECT source_id,retained_id FROM restore_suppressed_echoes
  UNION ALL
  SELECT archived.id,retained.id
  FROM restore_messages selected JOIN backup.messages archived ON archived.id=selected.id
  JOIN messages retained ON retained.id=archived.id AND retained.account_id=archived.account_id
    AND retained.conversation_id=archived.conversation_id
  JOIN conversations chat ON chat.id=retained.conversation_id AND json_extract(chat.payload,'$.Kind')='room'
  WHERE selected.is_new=0 AND archived.is_own=1 AND archived.id=archived.provider_id AND archived.state='submitted'
    AND retained.is_own=1 AND retained.id=retained.provider_id AND retained.state='submitted'
)
SELECT e.source_id,e.retained_id,f.scope,f.author,f.remote_id,f.target_id,f.source_hash,
       native.revision_id AS source_revision_id
FROM originals e
JOIN backup.messages echo ON echo.id=e.source_id
JOIN backup.message_correction_facts f ON f.message_id=e.source_id AND f.target_id=''
JOIN backup.message_revisions archived ON archived.id=f.source_revision_id AND archived.entity_id=e.source_id
JOIN messages native ON native.id=e.retained_id
JOIN message_revisions retained ON retained.id=native.revision_id AND retained.entity_id=native.id
WHERE f.scope<>'' AND f.author<>'' AND f.remote_id<>''
  AND json_type(native.payload,'$.Attachments[0]') IS NULL
  AND f.source_hash=COALESCE(NULLIF(json_extract(echo.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(echo.payload,'$.OriginalText')))
  AND f.source_hash=COALESCE(NULLIF(json_extract(archived.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(archived.payload,'$.OriginalText')))
  AND f.source_hash=COALESCE(NULLIF(json_extract(native.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(native.payload,'$.OriginalText')))
  AND f.source_hash=COALESCE(NULLIF(json_extract(retained.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(retained.payload,'$.OriginalText')))
  AND NOT EXISTS (
    SELECT 1 FROM message_correction_facts existing
    JOIN messages owner ON owner.id=existing.message_id
      AND owner.account_id=native.account_id AND owner.conversation_id=native.conversation_id
    LEFT JOIN message_canonical_aliases alias ON alias.alias_id=owner.id
    WHERE existing.scope=f.scope AND existing.author=f.author AND existing.remote_id=f.remote_id
      AND COALESCE(alias.canonical_id,owner.id)<>e.retained_id
  );

-- A room's minimum can keep a native send beyond the age cutoff. Its exact
-- correction sources follow the retained root only through verified links.
WITH RECURSIVE retained_edits(source_id,scope,author,remote_id,account_id,conversation_id,id,is_new) AS (
  SELECT root.source_id,root.scope,root.author,root.remote_id,native.account_id,native.conversation_id,NULL,0
  FROM restore_verified_correction_roots root
  JOIN messages native ON native.id=root.retained_id AND native.state='submitted'
  JOIN conversations chat ON chat.id=native.conversation_id AND json_extract(chat.payload,'$.Kind')='room'
  WHERE NOT EXISTS (
      SELECT 1 FROM expired_message_references expired
      WHERE expired.conversation_id=chat.id AND expired.scope=root.scope
        AND expired.remote_id=root.remote_id AND expired.author=root.author
  )
  UNION
  SELECT parent.source_id,parent.scope,parent.author,fact.remote_id,parent.account_id,parent.conversation_id,child.id,child.is_new
  FROM retained_edits parent
  JOIN backup.message_correction_facts fact ON fact.scope=parent.scope AND fact.author=parent.author AND fact.target_id=parent.remote_id
  JOIN restore_message_candidates child ON child.id=fact.message_id AND child.canonical_id=parent.source_id
  JOIN backup.messages archived ON archived.id=child.id AND archived.account_id=parent.account_id
    AND archived.conversation_id=parent.conversation_id
  JOIN backup.message_revisions revision ON revision.id=fact.source_revision_id AND revision.entity_id=child.id
  WHERE fact.remote_id<>''
    AND fact.source_hash=COALESCE(NULLIF(json_extract(archived.payload,'$.WireBodySHA256'),''),
                                 oie_source_hash(json_extract(archived.payload,'$.OriginalText')))
    AND fact.source_hash=COALESCE(NULLIF(json_extract(revision.payload,'$.WireBodySHA256'),''),
                                 oie_source_hash(json_extract(revision.payload,'$.OriginalText')))
    AND NOT EXISTS (
        SELECT 1 FROM expired_message_references expired
        WHERE expired.conversation_id=parent.conversation_id AND expired.scope=fact.scope
          AND expired.remote_id=fact.remote_id AND expired.author=fact.author
    )
    AND NOT EXISTS (
        SELECT 1 FROM message_correction_facts existing JOIN messages owner ON owner.id=existing.message_id
          AND owner.account_id=parent.account_id AND owner.conversation_id=parent.conversation_id
        WHERE existing.scope=fact.scope AND existing.author=fact.author AND existing.remote_id=fact.remote_id
          AND existing.message_id<>child.id
    )
)
INSERT INTO restore_messages
SELECT id,is_new FROM retained_edits WHERE id IS NOT NULL
ON CONFLICT DO NOTHING;

DELETE FROM restore_messages
WHERE is_new=1 AND EXISTS (
  SELECT 1 FROM backup.message_canonical_aliases a WHERE a.alias_id=restore_messages.id
  AND NOT EXISTS(SELECT 1 FROM restore_messages root WHERE root.id=a.canonical_id)
  AND NOT EXISTS(SELECT 1 FROM messages root WHERE root.id=a.canonical_id)
  AND NOT EXISTS (
    SELECT 1 FROM restore_verified_correction_roots e
    JOIN backup.message_correction_facts f ON f.message_id=a.alias_id AND f.scope=e.scope AND f.author=e.author
    WHERE e.source_id=a.canonical_id AND f.target_id<>''
  )
);

CREATE TEMP VIEW restore_own_echo_aliases AS
SELECT echo_id,original_id FROM restore_witness_echoes
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
  WHERE EXISTS (
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

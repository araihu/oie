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

-- Earlier direct observations could keep the sender reference while taking
-- the origin from the ordinary provider ID. Its exact scoped reference can
-- prevent a resend without being sufficient to identify the wire contents.
-- Multiple exact references rule out the guessed provider but prove no origin.
CREATE TEMP TABLE restore_legacy_direct_origins (
  is_backup INTEGER NOT NULL,id TEXT NOT NULL,account_id TEXT NOT NULL,conversation_id TEXT NOT NULL,
  provider_id TEXT NOT NULL,wire_id TEXT NOT NULL,PRIMARY KEY(is_backup,id)
) WITHOUT ROWID;
INSERT INTO restore_legacy_direct_origins
WITH candidates(is_backup,id,account_id,conversation_id,provider_id,payload,remote_id,source_hash) AS (
  SELECT 1,m.id,m.account_id,m.conversation_id,m.provider_id,m.payload,r.remote_id,r.source_hash
  FROM backup.messages m JOIN restore_chats x ON x.id=m.conversation_id
  JOIN backup.conversations c ON c.id=x.id AND c.account_id=m.account_id
  JOIN backup.accounts a ON a.id=m.account_id
  JOIN backup.message_references r ON r.message_id=m.id AND r.scope='sender:'||a.address AND r.author=a.address
  WHERE json_extract(c.payload,'$.Kind')='direct' AND m.is_own=1 AND m.state='submitted' AND m.id<>m.provider_id
    AND COALESCE(json_extract(m.payload,'$.OwnOriginID'),'') IN ('',m.provider_id)
    AND NOT EXISTS (
        SELECT 1 FROM backup.message_correction_facts f WHERE f.message_id=m.id AND f.target_id<>''
    )
  UNION ALL
  SELECT 0,m.id,m.account_id,m.conversation_id,m.provider_id,m.payload,r.remote_id,r.source_hash
  FROM messages m JOIN restore_chats x ON x.id=m.conversation_id
  JOIN conversations c ON c.id=x.id AND c.account_id=m.account_id
  JOIN accounts a ON a.id=m.account_id
  JOIN message_references r ON r.message_id=m.id AND r.scope='sender:'||a.address AND r.author=a.address
  WHERE json_extract(c.payload,'$.Kind')='direct' AND m.is_own=1 AND m.state='submitted' AND m.id<>m.provider_id
    AND COALESCE(json_extract(m.payload,'$.OwnOriginID'),'') IN ('',m.provider_id)
    AND NOT EXISTS (
        SELECT 1 FROM message_correction_facts f WHERE f.message_id=m.id AND f.target_id<>''
    )
), exact AS (
  SELECT is_backup,id,account_id,conversation_id,provider_id,MIN(remote_id) AS wire_id,
         COUNT(DISTINCT remote_id) AS reference_count
  FROM candidates
  WHERE source_hash=COALESCE(NULLIF(json_extract(payload,'$.WireBodySHA256'),''),
                             oie_source_hash(json_extract(payload,'$.OriginalText')))
  GROUP BY is_backup,id,account_id,conversation_id,provider_id
)
SELECT is_backup,id,account_id,conversation_id,provider_id,CASE WHEN reference_count=1 THEN wire_id ELSE '' END
FROM exact WHERE reference_count>1 OR (wire_id<>provider_id
   AND wire_id GLOB '????????-????-7???-[89ab]???-????????????'
   AND length(replace(wire_id,'-',''))=32 AND replace(wire_id,'-','') NOT GLOB '*[^0-9a-f]*');

-- Restore send evidence onto the original local row. An own archive echo
-- retained under another row ID would duplicate that message in the timeline.
CREATE TEMP VIEW restore_source_echoes AS
SELECT m.*,COALESCE(NULLIF(json_extract(m.payload,'$.OwnOriginID'),''), m.provider_id) AS wire_id
FROM backup.messages m JOIN restore_chats x ON x.id=m.conversation_id
JOIN backup.conversations c ON c.id=x.id AND c.account_id=m.account_id
WHERE m.is_own=1 AND m.state='submitted' AND m.id<>m.provider_id
  AND NOT EXISTS (SELECT 1 FROM restore_legacy_direct_origins old WHERE old.is_backup=1 AND old.id=m.id)
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
  AND oie_wire_mentions_match(d.payload,json_extract(m.payload,'$.WireMentions'),json_extract(m.payload,'$.WireMentionDigest'))=1
  -- A submitted correction can also use its native acceptance ID as the
  -- provider key. A conflicting body or correction target must stay visible
  -- to the identity checker instead of being discarded as an ordinary echo.
  AND NOT EXISTS (
    SELECT 1 FROM backup.message_correction_facts observed WHERE observed.message_id=m.id AND observed.target_id<>''
      AND NOT EXISTS (
        SELECT 1 FROM message_correction_facts retained
        JOIN message_revisions local_proof ON local_proof.id=retained.source_revision_id AND local_proof.entity_id=d.id
        JOIN backup.message_revisions backup_proof ON backup_proof.id=observed.source_revision_id AND backup_proof.entity_id=m.id
        WHERE retained.message_id=d.id AND retained.scope=observed.scope AND retained.author=observed.author
          AND retained.remote_id=observed.remote_id AND retained.target_id=observed.target_id
          AND retained.source_hash=observed.source_hash
          AND retained.source_hash=COALESCE(NULLIF(json_extract(d.payload,'$.WireBodySHA256'),''),
                                           oie_source_hash(json_extract(d.payload,'$.OriginalText')))
          AND retained.source_hash=COALESCE(NULLIF(json_extract(local_proof.payload,'$.WireBodySHA256'),''),
                                           oie_source_hash(json_extract(local_proof.payload,'$.OriginalText')))
          AND observed.source_hash=COALESCE(NULLIF(json_extract(m.payload,'$.WireBodySHA256'),''),
                                           oie_source_hash(json_extract(m.payload,'$.OriginalText')))
          AND observed.source_hash=COALESCE(NULLIF(json_extract(backup_proof.payload,'$.WireBodySHA256'),''),
                                           oie_source_hash(json_extract(backup_proof.payload,'$.OriginalText')))
      )
  )
  AND NOT EXISTS (
    SELECT 1 FROM message_correction_facts retained WHERE retained.message_id=d.id AND retained.target_id<>''
      AND NOT EXISTS (
        SELECT 1 FROM backup.message_correction_facts observed WHERE observed.message_id=m.id
          AND observed.scope=retained.scope AND observed.author=retained.author AND observed.remote_id=retained.remote_id
          AND observed.target_id=retained.target_id AND observed.source_hash=retained.source_hash
      )
  )
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
    AND NOT EXISTS (SELECT 1 FROM restore_legacy_direct_origins old WHERE old.is_backup=0 AND old.id=d.id)
  UNION ALL
  SELECT b.id,b.account_id,b.conversation_id,b.provider_id,b.payload,
         json_extract(c.payload,'$.Kind') AS kind
  FROM backup.messages b JOIN restore_messages selected ON selected.id=b.id AND selected.is_new=1
  JOIN backup.message_canonical_aliases a ON a.alias_id=b.id
  JOIN backup.conversations c ON c.id=b.conversation_id AND c.account_id=b.account_id
  WHERE b.is_own=1 AND b.state='submitted' AND b.id<>b.provider_id
    AND NOT EXISTS (SELECT 1 FROM restore_legacy_direct_origins old WHERE old.is_backup=1 AND old.id=b.id)
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
    AND NOT EXISTS (SELECT 1 FROM backup.message_correction_facts f WHERE f.message_id=b.id AND f.target_id<>'')
    AND (d.alias_id IS NULL OR d.canonical_id=b.id)
    AND NOT EXISTS(SELECT 1 FROM backup.message_canonical_aliases a WHERE a.alias_id=b.id)
    AND NOT EXISTS(SELECT 1 FROM message_canonical_aliases a WHERE a.alias_id=b.id)
    AND json_type(e.payload,'$.OriginalText')='text' AND json_type(b.payload,'$.OriginalText')='text'
    AND oie_wire_mentions_match(b.payload,json_extract(e.payload,'$.WireMentions'),json_extract(e.payload,'$.WireMentionDigest'))=1
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
  WHERE selected.is_new=0 AND (
    (archived.is_own=1 AND archived.id=archived.provider_id AND archived.state='submitted'
     AND retained.is_own=1 AND retained.id=retained.provider_id AND retained.state='submitted')
    OR (archived.is_own=0 AND archived.state='received' AND retained.is_own=0 AND retained.state='received'
        AND archived.provider_id=retained.provider_id AND archived.provider_id<>''
        AND archived.revision_id=retained.revision_id)
  )
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
  AND (e.source_id=e.retained_id OR oie_wire_mentions_match(native.payload,json_extract(echo.payload,'$.WireMentions'),json_extract(echo.payload,'$.WireMentionDigest'))=1)
  AND f.source_hash=COALESCE(NULLIF(json_extract(echo.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(echo.payload,'$.OriginalText')))
  AND f.source_hash=COALESCE(NULLIF(json_extract(archived.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(archived.payload,'$.OriginalText')))
  AND f.source_hash=COALESCE(NULLIF(json_extract(native.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(native.payload,'$.OriginalText')))
  AND f.source_hash=COALESCE(NULLIF(json_extract(retained.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(retained.payload,'$.OriginalText')))
  AND NOT EXISTS (
    SELECT 1 FROM message_correction_facts known
    WHERE known.message_id=native.id AND known.source_hash=f.source_hash
      AND (known.scope<>f.scope OR known.author<>f.author OR known.remote_id<>f.remote_id OR known.target_id<>'')
  )
  AND NOT EXISTS (
    SELECT 1 FROM message_correction_facts existing
    JOIN messages owner ON owner.id=existing.message_id
      AND owner.account_id=native.account_id AND owner.conversation_id=native.conversation_id
    LEFT JOIN message_canonical_aliases alias ON alias.alias_id=owner.id
    WHERE existing.scope=f.scope AND existing.author=f.author AND existing.remote_id=f.remote_id
      AND COALESCE(alias.canonical_id,owner.id)<>e.retained_id
  );

-- A room minimum or a local star can keep an original beyond the cutoff.
-- Track verified correction links even through excluded intermediates so
-- the normal age window cannot expose their descendants as new sources.
CREATE TEMP TABLE restore_retained_correction_sources AS
WITH RECURSIVE retained_edits(source_id,scope,author,remote_id,account_id,conversation_id,id,is_new,allowed) AS (
  SELECT root.source_id,root.scope,root.author,root.remote_id,native.account_id,native.conversation_id,NULL,0,
    NOT EXISTS(SELECT 1 FROM expired_message_ids e WHERE e.id IN (root.source_id,root.retained_id))
    AND NOT EXISTS (
        SELECT 1 FROM expired_message_references expired
        WHERE expired.conversation_id=chat.id AND expired.scope=root.scope
          AND expired.remote_id=root.remote_id AND expired.author=root.author
    )
  FROM restore_verified_correction_roots root
  JOIN messages native ON native.id=root.retained_id AND native.state IN ('received','submitted')
  JOIN conversations chat ON chat.id=native.conversation_id
  WHERE json_extract(chat.payload,'$.Kind')='room' OR EXISTS (
      SELECT 1 FROM message_stars star WHERE star.starred=1 AND (
        star.message_id=native.id OR star.message_id IN (
            SELECT alias_id FROM message_canonical_aliases WHERE canonical_id=native.id
        )
      )
  )
  UNION
  SELECT parent.source_id,parent.scope,parent.author,fact.remote_id,parent.account_id,parent.conversation_id,
      archived.id,COALESCE(child.is_new,1),
      parent.allowed AND child.id IS NOT NULL
      AND NOT EXISTS(SELECT 1 FROM expired_message_ids e WHERE e.id=archived.id)
      AND NOT EXISTS (
          SELECT 1 FROM expired_message_references expired
          WHERE expired.conversation_id=parent.conversation_id AND (
            (expired.scope=fact.scope AND expired.remote_id=fact.remote_id AND expired.author=fact.author)
            OR EXISTS (
                SELECT 1 FROM backup.message_references ref WHERE ref.message_id=archived.id
                  AND ref.scope=expired.scope AND ref.remote_id=expired.remote_id AND ref.author=expired.author
            )
          )
      )
  FROM retained_edits parent
  JOIN backup.message_correction_facts fact ON fact.scope=parent.scope AND fact.author=parent.author AND fact.target_id=parent.remote_id
  JOIN backup.message_canonical_aliases alias ON alias.alias_id=fact.message_id AND alias.canonical_id=parent.source_id
  JOIN backup.messages archived ON archived.id=alias.alias_id AND archived.account_id=parent.account_id
    AND archived.conversation_id=parent.conversation_id
  LEFT JOIN restore_message_candidates child ON child.id=archived.id
  JOIN backup.message_revisions revision ON revision.id=fact.source_revision_id AND revision.entity_id=archived.id
  WHERE fact.remote_id<>''
    AND fact.source_hash=COALESCE(NULLIF(json_extract(archived.payload,'$.WireBodySHA256'),''),
                                 oie_source_hash(json_extract(archived.payload,'$.OriginalText')))
    AND fact.source_hash=COALESCE(NULLIF(json_extract(revision.payload,'$.WireBodySHA256'),''),
                                 oie_source_hash(json_extract(revision.payload,'$.OriginalText')))
    AND NOT EXISTS (
        SELECT 1 FROM message_correction_facts existing JOIN messages owner ON owner.id=existing.message_id
          AND owner.account_id=parent.account_id AND owner.conversation_id=parent.conversation_id
        WHERE existing.scope=fact.scope AND existing.author=fact.author AND existing.remote_id=fact.remote_id
          AND existing.message_id<>archived.id
    )
)
SELECT id,MIN(is_new) AS is_new,MAX(allowed) AS allowed FROM retained_edits
WHERE id IS NOT NULL GROUP BY id;

DELETE FROM restore_messages
WHERE is_new=1 AND id IN (SELECT id FROM restore_retained_correction_sources WHERE allowed=0);

INSERT INTO restore_messages
SELECT id,is_new FROM restore_retained_correction_sources WHERE allowed=1
ON CONFLICT DO NOTHING;

-- Explicit expiry also applies when the root is within the normal age window
-- or is being imported for the first time. Keep a deleted branch from becoming
-- independent history simply because an intermediate correction is absent.
WITH RECURSIVE verified_corrections AS MATERIALIZED (
  SELECT m.id,m.account_id,m.conversation_id,COALESCE(a.canonical_id,m.id) AS root_id,
         f.scope,f.author,f.remote_id,f.target_id,f.source_hash
  FROM backup.message_correction_facts f
  JOIN backup.messages m ON m.id=f.message_id AND m.state IN ('received','submitted')
  JOIN restore_chats x ON x.id=m.conversation_id
  JOIN backup.conversations chat ON chat.id=x.id AND chat.account_id=m.account_id
  JOIN backup.message_revisions revision ON revision.id=f.source_revision_id AND revision.entity_id=m.id
  LEFT JOIN backup.message_canonical_aliases a ON a.alias_id=m.id
  WHERE f.scope<>'' AND f.author<>'' AND f.remote_id<>''
    AND (EXISTS(SELECT 1 FROM expired_message_ids) OR EXISTS (
      SELECT 1 FROM expired_message_references e WHERE e.conversation_id=m.conversation_id
    ))
    AND json_extract(m.payload,'$.ID')=m.id AND json_extract(revision.payload,'$.ID')=m.id
    AND json_extract(revision.payload,'$.RevisionID')=revision.id
    AND json_extract(m.payload,'$.AccountID')=m.account_id AND json_extract(revision.payload,'$.AccountID')=m.account_id
    AND json_extract(m.payload,'$.ConversationID')=m.conversation_id
    AND json_extract(revision.payload,'$.ConversationID')=m.conversation_id
    AND f.source_hash=COALESCE(NULLIF(json_extract(m.payload,'$.WireBodySHA256'),''),
                               oie_source_hash(json_extract(m.payload,'$.OriginalText')))
    AND f.source_hash=COALESCE(NULLIF(json_extract(revision.payload,'$.WireBodySHA256'),''),
                               oie_source_hash(json_extract(revision.payload,'$.OriginalText')))
), expired_branches(id,account_id,conversation_id,root_id,scope,author,remote_id) AS (
  SELECT f.id,f.account_id,f.conversation_id,f.root_id,f.scope,f.author,f.remote_id
  FROM verified_corrections f
  WHERE EXISTS(SELECT 1 FROM expired_message_ids e WHERE e.id IN (f.id,f.root_id))
     OR EXISTS (
        SELECT 1 FROM expired_message_references e WHERE e.conversation_id=f.conversation_id AND (
          (e.scope=f.scope AND e.author=f.author AND e.remote_id IN (f.remote_id,f.target_id))
          OR EXISTS (
              SELECT 1 FROM backup.message_references ref WHERE ref.message_id=f.id AND ref.source_hash=f.source_hash
                AND ref.scope=e.scope AND ref.remote_id=e.remote_id AND ref.author=e.author
          )
        )
     )
  UNION
  SELECT child.id,child.account_id,child.conversation_id,child.root_id,child.scope,child.author,child.remote_id
  FROM expired_branches parent JOIN verified_corrections child
    ON child.account_id=parent.account_id AND child.conversation_id=parent.conversation_id
    AND child.root_id=parent.root_id AND child.scope=parent.scope AND child.author=parent.author
    AND child.target_id=parent.remote_id
)
DELETE FROM restore_messages
WHERE is_new=1 AND id IN (SELECT id FROM expired_branches);

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

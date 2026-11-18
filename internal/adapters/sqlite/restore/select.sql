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

CREATE TEMP TABLE restore_messages (
  id TEXT PRIMARY KEY,is_new INTEGER NOT NULL,within_retention INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;
INSERT INTO restore_messages
WITH eligibility AS (
  SELECT m.id,m.is_new,m.days=0 OR m.age_ms>=@now_ms - 86400000 * m.days OR EXISTS (
       SELECT 1 FROM backup.message_stars s WHERE s.starred=1 AND (
         s.message_id=COALESCE(m.canonical_id,m.id)
         OR s.message_id IN (SELECT al.alias_id FROM backup.message_canonical_aliases al WHERE al.canonical_id=COALESCE(m.canonical_id,m.id))
       )
  ) AS within_retention
  FROM restore_message_candidates m
)
SELECT id,is_new,within_retention FROM eligibility WHERE is_new=0 OR within_retention;

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
-- Keep both immutable source rows for one verified idless direct stanza.
CREATE TEMP TABLE restore_idless_direct_echoes (
  source_id TEXT PRIMARY KEY, retained_id TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO restore_idless_direct_echoes
WITH pairs AS MATERIALIZED (
  SELECT m.id AS source_id,d.id AS retained_id,
         local_alias.canonical_id IS NOT NULL OR EXISTS (
           SELECT 1 FROM backup.archive_aliases incoming
           JOIN archive_aliases local ON local.account_id=incoming.account_id
             AND local.scope=incoming.scope AND local.archive_id=incoming.archive_id
           LEFT JOIN message_canonical_aliases owner ON owner.alias_id=local.message_id
           WHERE incoming.message_id=m.id AND incoming.account_id=m.account_id AND incoming.scope=a.address
             AND COALESCE(owner.canonical_id,local.message_id)=d.id
         ) AS established
  FROM restore_message_candidates selected
  JOIN backup.messages m ON m.id=selected.id
  JOIN conversations chat ON chat.id=m.conversation_id AND chat.account_id=m.account_id
  JOIN accounts a ON a.id=m.account_id
  JOIN backup.message_references observed ON observed.message_id=m.id
    AND observed.scope='sender:'||a.address AND observed.author=a.address
    AND observed.remote_id=json_extract(m.payload,'$.OwnOriginID')
    AND observed.source_hash=COALESCE(NULLIF(json_extract(m.payload,'$.WireBodySHA256'),''),
                                     oie_source_hash(json_extract(m.payload,'$.OriginalText')))
  JOIN message_references retained ON retained.scope=observed.scope AND retained.author=observed.author
    AND retained.remote_id=observed.remote_id AND retained.source_hash=observed.source_hash
  JOIN messages d ON d.id=retained.message_id AND d.id<>m.id
    AND d.conversation_id=m.conversation_id AND d.account_id=m.account_id
  LEFT JOIN message_canonical_aliases local_alias ON local_alias.alias_id=m.id
  LEFT JOIN backup.message_replies observed_reply ON observed_reply.message_id=m.id AND observed_reply.body_hash=observed.source_hash
  LEFT JOIN message_replies retained_reply ON retained_reply.message_id=d.id AND retained_reply.body_hash=retained.source_hash
  WHERE (selected.is_new=1 OR local_alias.canonical_id=d.id)
    AND json_extract(chat.payload,'$.Kind')='direct'
    AND m.is_own=1 AND d.is_own=1 AND m.state='submitted' AND d.state='submitted'
    AND (m.provider_id='' OR m.provider_id GLOB 'archive:*')
    AND (d.provider_id='' OR d.provider_id GLOB 'archive:*')
    AND retained.source_hash=COALESCE(NULLIF(json_extract(d.payload,'$.WireBodySHA256'),''),
                                     oie_source_hash(json_extract(d.payload,'$.OriginalText')))
    AND NOT EXISTS (SELECT 1 FROM backup.message_canonical_aliases alias WHERE alias.alias_id=m.id)
    AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases alias WHERE alias.alias_id=d.id)
    AND NOT EXISTS (SELECT 1 FROM backup.message_correction_facts fact WHERE fact.message_id=m.id AND fact.target_id<>'')
    AND NOT EXISTS (SELECT 1 FROM message_correction_facts fact WHERE fact.message_id=d.id AND fact.target_id<>'')
    AND oie_same_direct_echo(m.payload,observed_reply.payload,d.payload,retained_reply.payload)=1
    AND NOT EXISTS (
      SELECT 1 FROM backup.archive_aliases incoming
      JOIN archive_aliases local ON local.account_id=incoming.account_id
        AND local.scope=incoming.scope AND local.archive_id=incoming.archive_id
      LEFT JOIN message_canonical_aliases owner ON owner.alias_id=local.message_id
      WHERE incoming.message_id=m.id AND COALESCE(owner.canonical_id,local.message_id)<>d.id
    )
), preferred AS (
  SELECT p.* FROM pairs p
  WHERE p.established OR NOT EXISTS (
    SELECT 1 FROM pairs known WHERE known.established
      AND (known.source_id=p.source_id OR known.retained_id=p.retained_id)
  )
), counted AS (
  SELECT p.*,COUNT(*) OVER (PARTITION BY p.source_id) AS retained_count,
         COUNT(*) OVER (PARTITION BY p.retained_id) AS source_count
  FROM preferred p
)
SELECT source_id,retained_id FROM counted
WHERE retained_count=1 AND (established OR source_count=1);

-- Verify and flatten relayed aliases before applying the age window to
-- any physical rows that carry the retained star's causal history.
INSERT INTO restore_idless_direct_echoes
SELECT source.id,COALESCE(mapped.retained_id,root.id)
FROM backup.message_canonical_aliases edge
JOIN backup.messages source ON source.id=edge.alias_id AND source.is_own=1 AND source.state='submitted'
JOIN restore_message_candidates candidate ON candidate.id=source.id
JOIN backup.messages root ON root.id=edge.canonical_id AND root.is_own=1 AND root.state='submitted'
  AND root.account_id=source.account_id AND root.conversation_id=source.conversation_id
JOIN restore_message_candidates root_candidate ON root_candidate.id=root.id
LEFT JOIN restore_messages selected ON selected.id=source.id
LEFT JOIN restore_messages selected_root ON selected_root.id=root.id
JOIN backup.conversations chat ON chat.id=root.conversation_id AND chat.account_id=root.account_id
JOIN backup.accounts account ON account.id=root.account_id
JOIN backup.message_references sr ON sr.message_id=source.id AND sr.scope='sender:'||account.address AND sr.author=account.address
  AND sr.remote_id=json_extract(source.payload,'$.OwnOriginID')
  AND sr.source_hash=COALESCE(NULLIF(json_extract(source.payload,'$.WireBodySHA256'),''),
                             oie_source_hash(json_extract(source.payload,'$.OriginalText')))
JOIN backup.message_references rr ON rr.message_id=root.id AND rr.scope=sr.scope AND rr.author=sr.author
  AND rr.remote_id=sr.remote_id AND rr.source_hash=sr.source_hash
LEFT JOIN backup.message_replies sp ON sp.message_id=source.id AND sp.body_hash=sr.source_hash
LEFT JOIN backup.message_replies rp ON rp.message_id=root.id AND rp.body_hash=rr.source_hash
LEFT JOIN restore_idless_direct_echoes mapped ON mapped.source_id=root.id
LEFT JOIN message_canonical_aliases known ON known.alias_id=source.id
LEFT JOIN messages local ON local.id=root.id AND local.account_id=root.account_id AND local.conversation_id=root.conversation_id
LEFT JOIN message_replies lp ON lp.message_id=local.id AND lp.body_hash=rr.source_hash
WHERE json_extract(chat.payload,'$.Kind')='direct'
  AND (candidate.is_new=1 OR known.canonical_id=COALESCE(mapped.retained_id,root.id))
  AND (
    (selected.id IS NOT NULL AND selected_root.id IS NOT NULL) OR mapped.retained_id IS NOT NULL
    OR (local.id IS NOT NULL AND oie_same_direct_echo(root.payload,rp.payload,local.payload,lp.payload)=1 AND EXISTS (
      SELECT 1 FROM message_references ref WHERE ref.message_id=local.id AND ref.scope=rr.scope
        AND ref.remote_id=rr.remote_id AND ref.author=rr.author AND ref.source_hash=rr.source_hash
    ))
  )
  AND COALESCE(mapped.retained_id,root.id)<>source.id
  AND NOT EXISTS (SELECT 1 FROM backup.message_canonical_aliases edge WHERE edge.alias_id=root.id)
  AND NOT EXISTS (SELECT 1 FROM message_canonical_aliases edge WHERE edge.alias_id=COALESCE(mapped.retained_id,root.id))
  AND NOT EXISTS (SELECT 1 FROM backup.message_correction_facts fact WHERE fact.message_id IN (source.id,root.id) AND fact.target_id<>'')
  AND oie_same_direct_echo(source.payload,sp.payload,root.payload,rp.payload)=1;

CREATE TEMP VIEW restore_idless_star_aliases AS
SELECT alias.id AS message_id,root.id AS canonical_id
FROM message_canonical_aliases link
JOIN messages root ON root.id=link.canonical_id AND root.is_own=1 AND root.state='submitted'
JOIN messages alias ON alias.id=link.alias_id AND alias.account_id=root.account_id
  AND alias.conversation_id=root.conversation_id AND alias.is_own=1 AND alias.state='submitted' AND alias.provider_id=''
JOIN conversations chat ON chat.id=root.conversation_id AND json_extract(chat.payload,'$.Kind')='direct'
WHERE root.id<>json_extract(root.payload,'$.OwnOriginID') AND json_extract(root.payload,'$.OwnOriginID')<>''
  AND json_extract(alias.payload,'$.OwnOriginID')=json_extract(root.payload,'$.OwnOriginID');

-- Carry just the physical rows that establish the path from an archived
-- unstar to the retained head. Keep the ancestry available for re-export.
-- An alias already retained by this exception still has the same cutoff on
-- a repeat. A normal age-eligible source can contribute its full history.
CREATE TEMP TABLE restore_idless_star_restricted (message_id TEXT PRIMARY KEY) WITHOUT ROWID;
INSERT INTO restore_idless_star_restricted
SELECT e.source_id FROM restore_idless_direct_echoes e JOIN messages retained ON retained.id=e.retained_id
LEFT JOIN restore_messages selected ON selected.id=e.source_id
WHERE COALESCE(selected.within_retention,0)=0;

CREATE TEMP TABLE restore_idless_star_age (
  message_id TEXT NOT NULL,revision_id TEXT NOT NULL,PRIMARY KEY(message_id,revision_id)
) WITHOUT ROWID;
INSERT INTO restore_idless_star_age
WITH RECURSIVE
roots AS MATERIALIZED (
  SELECT DISTINCT e.retained_id AS canonical_id
  FROM restore_idless_direct_echoes e JOIN restore_idless_star_restricted limited ON limited.message_id=e.source_id
),
current_heads AS MATERIALIZED (
  SELECT root.canonical_id,(
    SELECT s.revision_id FROM message_stars s
    WHERE s.message_id=root.canonical_id OR s.message_id IN (
      SELECT alias.message_id FROM restore_idless_star_aliases alias WHERE alias.canonical_id=root.canonical_id
    )
    ORDER BY s.updated_at DESC,s.revision_id DESC LIMIT 1
  ) AS revision_id
  FROM roots root
),
archived_members AS MATERIALIZED (
  SELECT e.source_id AS message_id,e.retained_id AS canonical_id
  FROM restore_idless_direct_echoes e JOIN current_heads h ON h.canonical_id=e.retained_id AND h.revision_id IS NOT NULL
  UNION
  SELECT h.canonical_id,h.canonical_id FROM current_heads h
  JOIN restore_message_candidates candidate ON candidate.id=h.canonical_id
  WHERE h.revision_id IS NOT NULL
),
retained_members AS MATERIALIZED (
  SELECT alias.message_id,alias.canonical_id
  FROM restore_idless_star_aliases alias JOIN current_heads h ON h.canonical_id=alias.canonical_id
  WHERE h.revision_id IS NOT NULL
  UNION ALL
  SELECT h.canonical_id,h.canonical_id FROM current_heads h WHERE h.revision_id IS NOT NULL
),
star_revisions AS MATERIALIZED (
  SELECT member.canonical_id,r.id,r.message_id,r.parents,r.payload,r.created_at
  FROM archived_members member JOIN backup.message_star_revisions r ON r.message_id=member.message_id
  WHERE NOT EXISTS (SELECT 1 FROM message_star_revisions known WHERE known.id=r.id)
    AND json_extract(r.payload,'$.RevisionID')=r.id AND json_extract(r.payload,'$.MessageID')=r.message_id
  UNION ALL
  SELECT member.canonical_id,r.id,r.message_id,r.parents,r.payload,r.created_at
  FROM retained_members member JOIN message_star_revisions r ON r.message_id=member.message_id
),
edges AS MATERIALIZED (
  SELECT child.canonical_id,child.id AS child_id,parent.id AS parent_id
  FROM star_revisions child JOIN json_each(child.parents) edge ON edge.type='text'
  JOIN star_revisions parent ON parent.canonical_id=child.canonical_id AND parent.id=edge.value
),
descendants(canonical_id,revision_id) AS (
  SELECT canonical_id,revision_id FROM current_heads WHERE revision_id IS NOT NULL
  UNION
  SELECT d.canonical_id,e.child_id FROM descendants d JOIN edges e
    ON e.canonical_id=d.canonical_id AND e.parent_id=d.revision_id
),
keep(canonical_id,revision_id) AS (
  SELECT member.canonical_id,s.revision_id
  FROM archived_members member
  JOIN backup.message_stars s ON s.message_id=member.message_id AND s.starred=0
  JOIN star_revisions r ON r.id=s.revision_id AND r.message_id=s.message_id AND r.canonical_id=member.canonical_id
  JOIN descendants d ON d.canonical_id=r.canonical_id AND d.revision_id=r.id
  JOIN current_heads h ON h.canonical_id=r.canonical_id AND h.revision_id<>r.id
  WHERE r.payload=s.payload AND r.created_at=s.updated_at
    AND json_extract(s.payload,'$.RevisionID')=s.revision_id AND json_extract(s.payload,'$.MessageID')=s.message_id
    AND json_extract(s.payload,'$.Starred')=s.starred
  UNION
  SELECT k.canonical_id,e.parent_id FROM keep k
  JOIN current_heads h ON h.canonical_id=k.canonical_id AND h.revision_id<>k.revision_id
  JOIN edges e ON e.canonical_id=k.canonical_id AND e.child_id=k.revision_id
  JOIN descendants d ON d.canonical_id=e.canonical_id AND d.revision_id=e.parent_id
)
SELECT DISTINCT limited.message_id,r.id FROM keep k JOIN star_revisions r
  ON r.canonical_id=k.canonical_id AND r.id=k.revision_id
JOIN archived_members member ON member.canonical_id=r.canonical_id AND member.message_id=r.message_id
JOIN restore_idless_star_restricted limited ON limited.message_id=member.message_id;

INSERT INTO restore_messages(id,is_new)
SELECT DISTINCT message_id,1 FROM restore_idless_star_age WHERE TRUE ON CONFLICT DO NOTHING;

CREATE TEMP VIEW restore_admitted_star_revisions AS
SELECT r.* FROM backup.message_star_revisions r JOIN restore_messages selected ON selected.id=r.message_id
WHERE NOT EXISTS (SELECT 1 FROM restore_idless_star_restricted limited WHERE limited.message_id=r.message_id)
   OR EXISTS (SELECT 1 FROM restore_idless_star_age admitted WHERE admitted.message_id=r.message_id AND admitted.revision_id=r.id);

DELETE FROM restore_idless_direct_echoes
WHERE source_id IN (SELECT alias_id FROM backup.message_canonical_aliases)
  AND NOT EXISTS(SELECT 1 FROM restore_messages selected WHERE selected.id=source_id);

-- An older exact observation may add archive metadata to a retained message
-- without enrolling a second copy of its history outside the age window.
INSERT INTO restore_suppressed_echoes
SELECT source_id,retained_id FROM restore_idless_direct_echoes e
WHERE NOT EXISTS (SELECT 1 FROM restore_messages selected WHERE selected.id=e.source_id);

-- The destination can retain its own original beyond the archive cutoff.
-- The verified echo may still contribute metadata for that retained body.
INSERT INTO restore_suppressed_echoes
SELECT m.id,d.id FROM restore_source_echoes m
JOIN messages d ON d.id=m.wire_id AND d.id=d.provider_id
  AND d.account_id=m.account_id AND d.conversation_id=m.conversation_id AND d.is_own=1
LEFT JOIN backup.message_canonical_aliases a ON a.alias_id=m.id
LEFT JOIN backup.outbox o ON o.message_id=m.id
WHERE NOT EXISTS(SELECT 1 FROM messages existing WHERE existing.id=m.id)
  AND NOT EXISTS(SELECT 1 FROM restore_idless_direct_echoes matched WHERE matched.source_id=m.id)
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
        LEFT JOIN message_edit_commands local_edit ON local_edit.id=d.id AND local_edit.message_id=retained.target_id
          AND local_edit.account_id=d.account_id AND local_edit.conversation_id=d.conversation_id
        LEFT JOIN backup.message_replies observed_reply ON observed_reply.message_id=m.id AND observed_reply.body_hash=observed.source_hash
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
          AND (local_edit.id IS NULL OR oie_edit_wire_matches(local_edit.payload,m.payload,observed_reply.payload)=1)
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

INSERT INTO restore_messages(id,is_new)
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

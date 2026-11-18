-- A matching archive observation can enrich the retained source without
-- introducing a second correction, regardless of its persisted provider key.
INSERT INTO restore_suppressed_echoes
SELECT DISTINCT observation.id,native.id
FROM restore_messages selected
JOIN backup.messages observation ON observation.id=selected.id AND observation.is_own=1 AND observation.state='submitted'
JOIN conversations chat ON chat.id=observation.conversation_id AND chat.account_id=observation.account_id
JOIN backup.message_correction_facts observed ON observed.message_id=observation.id AND observed.target_id<>''
JOIN backup.message_revisions observed_revision ON observed_revision.id=observed.source_revision_id AND observed_revision.entity_id=observation.id
JOIN message_edit_commands command ON command.id=observed.remote_id AND command.message_id=observed.target_id
  AND command.account_id=observation.account_id AND command.conversation_id=observation.conversation_id
  AND command.state IN ('submitted','failed')
JOIN messages root ON root.id=command.message_id AND root.id=root.provider_id AND root.is_own=1
  AND root.account_id=command.account_id AND root.conversation_id=command.conversation_id
JOIN messages native ON native.is_own=1 AND native.state='submitted'
  AND native.account_id=command.account_id AND native.conversation_id=command.conversation_id
  AND (native.id=command.id AND native.id=native.provider_id OR native.provider_id IN (
      oie_room_correction_provider_id(observed.author,observed.remote_id),
      oie_scoped_room_correction_provider_id(observed.scope,observed.author,observed.remote_id)
  ))
LEFT JOIN message_canonical_aliases retained_parent ON retained_parent.alias_id=native.id
JOIN message_correction_facts known ON known.message_id=native.id AND known.remote_id=observed.remote_id
  AND known.target_id=observed.target_id AND known.scope=observed.scope AND known.author=observed.author
  AND known.source_hash=observed.source_hash
JOIN message_revisions native_revision ON native_revision.id=known.source_revision_id AND native_revision.entity_id=native.id
LEFT JOIN backup.message_replies observed_reply ON observed_reply.message_id=observation.id AND observed_reply.body_hash=observed.source_hash
LEFT JOIN message_replies native_reply ON native_reply.message_id=native.id AND native_reply.body_hash=known.source_hash
LEFT JOIN backup.message_canonical_aliases parent ON parent.alias_id=observation.id
WHERE selected.is_new=1 AND observation.id<>native.id AND json_extract(chat.payload,'$.Kind')='room'
  AND instr(observed.scope,'xmpp:correct:room:'||chat.address||':')=1 AND instr(observed.author,chat.address||'/')=1
  AND observation.provider_id IN (oie_room_correction_provider_id(observed.author,observed.remote_id),
                                  oie_scoped_room_correction_provider_id(observed.scope,observed.author,observed.remote_id))
  AND (retained_parent.alias_id IS NULL OR retained_parent.canonical_id=root.id)
  AND json_extract(command.payload,'$.Plan.Scope')=observed.scope AND json_extract(command.payload,'$.Plan.Author')=observed.author
  AND json_extract(command.payload,'$.Plan.OriginalWireID')=observed.target_id
  AND observed.source_hash=oie_edit_wire_hash(command.payload)
  AND oie_edit_wire_matches(command.payload,observation.payload,observed_reply.payload)=1
  AND oie_edit_wire_matches(command.payload,observed_revision.payload,observed_reply.payload)=1
  AND oie_edit_wire_matches(command.payload,native.payload,native_reply.payload)=1
  AND observed.source_hash=COALESCE(NULLIF(json_extract(observation.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(observation.payload,'$.OriginalText')))
  AND observed.source_hash=COALESCE(NULLIF(json_extract(observed_revision.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(observed_revision.payload,'$.OriginalText')))
  AND observed.source_hash=COALESCE(NULLIF(json_extract(native.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(native.payload,'$.OriginalText')))
  AND observed.source_hash=COALESCE(NULLIF(json_extract(native_revision.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(native_revision.payload,'$.OriginalText')))
  AND (parent.alias_id IS NULL OR parent.canonical_id=root.id OR EXISTS (
      SELECT 1 FROM restore_suppressed_echoes e WHERE e.source_id=parent.canonical_id AND e.retained_id=root.id
  ))
  AND NOT EXISTS (
    SELECT 1 FROM backup.message_correction_facts f WHERE f.message_id=observation.id
      AND (f.remote_id<>observed.remote_id OR f.target_id<>observed.target_id OR f.scope<>observed.scope
        OR f.author<>observed.author OR f.source_hash<>observed.source_hash)
  )
  AND NOT EXISTS (
    SELECT 1 FROM message_correction_facts f WHERE f.message_id=native.id
      AND (f.remote_id<>known.remote_id OR f.target_id<>known.target_id OR f.scope<>known.scope
        OR f.author<>known.author OR f.source_hash<>known.source_hash)
  )
ON CONFLICT DO NOTHING;

-- Independent observers can keep different physical rows for the same packet.
-- Match verified originals first so their corrections can agree on the root
-- even when neither observer has a local edit command.
CREATE TEMP TABLE restore_independent_room_observations AS
  SELECT source.id AS source_id,retained.id AS retained_id,observed.target_id='' AS is_root,
         parent.canonical_id AS source_root,retained_parent.canonical_id AS retained_root
  FROM restore_messages selected
  JOIN backup.messages source ON source.id=selected.id AND source.is_own=1 AND source.state='submitted'
  JOIN conversations chat ON chat.id=source.conversation_id AND chat.account_id=source.account_id
  JOIN backup.message_correction_facts observed ON observed.message_id=source.id
  JOIN backup.message_revisions observed_revision ON observed_revision.id=observed.source_revision_id AND observed_revision.entity_id=source.id
  JOIN message_correction_facts known ON known.scope=observed.scope AND known.author=observed.author
    AND known.remote_id=observed.remote_id AND known.target_id=observed.target_id AND known.source_hash=observed.source_hash
  JOIN messages retained ON retained.id=known.message_id AND retained.account_id=source.account_id
    AND retained.conversation_id=source.conversation_id AND retained.is_own=1 AND retained.state='submitted'
  JOIN message_revisions retained_revision ON retained_revision.id=known.source_revision_id AND retained_revision.entity_id=retained.id
  LEFT JOIN backup.message_replies observed_reply ON observed_reply.message_id=source.id AND observed_reply.body_hash=observed.source_hash
  LEFT JOIN message_replies retained_reply ON retained_reply.message_id=retained.id AND retained_reply.body_hash=known.source_hash
  LEFT JOIN backup.message_canonical_aliases parent ON parent.alias_id=source.id
  LEFT JOIN message_canonical_aliases retained_parent ON retained_parent.alias_id=retained.id
  WHERE json_extract(chat.payload,'$.Kind')='room'
    AND (selected.is_new=1 OR (observed.target_id='' AND EXISTS (
      SELECT 1 FROM message_canonical_aliases edge JOIN messages hidden ON hidden.id=edge.alias_id
      WHERE edge.alias_id=source.id AND edge.canonical_id=retained.id AND hidden.provider_id=''
    )))
    AND instr(observed.scope,'xmpp:correct:room:'||chat.address||':')=1 AND instr(observed.author,chat.address||'/')=1
    AND source.provider_id IN (oie_room_correction_provider_id(observed.author,observed.remote_id),
                                 oie_scoped_room_correction_provider_id(observed.scope,observed.author,observed.remote_id))
    AND retained.provider_id IN (oie_room_correction_provider_id(known.author,known.remote_id),
                                   oie_scoped_room_correction_provider_id(known.scope,known.author,known.remote_id))
    AND (observed.target_id<>'' OR (parent.alias_id IS NULL AND retained_parent.alias_id IS NULL
      AND source.provider_id=oie_room_correction_provider_id(observed.author,observed.remote_id)
      AND retained.provider_id=source.provider_id))
    AND NOT EXISTS (
      SELECT 1 FROM expired_message_references expired
      WHERE expired.conversation_id=chat.id AND expired.scope=observed.scope AND expired.author=observed.author
        AND expired.remote_id IN (observed.remote_id,observed.target_id)
    )
    AND observed.source_hash=COALESCE(NULLIF(json_extract(source.payload,'$.WireBodySHA256'),''),
                                      oie_source_hash(json_extract(source.payload,'$.OriginalText')))
    AND observed.source_hash=COALESCE(NULLIF(json_extract(observed_revision.payload,'$.WireBodySHA256'),''),
                                      oie_source_hash(json_extract(observed_revision.payload,'$.OriginalText')))
    AND known.source_hash=COALESCE(NULLIF(json_extract(retained.payload,'$.WireBodySHA256'),''),
                                   oie_source_hash(json_extract(retained.payload,'$.OriginalText')))
    AND known.source_hash=COALESCE(NULLIF(json_extract(retained_revision.payload,'$.WireBodySHA256'),''),
                                   oie_source_hash(json_extract(retained_revision.payload,'$.OriginalText')))
    AND (
      oie_same_room_correction_wire(source.payload,observed_reply.payload,retained.payload,retained_reply.payload)=1
      OR (observed.target_id='' AND EXISTS (
        SELECT 1 FROM backup.archive_aliases incoming
        JOIN archive_aliases local ON local.account_id=incoming.account_id
          AND local.scope=incoming.scope AND local.archive_id=incoming.archive_id
        WHERE incoming.account_id=source.account_id AND incoming.scope=chat.address
          AND incoming.archive_id<>'' AND incoming.message_id=source.id AND local.message_id=retained.id
      ) AND oie_same_archived_room_original_wire(source.payload,observed_reply.payload,retained.payload,retained_reply.payload)=1)
    )
    AND (SELECT COUNT(*) FROM backup.message_correction_facts fact WHERE fact.message_id=source.id)=1
    AND (SELECT COUNT(*) FROM message_correction_facts fact WHERE fact.message_id=retained.id)=1;

CREATE TEMP TABLE restore_room_original_aliases (
  source_id TEXT PRIMARY KEY,retained_id TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO restore_room_original_aliases
SELECT source_id,MIN(retained_id) FROM restore_independent_room_observations
WHERE is_root=1 GROUP BY source_id HAVING COUNT(DISTINCT retained_id)=1
ON CONFLICT DO NOTHING;

-- A re-export records the physical observation without a second correction
-- fact. Validate the original provider and wire body against its root. The
-- archive key of the observation is kept on that proven canonical root.
INSERT INTO restore_room_original_aliases
SELECT source.id,COALESCE(matched.retained_id,root.id)
FROM backup.message_canonical_aliases edge
JOIN backup.messages source ON source.id=edge.alias_id AND source.is_own=1 AND source.state='submitted'
JOIN restore_messages selected ON selected.id=source.id
JOIN backup.messages root ON root.id=edge.canonical_id AND root.account_id=source.account_id
  AND root.conversation_id=source.conversation_id AND root.is_own=1 AND root.state='submitted'
JOIN restore_messages selected_root ON selected_root.id=root.id
JOIN backup.conversations chat ON chat.id=root.conversation_id AND chat.account_id=root.account_id
JOIN backup.message_revisions original ON original.id=source.revision_id AND original.entity_id=source.id
JOIN backup.message_correction_facts fact ON fact.message_id=root.id AND fact.target_id=''
JOIN backup.message_revisions proof ON proof.id=fact.source_revision_id AND proof.entity_id=root.id
JOIN backup.archive_aliases archived ON archived.account_id=source.account_id AND archived.scope=chat.address
  AND archived.archive_id=json_extract(source.payload,'$.ArchiveID') AND archived.message_id=root.id
LEFT JOIN backup.message_replies source_reply ON source_reply.message_id=source.id AND source_reply.body_hash=fact.source_hash
LEFT JOIN backup.message_replies root_reply ON root_reply.message_id=root.id AND root_reply.body_hash=fact.source_hash
LEFT JOIN restore_room_original_aliases matched ON matched.source_id=root.id
LEFT JOIN message_canonical_aliases retained ON retained.alias_id=source.id
WHERE json_extract(chat.payload,'$.Kind')='room'
  AND source.provider_id='' AND archived.archive_id<>'' AND json_extract(source.payload,'$.ArchiveScope')=chat.address
  AND instr(fact.scope,'xmpp:correct:room:'||chat.address||':')=1 AND instr(fact.author,chat.address||'/')=1
  AND root.provider_id=oie_room_correction_provider_id(fact.author,fact.remote_id)
  AND json_extract(original.payload,'$.ProviderID')=root.provider_id
  AND fact.source_hash=COALESCE(NULLIF(json_extract(original.payload,'$.WireBodySHA256'),''),
                                 oie_source_hash(json_extract(original.payload,'$.OriginalText')))
  AND fact.source_hash=COALESCE(NULLIF(json_extract(proof.payload,'$.WireBodySHA256'),''),
                                 oie_source_hash(json_extract(proof.payload,'$.OriginalText')))
  AND fact.source_hash=COALESCE(NULLIF(json_extract(root.payload,'$.WireBodySHA256'),''),
                                 oie_source_hash(json_extract(root.payload,'$.OriginalText')))
  AND (SELECT COUNT(*) FROM backup.message_correction_facts known WHERE known.message_id=root.id)=1
  AND NOT EXISTS(SELECT 1 FROM backup.message_correction_facts known WHERE known.message_id=source.id)
  AND NOT EXISTS(SELECT 1 FROM backup.message_canonical_aliases child WHERE child.alias_id=root.id)
  AND COALESCE(matched.retained_id,root.id)<>source.id
  AND (selected.is_new=1 OR retained.canonical_id=COALESCE(matched.retained_id,root.id))
  AND oie_same_archived_room_original_wire(source.payload,source_reply.payload,root.payload,root_reply.payload)=1
ON CONFLICT DO NOTHING;

INSERT INTO restore_suppressed_echoes
SELECT source_id,retained_id FROM restore_independent_room_observations
WHERE is_root=1 AND EXISTS (
  SELECT 1 FROM restore_room_original_aliases a
  WHERE a.source_id=restore_independent_room_observations.source_id
    AND a.retained_id=restore_independent_room_observations.retained_id
)
ON CONFLICT DO NOTHING;

INSERT INTO restore_suppressed_echoes
SELECT source_id,MIN(retained_id) FROM restore_independent_room_observations candidate
WHERE is_root=0 AND (source_root IS NULL OR retained_root IS NULL OR source_root=retained_root OR EXISTS (
  SELECT 1 FROM restore_room_original_aliases root WHERE root.source_id=candidate.source_root AND root.retained_id=candidate.retained_root
) OR EXISTS (
  SELECT 1 FROM restore_suppressed_echoes root WHERE root.source_id=candidate.source_root AND root.retained_id=candidate.retained_root
))
GROUP BY source_id HAVING COUNT(DISTINCT retained_id)=1
ON CONFLICT DO NOTHING;

DROP TABLE restore_independent_room_observations;

DELETE FROM restore_messages WHERE id IN (SELECT source_id FROM restore_suppressed_echoes)
  AND id NOT IN (SELECT source_id FROM restore_room_original_aliases);

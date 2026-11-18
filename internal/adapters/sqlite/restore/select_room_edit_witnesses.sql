-- When the native edit arrived first, the archive observation can add its
-- room identifiers to that source without introducing a second correction.
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
JOIN messages native ON native.id=command.id AND native.id=native.provider_id AND native.is_own=1 AND native.state='submitted'
  AND native.account_id=command.account_id AND native.conversation_id=command.conversation_id
JOIN message_correction_facts known ON known.message_id=native.id AND known.remote_id=observed.remote_id
  AND known.target_id=observed.target_id AND known.scope=observed.scope AND known.author=observed.author
  AND known.source_hash=observed.source_hash
JOIN message_revisions native_revision ON native_revision.id=known.source_revision_id AND native_revision.entity_id=native.id
LEFT JOIN backup.message_canonical_aliases parent ON parent.alias_id=observation.id
WHERE selected.is_new=1 AND observation.id<>native.id AND json_extract(chat.payload,'$.Kind')='room'
  AND instr(observed.scope,'xmpp:correct:room:'||chat.address||':')=1 AND instr(observed.author,chat.address||'/')=1
  AND observation.provider_id=oie_room_correction_provider_id(observed.author,observed.remote_id)
  AND json_extract(command.payload,'$.Plan.Scope')=observed.scope AND json_extract(command.payload,'$.Plan.Author')=observed.author
  AND json_extract(command.payload,'$.Plan.OriginalWireID')=observed.target_id
  AND observed.source_hash=oie_edit_wire_hash(command.payload)
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

DELETE FROM restore_messages WHERE id IN (SELECT source_id FROM restore_suppressed_echoes);

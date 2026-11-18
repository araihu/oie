WITH interrupted AS (
  SELECT b.id FROM restore_local_edit_commands admitted
  JOIN backup.message_edit_commands b ON b.id=admitted.id
  WHERE admitted.eligible=1 AND b.state='dispatching'
), witnessed AS (
  SELECT pending.id,MIN(witness.id) AS witness_id FROM restore_messages selected
  JOIN backup.messages archived ON archived.id=selected.id AND archived.is_own=1 AND archived.state='submitted'
  JOIN backup.message_correction_facts fact ON fact.message_id=archived.id AND fact.target_id<>''
  JOIN backup.message_revisions proof ON proof.id=fact.source_revision_id AND proof.entity_id=archived.id
  JOIN messages witness ON witness.id=archived.id AND witness.account_id=archived.account_id
    AND witness.conversation_id=archived.conversation_id AND witness.is_own=1 AND witness.state='submitted'
  JOIN message_edit_commands pending ON pending.id=fact.remote_id AND pending.account_id=witness.account_id
    AND pending.conversation_id=witness.conversation_id AND pending.state IN ('queued','dispatching','unknown','failed')
  JOIN messages original ON original.id=pending.message_id AND original.id=original.provider_id
    AND original.account_id=pending.account_id AND original.conversation_id=pending.conversation_id
    AND original.is_own=1 AND original.state='submitted'
  LEFT JOIN message_canonical_aliases linked ON linked.alias_id=witness.id
  LEFT JOIN backup.message_replies archived_reply ON archived_reply.message_id=archived.id AND archived_reply.body_hash=fact.source_hash
  LEFT JOIN message_replies witness_reply ON witness_reply.message_id=witness.id AND witness_reply.body_hash=fact.source_hash
  WHERE (linked.alias_id IS NULL OR linked.canonical_id=original.id)
    AND fact.target_id=original.provider_id AND fact.scope=json_extract(pending.payload,'$.Plan.Scope')
    AND fact.author=json_extract(pending.payload,'$.Plan.Author') AND fact.source_hash=oie_edit_wire_hash(pending.payload)
    AND oie_edit_wire_matches(pending.payload,archived.payload,archived_reply.payload)=1
    AND oie_edit_wire_matches(pending.payload,proof.payload,archived_reply.payload)=1
    AND oie_edit_wire_matches(pending.payload,witness.payload,witness_reply.payload)=1
    AND fact.source_hash=COALESCE(NULLIF(json_extract(archived.payload,'$.WireBodySHA256'),''),
                                   oie_source_hash(json_extract(archived.payload,'$.OriginalText')))
    AND fact.source_hash=COALESCE(NULLIF(json_extract(proof.payload,'$.WireBodySHA256'),''),
                                   oie_source_hash(json_extract(proof.payload,'$.OriginalText')))
    AND NOT EXISTS (
      SELECT 1 FROM expired_message_ids expired WHERE expired.id IN (witness.id,original.id,pending.id)
    )
    AND NOT EXISTS (
      SELECT 1 FROM expired_message_references expired
      WHERE expired.conversation_id=witness.conversation_id AND expired.scope=fact.scope AND expired.author=fact.author
        AND expired.remote_id IN (fact.remote_id,fact.target_id)
    )
    AND NOT EXISTS (
      SELECT 1 FROM message_correction_facts other JOIN messages owner ON owner.id=other.message_id
      WHERE owner.account_id=witness.account_id AND owner.conversation_id=witness.conversation_id
        AND other.message_id<>witness.id AND other.scope=fact.scope AND other.author=fact.author AND other.remote_id=fact.remote_id
    )
  GROUP BY pending.id HAVING COUNT(DISTINCT witness.id)=1
)
SELECT pending.id,i.id IS NOT NULL,w.witness_id
FROM message_edit_commands pending
LEFT JOIN interrupted i ON i.id=pending.id AND pending.state='queued'
LEFT JOIN witnessed w ON w.id=pending.id
WHERE i.id IS NOT NULL OR w.id IS NOT NULL;

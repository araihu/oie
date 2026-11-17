-- A local edit's wire target names the original. Its immutable acceptance
-- instead identifies the previous content revision it was allowed to edit.
CREATE TEMP TABLE restore_local_edit_sources (
    source_id TEXT PRIMARY KEY, command_id TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO restore_local_edit_sources
SELECT source.id,command.id FROM backup.message_edit_commands command
JOIN restore_chats chat ON chat.id=command.conversation_id
JOIN backup.messages source ON source.provider_id=command.id AND source.account_id=command.account_id
  AND source.conversation_id=command.conversation_id AND source.is_own=1 AND source.state='submitted'
WHERE command.state IN ('submitted','failed');

CREATE TEMP TABLE restore_local_edit_commands (id TEXT PRIMARY KEY, eligible INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO restore_local_edit_commands
WITH RECURSIVE accepted AS MATERIALIZED (
    SELECT source.source_id,command.id,command.message_id,command.account_id,command.conversation_id,command.state,
           json_extract(command.payload,'$.Command.ExpectedRevisionID') AS parent_revision,
           json_extract(command.payload,'$.ContentRevisionID') AS content_revision,
           json_extract(command.payload,'$.Plan.OriginalWireID') AS wire_target,
           json_extract(command.payload,'$.Plan.Scope') AS scope,
           json_extract(command.payload,'$.Plan.Author') AS author,
           oie_edit_wire_hash(command.payload) AS wire_hash,
           receipt.fingerprint,receipt.result
    FROM backup.message_edit_commands command
    JOIN restore_chats chat ON chat.id=command.conversation_id
    JOIN backup.messages original ON original.id=command.message_id AND original.is_own=1 AND original.id=original.provider_id
      AND original.account_id=command.account_id AND original.conversation_id=command.conversation_id
    LEFT JOIN restore_local_edit_sources source ON source.command_id=command.id
    JOIN backup.operations receipt ON receipt.id=command.id AND receipt.resource_id=command.id AND receipt.kind='edit_message'
    WHERE json_extract(command.payload,'$.Command.ID')=command.id
      AND json_extract(command.payload,'$.Command.RevisionID')=command.revision_id
      AND json_extract(command.payload,'$.Command.State')=command.state
      AND json_extract(command.payload,'$.Command.MessageID')=command.message_id
      AND json_extract(command.payload,'$.Command.AccountID')=command.account_id
      AND json_extract(command.payload,'$.Command.ConversationID')=command.conversation_id
      AND json_extract(command.payload,'$.Plan.TargetMessageID')=command.message_id
      AND json_extract(command.payload,'$.Plan.OriginalWireID')=original.provider_id
      AND json_extract(receipt.result,'$.ID')=command.id
      AND json_extract(receipt.result,'$.State')='queued'
      AND json_extract(receipt.result,'$.MessageID')=command.message_id
      AND json_extract(receipt.result,'$.AccountID')=command.account_id
      AND json_extract(receipt.result,'$.ConversationID')=command.conversation_id
      AND json_extract(receipt.result,'$.ExpectedRevisionID')=json_extract(command.payload,'$.Command.ExpectedRevisionID')
      AND json_extract(receipt.result,'$.OriginalText')=json_extract(command.payload,'$.Command.OriginalText')
      AND json_extract(receipt.result,'$.CreatedAt')=json_extract(command.payload,'$.Command.CreatedAt')
), verified AS MATERIALIZED (
    SELECT edit.id,edit.source_id,edit.message_id,parent_message.id AS parent_id,
           (selected.id IS NOT NULL OR (edit.source_id IS NULL AND edit.state='failed'))
           AND original.id IS NOT NULL
           AND NOT EXISTS(SELECT 1 FROM expired_message_ids expired WHERE expired.id IN (edit.source_id,edit.message_id,parent_message.id))
           AND NOT EXISTS (
               SELECT 1 FROM expired_message_references expired
               WHERE expired.conversation_id=edit.conversation_id AND (
                   (expired.scope=edit.scope AND expired.author=edit.author AND expired.remote_id IN (edit.id,edit.wire_target))
                   OR EXISTS (
                       SELECT 1 FROM backup.message_references ref WHERE ref.message_id IN (edit.source_id,edit.message_id,parent_message.id)
                         AND ref.scope=expired.scope AND ref.remote_id=expired.remote_id AND ref.author=expired.author
                   )
               )
           ) AS selected
    FROM accepted edit
    LEFT JOIN restore_messages selected ON selected.id=edit.source_id
    LEFT JOIN restore_messages original ON original.id=edit.message_id
    LEFT JOIN backup.message_revisions parent ON parent.id=edit.parent_revision
    -- A re-export can retain the submitted parent's witnessed source in
    -- place of the native source revision named by the acceptance receipt.
    LEFT JOIN accepted witnessed_parent ON parent.id IS NULL AND witnessed_parent.content_revision=edit.parent_revision
      AND witnessed_parent.message_id=edit.message_id AND witnessed_parent.account_id=edit.account_id
      AND witnessed_parent.conversation_id=edit.conversation_id AND witnessed_parent.state='submitted'
      AND witnessed_parent.source_id<>witnessed_parent.id
    JOIN backup.messages parent_message ON parent_message.id=COALESCE(parent.entity_id,witnessed_parent.source_id)
      AND parent_message.account_id=edit.account_id
      AND parent_message.conversation_id=edit.conversation_id
    LEFT JOIN backup.message_canonical_aliases parent_alias ON parent_alias.alias_id=parent_message.id
    WHERE edit.scope<>'' AND edit.author<>'' AND edit.wire_target<>''
      AND COALESCE(parent_alias.canonical_id,parent_message.id)=edit.message_id
      AND (parent.id IS NULL OR parent.rowid=(SELECT MIN(first.rowid) FROM backup.message_revisions first WHERE first.entity_id=parent.entity_id))
      AND (edit.source_id IS NULL OR EXISTS (
          SELECT 1 FROM backup.messages source
          JOIN backup.message_correction_facts fact ON fact.message_id=source.id AND fact.remote_id=edit.id
            AND fact.scope=edit.scope AND fact.author=edit.author AND fact.target_id=edit.wire_target
          JOIN backup.message_revisions content ON content.id=fact.source_revision_id AND content.entity_id=source.id
          WHERE source.id=edit.source_id
            AND COALESCE(NULLIF(json_extract(source.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(source.payload,'$.OriginalText')))=edit.wire_hash
            AND COALESCE(NULLIF(json_extract(content.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(content.payload,'$.OriginalText')))=edit.wire_hash
            AND fact.source_hash=edit.wire_hash
      ))
      AND NOT EXISTS (
          SELECT 1 FROM operations local WHERE local.id=edit.id AND (
            local.kind<>'edit_message' OR local.resource_id<>edit.id OR local.fingerprint<>edit.fingerprint
            OR (local.result<>'null' AND local.result<>edit.result)
          )
      )
      AND NOT EXISTS (
          SELECT 1 FROM message_edit_commands local WHERE local.id=edit.id AND (
            local.message_id<>edit.message_id OR local.account_id<>edit.account_id OR local.conversation_id<>edit.conversation_id
            OR json_extract(local.payload,'$.Command.ExpectedRevisionID') IS NOT edit.parent_revision
            OR json_extract(local.payload,'$.Plan.Scope') IS NOT edit.scope
            OR json_extract(local.payload,'$.Plan.Author') IS NOT edit.author
            OR json_extract(local.payload,'$.Plan.OriginalWireID') IS NOT edit.wire_target
            OR oie_edit_wire_hash(local.payload) IS NOT edit.wire_hash
          )
      )
      AND NOT EXISTS (
          SELECT 1 FROM messages local JOIN message_revisions first ON first.rowid=(
              SELECT MIN(r.rowid) FROM message_revisions r WHERE r.entity_id=local.id
          ) WHERE local.id=parent.entity_id AND first.id<>parent.id
      )
), eligible(id,source_id,message_id) AS (
    SELECT id,source_id,message_id FROM verified WHERE selected AND parent_id=message_id
    UNION
    SELECT child.id,child.source_id,child.message_id FROM verified child JOIN eligible parent
      ON child.parent_id=parent.source_id AND child.message_id=parent.message_id
    WHERE child.selected
)
-- An accepted operation with no verified eligible history still reserves
-- its exact key, including interrupted commands and excluded ancestors.
SELECT DISTINCT accepted.id,eligible.id IS NOT NULL FROM accepted LEFT JOIN eligible ON eligible.id=accepted.id;

DELETE FROM restore_messages
WHERE is_new=1 AND EXISTS (
    SELECT 1 FROM restore_local_edit_sources source
    WHERE source.source_id=restore_messages.id
      AND NOT EXISTS(SELECT 1 FROM restore_local_edit_commands command WHERE command.id=source.command_id AND command.eligible=1)
);

-- A direct archive can already have the exact submitted correction under a
-- different local row ID. Keep that row and its canonical/archived identities.
CREATE TEMP TABLE restore_local_edit_witnesses (
    source_id TEXT PRIMARY KEY, witness_id TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO restore_local_edit_witnesses
SELECT source.source_id,witness.id FROM restore_local_edit_commands admitted
JOIN restore_local_edit_sources source ON source.command_id=admitted.id
JOIN restore_messages selected ON selected.id=source.source_id AND selected.is_new=1
JOIN backup.message_edit_commands command ON command.id=source.command_id AND command.state='submitted'
JOIN messages root ON root.id=command.message_id AND root.provider_id=root.id AND root.is_own=1
  AND root.account_id=command.account_id AND root.conversation_id=command.conversation_id
JOIN conversations chat ON chat.id=root.conversation_id AND json_extract(chat.payload,'$.Kind')='direct'
JOIN messages witness ON witness.account_id=root.account_id AND witness.conversation_id=root.conversation_id
  AND witness.provider_id=command.id AND witness.id<>source.source_id AND witness.is_own=1 AND witness.state='submitted'
JOIN message_canonical_aliases alias ON alias.alias_id=witness.id AND alias.canonical_id=root.id
JOIN backup.messages original_edit ON original_edit.id=source.source_id
WHERE admitted.eligible=1
  AND COALESCE(NULLIF(json_extract(witness.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(witness.payload,'$.OriginalText')))
      =COALESCE(NULLIF(json_extract(original_edit.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(original_edit.payload,'$.OriginalText')))
  AND EXISTS (
      SELECT 1 FROM message_correction_facts local
      JOIN backup.message_correction_facts archived ON archived.message_id=source.source_id
        AND archived.scope=local.scope AND archived.author=local.author AND archived.remote_id=local.remote_id
        AND archived.target_id=local.target_id AND archived.source_hash=local.source_hash
      JOIN message_revisions retained ON retained.id=local.source_revision_id AND retained.entity_id=witness.id
      WHERE local.message_id=witness.id AND local.remote_id=command.id AND local.target_id=root.provider_id
        AND local.scope=json_extract(command.payload,'$.Plan.Scope') AND local.author=json_extract(command.payload,'$.Plan.Author')
        AND local.source_hash=COALESCE(NULLIF(json_extract(witness.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(witness.payload,'$.OriginalText')))
        AND local.source_hash=COALESCE(NULLIF(json_extract(retained.payload,'$.WireBodySHA256'),''),oie_source_hash(json_extract(retained.payload,'$.OriginalText')))
  );
DELETE FROM restore_messages WHERE id IN (SELECT source_id FROM restore_local_edit_witnesses);

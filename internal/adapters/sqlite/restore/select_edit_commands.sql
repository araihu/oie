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
    SELECT source.source_id,command.id,command.message_id,command.account_id,command.conversation_id,
           json_extract(command.payload,'$.Command.ExpectedRevisionID') AS parent_revision,
           json_extract(command.payload,'$.Plan.OriginalWireID') AS wire_target,
           json_extract(command.payload,'$.Plan.Scope') AS scope,
           json_extract(command.payload,'$.Plan.Author') AS author,
           COALESCE(NULLIF(json_extract(command.payload,'$.WireText'),''),
                    json_extract(command.payload,'$.Command.OriginalText')) AS wire_body,
           receipt.fingerprint,receipt.result
    FROM restore_local_edit_sources source
    JOIN backup.message_edit_commands command ON command.id=source.command_id
    JOIN backup.operations receipt ON receipt.id=command.id AND receipt.resource_id=command.id AND receipt.kind='edit_message'
    WHERE json_extract(command.payload,'$.Command.ID')=command.id
      AND json_extract(command.payload,'$.Command.RevisionID')=command.revision_id
      AND json_extract(command.payload,'$.Command.State')=command.state
      AND json_extract(command.payload,'$.Command.MessageID')=command.message_id
      AND json_extract(command.payload,'$.Command.AccountID')=command.account_id
      AND json_extract(command.payload,'$.Command.ConversationID')=command.conversation_id
      AND json_extract(command.payload,'$.Plan.TargetMessageID')=command.message_id
      AND json_extract(receipt.result,'$.ID')=command.id
      AND json_extract(receipt.result,'$.MessageID')=command.message_id
      AND json_extract(receipt.result,'$.AccountID')=command.account_id
      AND json_extract(receipt.result,'$.ConversationID')=command.conversation_id
      AND json_extract(receipt.result,'$.ExpectedRevisionID')=json_extract(command.payload,'$.Command.ExpectedRevisionID')
      AND json_extract(receipt.result,'$.OriginalText')=json_extract(command.payload,'$.Command.OriginalText')
      AND json_extract(receipt.result,'$.CreatedAt')=json_extract(command.payload,'$.Command.CreatedAt')
), verified AS MATERIALIZED (
    SELECT edit.id,edit.source_id,edit.message_id,parent.entity_id AS parent_id,
           selected.id IS NOT NULL AND original.id IS NOT NULL
           AND NOT EXISTS(SELECT 1 FROM expired_message_ids expired WHERE expired.id IN (edit.source_id,edit.message_id,parent.entity_id))
           AND NOT EXISTS (
               SELECT 1 FROM expired_message_references expired
               WHERE expired.conversation_id=edit.conversation_id AND (
                   (expired.scope=edit.scope AND expired.author=edit.author AND expired.remote_id IN (edit.id,edit.wire_target))
                   OR EXISTS (
                       SELECT 1 FROM backup.message_references ref WHERE ref.message_id IN (edit.source_id,edit.message_id,parent.entity_id)
                         AND ref.scope=expired.scope AND ref.remote_id=expired.remote_id AND ref.author=expired.author
                   )
               )
           ) AS selected
    FROM accepted edit
    LEFT JOIN restore_messages selected ON selected.id=edit.source_id
    LEFT JOIN restore_messages original ON original.id=edit.message_id
    JOIN backup.messages source ON source.id=edit.source_id
    JOIN backup.message_revisions parent ON parent.id=edit.parent_revision
    JOIN backup.messages parent_message ON parent_message.id=parent.entity_id AND parent_message.account_id=edit.account_id
      AND parent_message.conversation_id=edit.conversation_id
    LEFT JOIN backup.message_canonical_aliases parent_alias ON parent_alias.alias_id=parent.entity_id
    JOIN backup.message_correction_facts fact ON fact.message_id=edit.source_id AND fact.remote_id=edit.id
      AND fact.scope=edit.scope AND fact.author=edit.author AND fact.target_id=edit.wire_target
    JOIN backup.message_revisions content ON content.id=fact.source_revision_id AND content.entity_id=edit.source_id
    WHERE edit.scope<>'' AND edit.author<>'' AND edit.wire_target<>''
      AND COALESCE(parent_alias.canonical_id,parent.entity_id)=edit.message_id
      AND parent.rowid=(SELECT MIN(first.rowid) FROM backup.message_revisions first WHERE first.entity_id=parent.entity_id)
      AND json_extract(source.payload,'$.OriginalText')=edit.wire_body
      AND json_extract(content.payload,'$.OriginalText')=edit.wire_body
      AND fact.source_hash=oie_source_hash(edit.wire_body)
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
            OR COALESCE(NULLIF(json_extract(local.payload,'$.WireText'),''),
                        json_extract(local.payload,'$.Command.OriginalText')) IS NOT edit.wire_body
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
SELECT DISTINCT verified.id,eligible.id IS NOT NULL FROM verified LEFT JOIN eligible ON eligible.id=verified.id;

DELETE FROM restore_messages
WHERE is_new=1 AND EXISTS (
    SELECT 1 FROM restore_local_edit_sources source
    WHERE source.source_id=restore_messages.id
      AND NOT EXISTS(SELECT 1 FROM restore_local_edit_commands command WHERE command.id=source.command_id AND command.eligible=1)
);

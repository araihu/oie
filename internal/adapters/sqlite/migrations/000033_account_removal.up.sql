-- Message deletion also purges outbox entries, message projections and their
-- revisions via purge_message_dependencies. Foreign keys remain enabled.
CREATE TRIGGER purge_account_dependencies BEFORE DELETE ON accounts BEGIN
 DELETE FROM changes
 WHERE json_extract(payload,'$.AccountID')=OLD.id
 OR (kind='account.registered' AND json_extract(payload,'$.ID')=OLD.id)
 OR (kind='conversation.history_pruned' AND json_extract(payload,'$.ConversationID') IN
     (SELECT id FROM conversations WHERE account_id=OLD.id));

 -- Reserve old operation keys without keeping their snapshots. This prevents
 -- retrying the original registration from silently re-adding the account.
 UPDATE operations SET result='null'
 WHERE resource_id=OLD.id
 OR resource_id IN (SELECT id FROM conversations WHERE account_id=OLD.id);

 DELETE FROM messages WHERE account_id=OLD.id;
 DELETE FROM staged_attachments WHERE conversation_id IN (SELECT id FROM conversations WHERE account_id=OLD.id);
 DELETE FROM original_transmission_order WHERE conversation_id IN (SELECT id FROM conversations WHERE account_id=OLD.id);
 DELETE FROM conversation_read_states WHERE conversation_id IN (SELECT id FROM conversations WHERE account_id=OLD.id);
 DELETE FROM conversation_read_revisions WHERE conversation_id IN (SELECT id FROM conversations WHERE account_id=OLD.id);
 DELETE FROM conversation_revisions WHERE entity_id IN (SELECT id FROM conversations WHERE account_id=OLD.id);
 DELETE FROM room_archives WHERE conversation_id IN (SELECT id FROM conversations WHERE account_id=OLD.id);
 DELETE FROM room_intents WHERE account_id=OLD.id;
 DELETE FROM room_reaction_privacy WHERE account_id=OLD.id;
 DELETE FROM message_reaction_observations WHERE account_id=OLD.id;
 DELETE FROM conversations WHERE account_id=OLD.id;

 DELETE FROM history_status WHERE account_id=OLD.id;
 DELETE FROM archive_checkpoints WHERE account_id=OLD.id;
 DELETE FROM archive_aliases WHERE account_id=OLD.id;
 DELETE FROM contact_sources WHERE account_id=OLD.id;
 DELETE FROM contact_revisions WHERE entity_id IN (SELECT id FROM contacts WHERE account_id=OLD.id);
 DELETE FROM contacts WHERE account_id=OLD.id;
 DELETE FROM avatar_revisions WHERE entity_id IN (SELECT id FROM avatars WHERE account_id=OLD.id);
 DELETE FROM avatars WHERE account_id=OLD.id;
 DELETE FROM revisions WHERE entity_id=OLD.id;
END;

PRAGMA user_version=33;

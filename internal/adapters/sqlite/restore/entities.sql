INSERT INTO accounts SELECT a.* FROM backup.accounts a JOIN restore_accounts x ON x.id=a.id
WHERE x.is_new=1;
INSERT INTO revisions SELECT r.* FROM backup.revisions r JOIN restore_accounts a ON a.id=r.entity_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
INSERT INTO operations
SELECT o.* FROM backup.operations o JOIN restore_accounts a ON a.id=o.resource_id
WHERE o.kind='register_account' AND o.result<>'null'
ON CONFLICT(id) DO NOTHING;

-- Newly restored chats receive their local activity sequence when publishing
-- conversation.created. Their final summary is computed from merged history.
INSERT INTO conversations (id,revision_id,account_id,address,peer_id,payload,activity_sequence,unread_count,archived,favorite)
SELECT c.id,c.revision_id,c.account_id,c.address,c.peer_id,
       json_set(c.payload,'$.LastMessage','','$.UnreadCount',0,'$.UnreadMention',json('false')),
       0,
       0,c.archived,c.favorite
FROM backup.conversations c JOIN restore_chats x ON x.id=c.id AND x.is_new=1;
INSERT INTO conversation_revisions
SELECT r.id,r.entity_id,r.parents,json_set(r.payload,'$.LastMessage',''),r.created_at
FROM backup.conversation_revisions r JOIN restore_chats x ON x.id=r.entity_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;

-- Keep the destination's preferences and removal decisions when adopting
-- the verified room identity under the existing conversation ID.
UPDATE conversations AS d SET revision_id=b.revision_id,
  payload=json_set(d.payload,'$.Kind','room','$.Title',json_extract(b.payload,'$.Title'),'$.RevisionID',b.revision_id)
FROM backup.conversations b JOIN restore_chats x ON x.id=b.id AND x.promote_room=1
WHERE d.id=b.id;

INSERT INTO accepted_direct_history
SELECT a.* FROM backup.accepted_direct_history a JOIN restore_chats x ON x.id=a.conversation_id
WHERE TRUE ON CONFLICT DO NOTHING;

-- A direct witness used the original wire ID as its provider key. Release the
-- key for the native row while retaining the witness's scoped archive aliases.
UPDATE messages AS d
SET provider_id=CASE WHEN d.provider_id=e.original_id THEN '' ELSE d.provider_id END,
    payload=json_set(d.payload,'$.OwnOriginID',e.original_id,'$.ProviderID',
                     CASE WHEN d.provider_id=e.original_id THEN '' ELSE d.provider_id END)
FROM restore_witness_echoes e WHERE d.id=e.echo_id
AND (d.provider_id=e.original_id OR json_extract(d.payload,'$.OwnOriginID') IS NOT e.original_id);

UPDATE messages AS d SET provider_id=e.provider_id,payload=json_set(d.payload,'$.ProviderID',e.provider_id)
FROM restore_direct_mention_echo_keys e WHERE d.id=e.id AND e.is_local=1;

INSERT INTO messages(id,revision_id,account_id,conversation_id,provider_id,is_own,state,payload,created_at_ms,timeline_at_ms,submitted_at_ms)
SELECT m.id,m.revision_id,m.account_id,m.conversation_id,COALESCE(e.provider_id,m.provider_id),m.is_own,m.state,
       CASE WHEN e.id IS NULL THEN m.payload ELSE json_set(m.payload,'$.ProviderID',e.provider_id) END,
       m.created_at_ms,m.timeline_at_ms,m.submitted_at_ms
FROM backup.messages m JOIN restore_messages x ON x.id=m.id AND x.is_new=1
LEFT JOIN restore_direct_mention_echo_keys e ON e.id=m.id AND e.is_local=0;
INSERT INTO message_revisions SELECT r.* FROM backup.message_revisions r JOIN restore_messages x ON x.id=r.entity_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
INSERT INTO message_canonical_aliases(alias_id,canonical_id)
SELECT echo_id,original_id FROM restore_witness_echoes WHERE TRUE ON CONFLICT(alias_id) DO NOTHING;

-- A witness may already have an authenticated correction family. Keep its
-- revisions and proof, while placing the content projection on the native ID.
UPDATE message_canonical_aliases AS a SET canonical_id=e.original_id
FROM restore_witness_echoes e WHERE a.canonical_id=e.echo_id AND a.alias_id<>e.original_id;
INSERT INTO message_content_heads(canonical_id,source_id,revision_id,payload,edited_at)
SELECT e.original_id,h.source_id,h.revision_id,
       json_set(h.payload,'$.ID',e.original_id,'$.CreatedAt',json_extract(m.payload,'$.CreatedAt')),h.edited_at
FROM restore_witness_echoes e JOIN message_content_heads h ON h.canonical_id=e.echo_id
JOIN messages m ON m.id=e.original_id
ORDER BY h.edited_at DESC,h.revision_id DESC ON CONFLICT(canonical_id) DO NOTHING;
DELETE FROM message_content_heads WHERE canonical_id IN (SELECT echo_id FROM restore_witness_echoes);

-- A terminal outbox entry still supplies timeline and cancellation evidence.
INSERT INTO outbox SELECT o.* FROM backup.outbox o JOIN restore_messages x ON x.id=o.message_id AND x.is_new=1
WHERE o.state IN ('submitted','failed','cancelled');
INSERT INTO operations
SELECT o.id,o.fingerprint,o.resource_id,
       o.result,o.created_at,o.kind
FROM backup.operations o JOIN restore_messages x ON x.id=o.resource_id AND x.is_new=1
WHERE o.kind IN ('send_message','cancel_message','update_pending_message','send_scheduled_message_now','forward_message')
ON CONFLICT(id) DO NOTHING;

INSERT INTO message_star_revisions SELECT r.* FROM backup.message_star_revisions r JOIN restore_messages x ON x.id=r.message_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
INSERT INTO message_stars SELECT s.* FROM backup.message_stars s JOIN restore_messages x ON x.id=s.message_id
WHERE TRUE ON CONFLICT(message_id) DO NOTHING;

INSERT INTO archive_aliases SELECT a.* FROM backup.archive_aliases a JOIN restore_messages x ON x.id=a.message_id
WHERE NOT EXISTS (
  SELECT 1 FROM restore_conflicting_relayed_echo_aliases d
  WHERE d.account_id=a.account_id AND d.scope=a.scope AND d.archive_id=a.archive_id
) ON CONFLICT DO NOTHING;
INSERT INTO archive_aliases(account_id,scope,archive_id,message_id)
SELECT a.account_id,a.scope,a.archive_id,e.witness_id
FROM backup.archive_aliases a JOIN restore_local_edit_witnesses e ON e.source_id=a.message_id
JOIN messages witness ON witness.id=e.witness_id AND witness.account_id=a.account_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO message_references SELECT r.* FROM backup.message_references r JOIN restore_messages x ON x.id=r.message_id
WHERE NOT EXISTS (
  SELECT 1 FROM restore_conflicting_relayed_echo_refs d
  WHERE d.message_id=r.message_id AND d.scope=r.scope AND d.remote_id=r.remote_id AND d.author=r.author
) ON CONFLICT DO NOTHING;
INSERT INTO message_replies SELECT r.* FROM backup.message_replies r JOIN restore_messages x ON x.id=r.message_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO message_sender_identities SELECT r.* FROM backup.message_sender_identities r JOIN restore_messages x ON x.id=r.message_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO message_retractions SELECT r.* FROM backup.message_retractions r JOIN restore_accounts x ON x.id=r.account_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO room_message_moderation SELECT r.* FROM backup.room_message_moderation r JOIN restore_accounts x ON x.id=r.account_id
WHERE TRUE ON CONFLICT DO NOTHING;

INSERT INTO operations
SELECT receipt.id,receipt.fingerprint,receipt.resource_id,
       CASE WHEN edit.eligible=1 THEN receipt.result ELSE 'null' END,receipt.created_at,receipt.kind
FROM backup.operations receipt JOIN restore_local_edit_commands edit ON edit.id=receipt.id
WHERE TRUE ON CONFLICT(id) DO UPDATE SET result=excluded.result
WHERE operations.result='null' AND excluded.result<>'null' AND operations.fingerprint=excluded.fingerprint
  AND operations.kind=excluded.kind AND operations.resource_id=excluded.resource_id;
INSERT INTO message_edit_commands
SELECT command.* FROM backup.message_edit_commands command JOIN restore_local_edit_commands edit ON edit.id=command.id
WHERE edit.eligible=1 ON CONFLICT(id) DO UPDATE SET
  revision_id=excluded.revision_id,state=excluded.state,
  payload=json_set(message_edit_commands.payload,
       '$.Command.RevisionID',excluded.revision_id,'$.Command.State',excluded.state,
       '$.Command.UpdatedAt',json_extract(excluded.payload,'$.Command.UpdatedAt'),
       '$.Command.LastErrorCode',COALESCE(json_extract(excluded.payload,'$.Command.LastErrorCode'),''))
WHERE message_edit_commands.state='queued' AND excluded.state='submitted';
INSERT INTO message_correction_facts SELECT f.* FROM backup.message_correction_facts f JOIN restore_messages x ON x.id=f.message_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO message_correction_facts(message_id,scope,author,remote_id,target_id,source_hash,source_revision_id)
SELECT e.original_id,f.scope,f.author,f.remote_id,'',f.source_hash,m.revision_id
FROM restore_witness_echoes e JOIN message_correction_facts f ON f.message_id=e.echo_id AND f.target_id=''
JOIN message_revisions p ON p.id=f.source_revision_id AND p.entity_id=f.message_id
JOIN messages m ON m.id=e.original_id
JOIN message_revisions r ON r.id=m.revision_id AND r.entity_id=m.id
WHERE f.source_hash=COALESCE(NULLIF(json_extract(p.payload,'$.WireBodySHA256'),''),
                            oie_source_hash(json_extract(p.payload,'$.OriginalText')))
  AND f.source_hash=COALESCE(NULLIF(json_extract(r.payload,'$.WireBodySHA256'),''),
                             oie_source_hash(json_extract(r.payload,'$.OriginalText')))
ON CONFLICT DO NOTHING;

INSERT INTO account_communication_preferences (account_id,revision_id,presence,send_typing)
SELECT a.account_id,a.revision_id,a.presence,a.send_typing
FROM backup.account_communication_preferences a JOIN restore_accounts x ON x.id=a.account_id
WHERE TRUE ON CONFLICT(account_id) DO NOTHING;
INSERT INTO avatars SELECT a.* FROM backup.avatars a JOIN restore_accounts x ON x.id=a.account_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO avatar_revisions SELECT r.* FROM backup.avatar_revisions r
JOIN backup.avatars b ON b.id=r.entity_id JOIN avatars a ON a.id=b.id AND a.account_id=b.account_id AND a.address=b.address
JOIN restore_accounts x ON x.id=b.account_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
INSERT INTO contacts SELECT a.* FROM backup.contacts a JOIN restore_accounts x ON x.id=a.account_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO contact_revisions SELECT r.* FROM backup.contact_revisions r
JOIN backup.contacts b ON b.id=r.entity_id JOIN contacts c ON c.id=b.id AND c.account_id=b.account_id AND c.address=b.address
JOIN restore_accounts x ON x.id=b.account_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
-- Restore read evidence with no policy authorization to send XMPP receipts.
INSERT INTO conversation_read_revisions (id,conversation_id,parents,payload,message_ids,created_at)
SELECT r.id,r.conversation_id,r.parents,r.payload,r.message_ids,r.created_at
FROM backup.conversation_read_revisions r JOIN restore_chats x ON x.id=r.conversation_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;
INSERT INTO conversation_read_states
SELECT r.* FROM backup.conversation_read_states r JOIN restore_chats x ON x.id=r.conversation_id
WHERE TRUE ON CONFLICT(conversation_id) DO NOTHING;
INSERT INTO message_reads
SELECT r.* FROM backup.message_reads r JOIN restore_messages x ON x.id=r.message_id
WHERE TRUE ON CONFLICT(message_id) DO NOTHING;

-- Preserve stable catalog IDs for downloaded files. Search/document projections
-- are rebuilt locally after restore.
INSERT INTO shared_content
SELECT ref.* FROM backup.shared_content ref JOIN restore_messages x ON x.id=ref.message_id AND x.is_new=1
JOIN backup.messages m ON m.id=x.id
LEFT JOIN backup.message_content_heads h ON h.canonical_id=m.id
WHERE oie_quote_preview_allowed(COALESCE(h.payload,m.payload),ref.url)
ON CONFLICT DO NOTHING;
INSERT INTO shared_content_state
SELECT r.* FROM backup.shared_content_state r JOIN restore_messages x ON x.id=r.message_id AND x.is_new=1
JOIN backup.messages m ON m.id=x.id
LEFT JOIN backup.message_content_heads h ON h.canonical_id=m.id
-- Saved captions may predate inbound quote redaction. Index the retained body.
WHERE COALESCE(json_extract(COALESCE(h.payload,m.payload),'$.WireBodySHA256'),'')=''
   OR COALESCE(json_type(COALESCE(h.payload,m.payload),'$.OutboundReply'),'null')<>'null'
ON CONFLICT DO NOTHING;

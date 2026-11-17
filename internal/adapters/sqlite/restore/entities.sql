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

INSERT INTO accepted_direct_history
SELECT a.* FROM backup.accepted_direct_history a JOIN restore_chats x ON x.id=a.conversation_id
WHERE TRUE ON CONFLICT DO NOTHING;

INSERT INTO messages SELECT m.* FROM backup.messages m JOIN restore_messages x ON x.id=m.id AND x.is_new=1;
INSERT INTO message_revisions SELECT r.* FROM backup.message_revisions r JOIN restore_messages x ON x.id=r.entity_id
WHERE TRUE ON CONFLICT(id) DO NOTHING;

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
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO message_references SELECT r.* FROM backup.message_references r JOIN restore_messages x ON x.id=r.message_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO message_replies SELECT r.* FROM backup.message_replies r JOIN restore_messages x ON x.id=r.message_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO message_sender_identities SELECT r.* FROM backup.message_sender_identities r JOIN restore_messages x ON x.id=r.message_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO message_retractions SELECT r.* FROM backup.message_retractions r JOIN restore_accounts x ON x.id=r.account_id
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO room_message_moderation SELECT r.* FROM backup.room_message_moderation r JOIN restore_accounts x ON x.id=r.account_id
WHERE TRUE ON CONFLICT DO NOTHING;

INSERT INTO message_edit_commands SELECT e.* FROM backup.message_edit_commands e JOIN restore_messages x ON x.id=e.message_id
WHERE e.state IN ('submitted','failed') ON CONFLICT(id) DO NOTHING;
INSERT INTO message_correction_facts SELECT f.* FROM backup.message_correction_facts f JOIN restore_messages x ON x.id=f.message_id
WHERE TRUE ON CONFLICT DO NOTHING;

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
INSERT INTO shared_content SELECT ref.* FROM backup.shared_content ref JOIN restore_messages x ON x.id=ref.message_id AND x.is_new=1
WHERE TRUE ON CONFLICT DO NOTHING;
INSERT INTO shared_content_state SELECT r.* FROM backup.shared_content_state r JOIN restore_messages x ON x.id=r.message_id AND x.is_new=1
WHERE TRUE ON CONFLICT DO NOTHING;

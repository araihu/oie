-- Resolve scopes before touching local row identifiers. Each row in the
-- result is a proposed translation of an internal relationship.
WITH accounts AS (
 SELECT s.id AS source,d.id AS destination FROM incoming.accounts s JOIN main.accounts d
 ON s.protocol='xmpp' AND d.protocol=s.protocol AND d.address=s.address
), chats AS (
 SELECT s.id AS source,d.id AS destination,s.peer_id AS source_peer,d.peer_id AS destination_peer
 FROM incoming.conversations s JOIN accounts a ON a.source=s.account_id
 JOIN main.conversations d ON d.account_id=a.destination AND d.address=s.address
 WHERE json_extract(d.payload,'$.Kind')=json_extract(s.payload,'$.Kind')
)
SELECT 'account',source,destination FROM accounts
UNION ALL SELECT 'conversation',source,destination FROM chats
UNION ALL SELECT 'peer',source_peer,destination_peer FROM chats
UNION ALL SELECT 'message',source,destination FROM portable_message_matches;

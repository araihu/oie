CREATE TABLE message_retractions (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    address TEXT NOT NULL,
    scope TEXT NOT NULL,
    remote_id TEXT NOT NULL,
    retracted_at TEXT NOT NULL,
    PRIMARY KEY(account_id,kind,address,scope,remote_id)
) WITHOUT ROWID;

-- Direct retractions refer to the sender's message id. For rooms the reference
-- must be room-assigned and the correction fact supplies verified author scope.
CREATE VIEW message_retraction_targets AS
SELECT m.id AS source_id,m.account_id,CAST(json_extract(c.payload,'$.Kind') AS TEXT) AS kind,c.address,f.scope,f.remote_id
FROM message_correction_facts f JOIN messages m ON m.id=f.message_id
JOIN conversations c ON c.id=m.conversation_id
WHERE CAST(json_extract(c.payload,'$.Kind') AS TEXT)='direct'
UNION ALL
SELECT m.id,m.account_id,CAST(json_extract(c.payload,'$.Kind') AS TEXT),c.address,'xmpp:correct:sender:'||a.address,m.id
FROM messages m JOIN conversations c ON c.id=m.conversation_id JOIN accounts a ON a.id=m.account_id
WHERE CAST(json_extract(c.payload,'$.Kind') AS TEXT)='direct' AND m.is_own=1
AND m.provider_id=m.id
AND m.state='submitted'
UNION ALL
SELECT m.id,m.account_id,CAST(json_extract(c.payload,'$.Kind') AS TEXT),c.address,f.scope,ref.remote_id
FROM message_references ref JOIN messages m ON m.id=ref.message_id
JOIN conversations c ON c.id=m.conversation_id JOIN message_correction_facts f ON f.message_id=m.id
WHERE CAST(json_extract(c.payload,'$.Kind') AS TEXT)='room' AND ref.scope='room:'||c.address AND ref.author=f.author
AND f.scope LIKE 'xmpp:correct:room:%';

CREATE VIEW retained_message_retractions AS
SELECT t.source_id,r.retracted_at
FROM message_retractions r CROSS JOIN message_retraction_targets t USING(account_id,kind,address,scope,remote_id);

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
CREATE VIEW direct_fact_retraction_targets AS
SELECT m.id AS source_id,m.account_id,CAST(json_extract(c.payload,'$.Kind') AS TEXT) AS kind,c.address,f.scope,f.remote_id
FROM message_correction_facts f JOIN messages m ON m.id=f.message_id
JOIN conversations c ON c.id=m.conversation_id
WHERE CAST(json_extract(c.payload,'$.Kind') AS TEXT)='direct';

CREATE VIEW direct_sent_retraction_targets AS
SELECT m.id AS source_id,m.account_id,CAST(json_extract(c.payload,'$.Kind') AS TEXT) AS kind,c.address,
CAST('xmpp:correct:sender:'||a.address AS TEXT) AS scope,m.id AS remote_id
FROM messages m JOIN conversations c ON c.id=m.conversation_id JOIN accounts a ON a.id=m.account_id
WHERE CAST(json_extract(c.payload,'$.Kind') AS TEXT)='direct' AND m.is_own=1
AND m.provider_id=m.id
AND m.state='submitted';

CREATE VIEW room_retraction_targets AS
SELECT m.id AS source_id,m.account_id,CAST(json_extract(c.payload,'$.Kind') AS TEXT) AS kind,c.address,f.scope,ref.remote_id
FROM message_references ref JOIN messages m ON m.id=ref.message_id
JOIN conversations c ON c.id=m.conversation_id JOIN message_correction_facts f ON f.message_id=m.id
WHERE CAST(json_extract(c.payload,'$.Kind') AS TEXT)='room' AND ref.scope='room:'||c.address AND ref.author=f.author
AND f.scope LIKE 'xmpp:correct:room:%';

CREATE VIEW message_retraction_targets AS
SELECT source_id,account_id,kind,address,scope,remote_id FROM direct_fact_retraction_targets
UNION ALL
SELECT source_id,account_id,kind,address,scope,remote_id FROM direct_sent_retraction_targets
UNION ALL
SELECT source_id,account_id,kind,address,scope,remote_id FROM room_retraction_targets;

-- Join inside each branch so a source-scoped read searches the retractions
-- primary key instead of scanning all retractions for every message.
CREATE VIEW retained_message_retractions AS
SELECT t.source_id,r.retracted_at
FROM direct_fact_retraction_targets t CROSS JOIN message_retractions r USING(account_id,kind,address,scope,remote_id)
UNION ALL
SELECT t.source_id,r.retracted_at
FROM direct_sent_retraction_targets t CROSS JOIN message_retractions r USING(account_id,kind,address,scope,remote_id)
UNION ALL
SELECT t.source_id,r.retracted_at
FROM room_retraction_targets t CROSS JOIN message_retractions r USING(account_id,kind,address,scope,remote_id);

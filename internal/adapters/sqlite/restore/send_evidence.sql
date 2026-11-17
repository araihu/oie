-- Use the same scoped proof for queue eligibility and operation-key reservation.
-- The original wire ID may survive only as a direct sender reference after
-- the witness's own retention has removed its local message row.
WITH proofs(account_id,conversation_id,wire_id) AS (
    SELECT b.account_id,b.conversation_id,b.wire_id FROM restore_source_echoes b
    JOIN conversations c ON c.id=b.conversation_id AND c.account_id=b.account_id
    UNION
    SELECT d.account_id,d.conversation_id,COALESCE(NULLIF(json_extract(d.payload,'$.OwnOriginID'),''), d.provider_id) FROM messages d
    JOIN restore_chats x ON x.id=d.conversation_id
    JOIN conversations c ON c.id=x.id AND c.account_id=d.account_id
    WHERE d.is_own=1 AND d.state='submitted' AND d.id<>d.provider_id
      AND (json_extract(d.payload,'$.OwnOriginID')<>'' OR json_extract(c.payload,'$.Kind')='direct'
           OR json_extract(d.payload,'$.Historical')=0)
    UNION
    SELECT c.account_id,c.id,r.remote_id FROM message_retractions r
    JOIN conversations c ON c.account_id=r.account_id AND c.address=r.address
    JOIN restore_chats x ON x.id=c.id
    JOIN accounts a ON a.id=c.account_id
    WHERE r.kind='direct' AND json_extract(c.payload,'$.Kind')='direct'
      AND r.scope='xmpp:correct:sender:'||a.address
    UNION
    SELECT c.account_id,c.id,e.remote_id FROM backup.expired_message_references e
    JOIN restore_chats x ON x.id=e.conversation_id
    JOIN conversations c ON c.id=x.id JOIN accounts a ON a.id=c.account_id
    WHERE json_extract(c.payload,'$.Kind')='direct'
      AND e.scope='sender:'||a.address AND e.author=a.address
    UNION
    SELECT c.account_id,c.id,e.remote_id FROM backup.discarded_message_references e
    JOIN conversations c ON c.account_id=e.account_id AND c.address=e.address
    JOIN restore_chats x ON x.id=c.id JOIN accounts a ON a.id=c.account_id
    WHERE e.kind='direct' AND json_extract(c.payload,'$.Kind')='direct'
      AND e.scope='sender:'||a.address AND e.author=a.address
)
INSERT INTO restored_own_echoes SELECT account_id,conversation_id,wire_id FROM proofs
WHERE wire_id GLOB '????????-????-7???-[89ab]???-????????????'
  AND length(replace(wire_id,'-',''))=32
  AND replace(wire_id,'-','') NOT GLOB '*[^0-9a-f]*'
ON CONFLICT DO NOTHING;

-- The send state is evidence even if its history has expired at the destination.
CREATE TEMP VIEW restore_pre_wire_sends AS
SELECT m.id,m.account_id,m.conversation_id,m.provider_id
FROM messages m JOIN outbox o ON o.message_id=m.id
JOIN restore_chats x ON x.id=m.conversation_id
WHERE m.is_own=1 AND m.state=o.state
  AND (m.state='queued' OR (m.state='dispatching' AND o.wire_stage='awaiting'));

CREATE TEMP TABLE restore_send_evidence (
    id TEXT PRIMARY KEY,state TEXT NOT NULL,at TEXT,payload TEXT,timeline_at_ms INTEGER,submitted_at_ms INTEGER
) WITHOUT ROWID;
INSERT INTO restore_send_evidence
SELECT m.id,CASE WHEN COALESCE(o.state,m.state) IN ('cancelled','submitted','failed')
         THEN COALESCE(o.state,m.state) ELSE 'unknown' END,
    (SELECT r.created_at FROM backup.message_revisions r WHERE r.id=m.revision_id AND r.entity_id=m.id),m.payload,
    CASE WHEN COALESCE(o.state,m.state) IN ('cancelled','submitted','failed') THEN m.timeline_at_ms END,
    CASE WHEN COALESCE(o.state,m.state) IN ('cancelled','submitted','failed') THEN m.submitted_at_ms END
FROM backup.messages m LEFT JOIN backup.outbox o ON o.message_id=m.id
JOIN restore_chats c ON c.id=m.conversation_id
LEFT JOIN recovered_sends p ON p.message_id=m.id AND p.conversation_id=c.id
LEFT JOIN restore_messages selected ON selected.id=m.id
LEFT JOIN messages d ON d.id=m.id AND d.conversation_id=m.conversation_id AND d.account_id=m.account_id AND d.is_own=1
LEFT JOIN restore_pre_wire_sends active ON active.id=d.id
WHERE m.is_own=1 AND COALESCE(o.state,m.state) IN ('cancelled','submitted','failed','dispatching','unknown')
  AND (p.message_id IS NOT NULL OR active.id IS NOT NULL
       OR (d.id IS NULL AND selected.id IS NULL AND COALESCE(o.state,m.state) IN ('cancelled','submitted','failed')));

-- Another engine can retain our verified own echo under a different local
-- row ID. The scoped sender ID is still the UUID of the original send.
INSERT INTO restore_send_evidence
SELECT b.wire_id,'submitted',r.created_at,b.payload,NULL,NULL
FROM restore_source_echoes b
JOIN backup.message_revisions r ON r.id=b.revision_id AND r.entity_id=b.id
WHERE (
    EXISTS(SELECT 1 FROM restore_pre_wire_sends m
           WHERE m.id=b.wire_id AND m.provider_id=m.id
             AND m.account_id=b.account_id AND m.conversation_id=b.conversation_id)
    OR EXISTS(SELECT 1 FROM recovered_sends p
              WHERE p.message_id=b.wire_id AND p.conversation_id=b.conversation_id)
)
ORDER BY r.created_at DESC,b.id
ON CONFLICT(id) DO NOTHING;

-- Previously restored proof may survive after the echo's history expired.
INSERT INTO restore_send_evidence
SELECT m.id,'unknown',NULL,NULL,NULL,NULL FROM restore_pre_wire_sends m
JOIN restored_own_echoes e ON e.account_id=m.account_id AND e.conversation_id=m.conversation_id AND e.message_id=m.id
WHERE TRUE
ON CONFLICT(id) DO NOTHING;

-- Expiry implies that the snapshot no longer had an executable send.
INSERT INTO restore_send_evidence
SELECT e.id,'unknown',NULL,NULL,NULL,NULL FROM backup.expired_message_ids e JOIN restore_pre_wire_sends m ON m.id=e.id
WHERE TRUE
ON CONFLICT(id) DO NOTHING;

-- Once terminal evidence is available a stale next restore must not recreate
-- a review item, even when retention excluded the terminal message itself.
INSERT INTO discarded_recovered_sends
SELECT e.id FROM restore_send_evidence e
WHERE e.state IN ('cancelled','submitted','failed')
  AND NOT EXISTS(SELECT 1 FROM messages m WHERE m.id=e.id)
ON CONFLICT(message_id) DO NOTHING;

DELETE FROM recovered_sends
WHERE EXISTS(SELECT 1 FROM discarded_recovered_sends d WHERE d.message_id=recovered_sends.message_id)
   OR EXISTS(SELECT 1 FROM expired_message_ids d WHERE d.id=recovered_sends.message_id)
   OR EXISTS(
       SELECT 1 FROM restored_own_echoes e JOIN conversations c ON c.id=e.conversation_id AND c.account_id=e.account_id
       WHERE c.id=recovered_sends.conversation_id AND e.message_id=recovered_sends.message_id
   );

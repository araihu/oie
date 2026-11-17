ALTER TABLE conversation_read_revisions ADD COLUMN displayed_policy_epoch TEXT;

DROP VIEW conversation_read_receipt_policy;
CREATE VIEW conversation_read_receipt_policy AS
SELECT c.id AS conversation_id,
       CAST(g.revision || ':' || COALESCE(p.revision, 0) AS TEXT) AS revision,
       CAST(CASE WHEN p.enabled IS NULL THEN 'inherit:' || g.revision || ':' || COALESCE(p.revision, 0)
                 ELSE 'override:' || p.revision END AS TEXT) AS policy_epoch,
       p.enabled AS override_enabled, g.enabled AS global_enabled,
       CAST(COALESCE(p.enabled, g.enabled) AS INTEGER) AS effective_enabled
FROM conversations c CROSS JOIN engine_read_receipt_settings g
LEFT JOIN conversation_read_receipt_settings p ON p.conversation_id=c.id
WHERE g.singleton=1 AND json_extract(c.payload,'$.Request') IS NULL
AND COALESCE(json_extract(c.payload,'$.RemovedFromChats'),0)=0;

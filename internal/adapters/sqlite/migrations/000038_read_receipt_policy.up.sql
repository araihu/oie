CREATE TABLE engine_read_receipt_settings (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    revision INTEGER NOT NULL
);
INSERT INTO engine_read_receipt_settings(singleton, enabled, revision) VALUES (1, 1, 0);

CREATE TABLE conversation_read_receipt_settings (
    conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
    enabled INTEGER CHECK (enabled IN (0, 1)),
    revision INTEGER NOT NULL
);

-- Preserve opt-outs from profiles used to preview the account-level setting.
INSERT INTO conversation_read_receipt_settings(conversation_id, enabled, revision)
SELECT c.id, 0, 1 FROM conversations c JOIN account_communication_preferences p ON p.account_id=c.account_id
WHERE p.send_read_receipts=0;
-- Keep the preview column so that a downgrade can restore its opt-outs.

CREATE VIEW conversation_read_receipt_policy AS
SELECT c.id AS conversation_id,
       CAST(g.revision || ':' || COALESCE(p.revision, 0) AS TEXT) AS revision,
       p.enabled AS override_enabled, g.enabled AS global_enabled,
       CAST(COALESCE(p.enabled, g.enabled) AS INTEGER) AS effective_enabled
FROM conversations c CROSS JOIN engine_read_receipt_settings g
LEFT JOIN conversation_read_receipt_settings p ON p.conversation_id=c.id
WHERE g.singleton=1 AND json_extract(c.payload,'$.Request') IS NULL
AND COALESCE(json_extract(c.payload,'$.RemovedFromChats'),0)=0;

DROP VIEW conversation_read_receipt_policy;
DROP TABLE conversation_read_receipt_settings;
DROP TABLE engine_read_receipt_settings;
ALTER TABLE account_communication_preferences ADD COLUMN send_read_receipts INTEGER NOT NULL DEFAULT 1 CHECK (send_read_receipts IN (0, 1));

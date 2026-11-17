-- name: GetAccountCommunicationPreferences :one
SELECT CAST(COALESCE(p.revision_id, '') AS TEXT) AS revision_id,
       CAST(COALESCE(p.presence, 'available') AS TEXT) AS presence,
       CAST(COALESCE(p.send_typing, 1) AS INTEGER) AS send_typing,
       CAST(COALESCE(p.send_read_receipts, 1) AS INTEGER) AS send_read_receipts
FROM accounts a
LEFT JOIN account_communication_preferences p ON p.account_id = a.id
WHERE a.id = sqlc.arg(account_id);

-- name: PutAccountCommunicationPreferences :exec
INSERT INTO account_communication_preferences(account_id,revision_id,presence,send_typing,send_read_receipts)
VALUES(?,?,?,?,?)
ON CONFLICT(account_id) DO UPDATE SET revision_id=excluded.revision_id,presence=excluded.presence,send_typing=excluded.send_typing,send_read_receipts=excluded.send_read_receipts;

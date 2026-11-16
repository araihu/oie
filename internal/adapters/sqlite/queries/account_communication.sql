-- name: GetAccountCommunicationPreferences :one
SELECT revision_id,presence,send_typing FROM account_communication_preferences WHERE account_id=?;

-- name: PutAccountCommunicationPreferences :exec
INSERT INTO account_communication_preferences(account_id,revision_id,presence,send_typing)
VALUES(?,?,?,?)
ON CONFLICT(account_id) DO UPDATE SET revision_id=excluded.revision_id,presence=excluded.presence,send_typing=excluded.send_typing;

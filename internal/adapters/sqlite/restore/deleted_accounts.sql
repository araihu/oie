INSERT INTO deleted_account_ids
SELECT d.id FROM backup.deleted_account_ids d
WHERE NOT EXISTS(SELECT 1 FROM accounts a WHERE a.id=d.id)
ON CONFLICT(id) DO NOTHING;

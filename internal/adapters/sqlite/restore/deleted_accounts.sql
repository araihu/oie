INSERT INTO deleted_account_ids
SELECT d.id FROM backup.deleted_account_ids d
WHERE NOT EXISTS(SELECT 1 FROM accounts a WHERE a.id=d.id)
ON CONFLICT(id) DO NOTHING;

INSERT INTO xmpp_removed_accounts SELECT s.* FROM backup.xmpp_removed_accounts s
WHERE NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id=s.id)
ON CONFLICT DO NOTHING;

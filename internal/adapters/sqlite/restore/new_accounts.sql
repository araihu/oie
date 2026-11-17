SELECT r.payload FROM restore_accounts a JOIN accounts d ON d.id=a.id
JOIN revisions r ON r.id=d.revision_id
WHERE a.is_new=1
ORDER BY a.id;

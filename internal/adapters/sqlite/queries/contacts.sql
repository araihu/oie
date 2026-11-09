-- name: ListContactRecords :many
SELECT * FROM contacts WHERE account_id=? ORDER BY address;

-- name: PutContact :exec
INSERT INTO contacts(id,revision_id,account_id,address,display_name,subscription,groups_json,active,payload)
VALUES(?,?,?,?,?,?,?,?,?)
ON CONFLICT(account_id,address) DO UPDATE SET revision_id=excluded.revision_id,display_name=excluded.display_name,subscription=excluded.subscription,groups_json=excluded.groups_json,active=excluded.active,payload=excluded.payload;

-- name: InsertContactRevision :exec
INSERT INTO contact_revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,?,?,?);

-- name: GetContactSource :one
SELECT * FROM contact_sources WHERE account_id=?;

-- name: PutContactSource :exec
INSERT INTO contact_sources(account_id,state,checked_at,updated_at) VALUES(?,?,?,?)
ON CONFLICT(account_id) DO UPDATE SET state=excluded.state,checked_at=excluded.checked_at,updated_at=excluded.updated_at;

-- name: SearchContacts :many
SELECT co.payload,a.label AS account_label,CAST(COALESCE(c.id,'') AS TEXT) AS conversation_id
FROM contacts co JOIN accounts a ON a.id=co.account_id
LEFT JOIN conversations c ON c.account_id=co.account_id AND c.address=co.address
WHERE co.active=1
AND (CAST(sqlc.arg(account_id) AS TEXT)='' OR co.account_id=CAST(sqlc.arg(account_id) AS TEXT))
AND co.id>CAST(sqlc.arg(cursor_id) AS TEXT)
AND (CAST(sqlc.arg(search) AS TEXT)='' OR oie_search_match(co.address||' '||co.display_name||' '||co.groups_json||' '||a.label,CAST(sqlc.arg(search) AS TEXT))=1)
ORDER BY co.id LIMIT sqlc.arg(page_limit);

-- name: ListContactSources :many
SELECT a.id AS account_id,CAST(COALESCE(s.state,'unknown') AS TEXT) AS state,CAST(COALESCE(s.checked_at,'') AS TEXT) AS checked_at
FROM accounts a LEFT JOIN contact_sources s ON s.account_id=a.id
WHERE (CAST(sqlc.arg(account_id) AS TEXT)='' OR a.id=CAST(sqlc.arg(account_id) AS TEXT))
ORDER BY a.id;

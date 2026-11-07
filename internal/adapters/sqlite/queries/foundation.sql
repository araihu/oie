-- name: InsertEngineIdentity :exec
INSERT INTO engine_identity(singleton,id) VALUES(1,?) ON CONFLICT(singleton) DO NOTHING;
-- name: Snapshot :one
SELECT id,CAST((SELECT COALESCE(MAX(sequence),0) FROM changes) AS INTEGER) AS sequence FROM engine_identity WHERE singleton=1;
-- name: InsertOperation :execrows
INSERT INTO operations(id,fingerprint,resource_id,result,created_at,kind) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING;
-- name: GetOperation :one
SELECT * FROM operations WHERE id=?;
-- name: SetOperationResult :exec
UPDATE operations SET resource_id=?,result=? WHERE id=?;
-- name: InsertAccount :exec
INSERT INTO accounts(id,revision_id,protocol,address,label,created_at) VALUES(?,?,?,?,?,?);
-- name: InsertAccountRevision :exec
INSERT INTO revisions(id,entity_id,parents,payload,created_at) VALUES(?,?,'[]',?,?);
-- name: GetAccount :one
SELECT r.payload FROM revisions r WHERE r.id=(SELECT a.revision_id FROM accounts a WHERE a.id=?);
-- name: ListAccounts :many
SELECT r.payload FROM accounts a JOIN revisions r ON a.revision_id=r.id WHERE a.id>? ORDER BY a.id LIMIT ?;
-- name: InsertChange :one
INSERT INTO changes(payload,occurred_at,kind) VALUES(?,?,?) RETURNING sequence;
-- name: ListChanges :many
SELECT * FROM changes WHERE sequence>? ORDER BY sequence LIMIT ?;

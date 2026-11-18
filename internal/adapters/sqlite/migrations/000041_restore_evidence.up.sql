CREATE TABLE deleted_account_ids (id TEXT PRIMARY KEY);
INSERT OR IGNORE INTO deleted_account_ids
SELECT resource_id FROM operations WHERE kind='register_account' AND result='null';
CREATE TRIGGER remember_deleted_account BEFORE DELETE ON accounts BEGIN
    INSERT OR IGNORE INTO deleted_account_ids(id) VALUES(OLD.id);
END;

CREATE TABLE recovered_send_revisions (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES recovered_sends(message_id) ON DELETE CASCADE,
    parents TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX recovered_send_revisions_message ON recovered_send_revisions(message_id);

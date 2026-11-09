CREATE TABLE contacts (
 id TEXT PRIMARY KEY,
 revision_id TEXT NOT NULL,
 account_id TEXT NOT NULL REFERENCES accounts(id),
 address TEXT NOT NULL,
 display_name TEXT NOT NULL,
 subscription TEXT NOT NULL CHECK(subscription IN ('none','to','from','both')),
 groups_json TEXT NOT NULL CHECK(json_valid(groups_json)),
 active INTEGER NOT NULL CHECK(active IN (0,1)),
 payload TEXT NOT NULL CHECK(json_valid(payload)),
 UNIQUE(account_id,address)
);
CREATE INDEX contacts_account ON contacts(account_id,id);
CREATE TABLE contact_revisions (
 id TEXT PRIMARY KEY,
 entity_id TEXT NOT NULL REFERENCES contacts(id),
 parents TEXT NOT NULL CHECK(json_valid(parents)),
 payload TEXT NOT NULL CHECK(json_valid(payload)),
 created_at TEXT NOT NULL
);
CREATE TABLE contact_sources (
 account_id TEXT PRIMARY KEY REFERENCES accounts(id),
 state TEXT NOT NULL CHECK(state IN ('known','unavailable')),
 checked_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);

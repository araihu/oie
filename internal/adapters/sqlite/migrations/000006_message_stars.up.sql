CREATE TABLE message_stars (
 message_id TEXT PRIMARY KEY REFERENCES messages(id),
 revision_id TEXT NOT NULL UNIQUE REFERENCES message_star_revisions(id) DEFERRABLE INITIALLY DEFERRED,
 starred INTEGER NOT NULL CHECK(starred IN (0,1)),
 payload TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE TABLE message_star_revisions (
 id TEXT PRIMARY KEY,
 message_id TEXT NOT NULL REFERENCES messages(id),
 parents TEXT NOT NULL,
 payload TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX message_stars_selected ON message_stars(starred,message_id);
PRAGMA user_version=6;

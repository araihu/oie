CREATE TABLE room_archives (
 conversation_id TEXT PRIMARY KEY REFERENCES conversations(id),
 mode TEXT NOT NULL DEFAULT 'gradual' CHECK(mode IN ('gradual','on_demand')),
 progress TEXT NOT NULL DEFAULT '{}'
);
PRAGMA user_version=17;

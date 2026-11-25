-- Position in the relay hand-off, advanced in the transaction of each input.
CREATE TABLE handoff_cursor (
 id INTEGER PRIMARY KEY CHECK(id = 1),
 seq INTEGER NOT NULL DEFAULT 0,
 receipt_id TEXT NOT NULL DEFAULT '',
 -- Last DMDATA connection generation seen; a change may hide missed reports.
 upstream_generation TEXT NOT NULL DEFAULT ''
);
INSERT INTO handoff_cursor(id) VALUES(1);

-- Revocable per-device bearer credentials. Only the token's SHA-256 is stored.
CREATE TABLE tokens (
 id TEXT PRIMARY KEY,
 name TEXT NOT NULL,
 token_sha256 TEXT NOT NULL UNIQUE,
 environment TEXT NOT NULL CHECK(environment IN ('production')),
 created_ms INTEGER NOT NULL,
 revoked_ms INTEGER
);

CREATE TABLE devices (
 installation_id TEXT PRIMARY KEY,
 credential_hash TEXT NOT NULL UNIQUE,
 revoked INTEGER NOT NULL DEFAULT 0,
 token TEXT NOT NULL DEFAULT '',
 environment TEXT NOT NULL DEFAULT 'development',
 push_active INTEGER NOT NULL DEFAULT 0,
 token_updated_ms INTEGER NOT NULL DEFAULT 0,
 device_name TEXT NOT NULL DEFAULT '',
 app_version TEXT NOT NULL DEFAULT '',
 os_version TEXT NOT NULL DEFAULT '',
 preferences TEXT NOT NULL,
 last_seen_at TEXT NOT NULL
);
CREATE UNIQUE INDEX devices_token ON devices(environment,token) WHERE token <> '';
CREATE TABLE pairing (
 code_hash TEXT PRIMARY KEY,
 expires INTEGER NOT NULL,
 installation_id TEXT NOT NULL DEFAULT '',
 failures INTEGER NOT NULL DEFAULT 0
);

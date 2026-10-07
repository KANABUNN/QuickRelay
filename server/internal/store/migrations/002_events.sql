CREATE TABLE reports (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 message_id TEXT NOT NULL UNIQUE,
 fingerprint TEXT NOT NULL UNIQUE,
 event_id TEXT NOT NULL,
 telegram_type TEXT NOT NULL,
 payload TEXT NOT NULL,
 raw BLOB NOT NULL,
 received_ms INTEGER NOT NULL
);
CREATE INDEX reports_event ON reports(event_id,sequence);
CREATE TABLE streams (
 event_id TEXT NOT NULL,
 telegram_type TEXT NOT NULL,
 payload TEXT NOT NULL,
 PRIMARY KEY(event_id,telegram_type)
);
CREATE TABLE events (
 event_id TEXT PRIMARY KEY,
 sequence INTEGER NOT NULL,
 payload TEXT NOT NULL
);
CREATE INDEX events_sequence ON events(sequence DESC);

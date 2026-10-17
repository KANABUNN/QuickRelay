-- Lossless receipt journal, committed before decompression or interpretation.
-- A frame hash also retains malformed and unknown messages without trusting their IDs.
CREATE TABLE received_telegrams (
 frame_sha256 TEXT PRIMARY KEY,
 frame BLOB NOT NULL,
 received_ms INTEGER NOT NULL,
 last_received_ms INTEGER NOT NULL,
 receptions INTEGER NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','normalized','ignored','rejected')),
 report_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX received_telegrams_time ON received_telegrams(received_ms);

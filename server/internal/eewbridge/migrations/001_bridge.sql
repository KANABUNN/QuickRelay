-- Current EEW state per event: the delivered JSON and bookkeeping beside it.
CREATE TABLE events (
 environment TEXT NOT NULL,
 event_id TEXT NOT NULL,
 state TEXT NOT NULL,
 meta TEXT NOT NULL,
 terminal INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL,
 PRIMARY KEY(environment, event_id)
);
CREATE INDEX events_updated ON events(updated_ms);

-- One row per input frame with the result of applying it. Also the duplicate
-- and identifier-conflict check, and the quarantine record.
CREATE TABLE inputs (
 receipt_id TEXT PRIMARY KEY,
 telegram_id TEXT NOT NULL DEFAULT '',
 code TEXT NOT NULL DEFAULT '',
 event_id TEXT NOT NULL DEFAULT '',
 serial INTEGER,
 info_type TEXT NOT NULL DEFAULT '',
 content_hash TEXT NOT NULL DEFAULT '',
 ingress_seq INTEGER NOT NULL DEFAULT 0,
 origin TEXT NOT NULL,
 upstream TEXT NOT NULL DEFAULT '',
 relay_applied INTEGER,
 received_ms INTEGER NOT NULL,
 processed_ms INTEGER NOT NULL,
 outcome TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 changed_series TEXT NOT NULL DEFAULT '',
 log_seq INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX inputs_telegram ON inputs(telegram_id);
CREATE INDEX inputs_processed ON inputs(processed_ms);

-- Delivery streams: one continuous, ordered log per authorization scope.
-- epoch changes only when the log cannot continue (rebuild, restore).
CREATE TABLE streams (
 stream_id TEXT PRIMARY KEY,
 epoch TEXT NOT NULL,
 last_seq INTEGER NOT NULL DEFAULT 0,
 purged_through INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE stream_log (
 stream_id TEXT NOT NULL REFERENCES streams(stream_id),
 seq INTEGER NOT NULL,
 environment TEXT NOT NULL,
 event_id TEXT NOT NULL,
 cause TEXT NOT NULL CHECK(cause IN ('telegram','reconcile')),
 changed_series TEXT NOT NULL,
 committed_ms INTEGER NOT NULL,
 event TEXT NOT NULL,
 PRIMARY KEY(stream_id, seq)
);
CREATE INDEX stream_log_committed ON stream_log(committed_ms);

-- Live EEW reports handed to the separate EEW bridge service, written in the
-- ingest transaction. AUTOINCREMENT keeps positions monotonic after pruning.
CREATE TABLE eew_handoff (
 ingress_seq INTEGER PRIMARY KEY AUTOINCREMENT,
 receipt_id TEXT NOT NULL UNIQUE,
 report_sequence INTEGER NOT NULL UNIQUE,
 report_id TEXT NOT NULL,
 telegram_type TEXT NOT NULL CHECK(telegram_type IN ('VXSE43','VXSE45')),
 received_ms INTEGER NOT NULL,
 committed_ms INTEGER NOT NULL,
 upstream_generation TEXT NOT NULL,
 -- Stream state QuickRelay committed for this report; NULL when it was not applied.
 applied TEXT
);
CREATE INDEX eew_handoff_committed ON eew_handoff(committed_ms);
CREATE TABLE eew_handoff_meta (
 id INTEGER PRIMARY KEY CHECK(id = 1),
 purged_through INTEGER NOT NULL DEFAULT 0
);
INSERT INTO eew_handoff_meta(id) VALUES(1);

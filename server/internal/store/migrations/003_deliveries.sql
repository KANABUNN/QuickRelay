CREATE TABLE deliveries (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 report_sequence INTEGER NOT NULL REFERENCES reports(sequence),
 device_id TEXT NOT NULL REFERENCES devices(installation_id),
 status TEXT NOT NULL DEFAULT 'pending',
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_ms INTEGER NOT NULL,
 expires_ms INTEGER NOT NULL,
 lease_until_ms INTEGER NOT NULL DEFAULT 0,
 apns_id TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '',
 updated_ms INTEGER NOT NULL,
 UNIQUE(report_sequence,device_id)
);
CREATE INDEX deliveries_ready ON deliveries(status,next_attempt_ms,lease_until_ms);

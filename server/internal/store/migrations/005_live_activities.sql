CREATE TABLE live_activity_start_tokens (
 device_id TEXT PRIMARY KEY REFERENCES devices(installation_id),
 token TEXT NOT NULL,
 updated_ms INTEGER NOT NULL
);
CREATE TABLE live_activities (
 device_id TEXT NOT NULL REFERENCES devices(installation_id),
 event_id TEXT NOT NULL,
 telegram_type TEXT NOT NULL,
 activity_id TEXT NOT NULL DEFAULT '',
 token TEXT NOT NULL DEFAULT '',
 token_updated_ms INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'starting',
 start_sequence INTEGER NOT NULL DEFAULT 0,
 last_sequence INTEGER NOT NULL DEFAULT 0,
 last_timestamp INTEGER NOT NULL DEFAULT 0,
 created_ms INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL,
 PRIMARY KEY(device_id,event_id,telegram_type)
);
CREATE TABLE live_activity_jobs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 report_sequence INTEGER NOT NULL REFERENCES reports(sequence),
 device_id TEXT NOT NULL REFERENCES devices(installation_id),
 force_end INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT 'pending',
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_ms INTEGER NOT NULL,
 expires_ms INTEGER NOT NULL,
 lease_until_ms INTEGER NOT NULL DEFAULT 0,
 reason TEXT NOT NULL DEFAULT '',
 updated_ms INTEGER NOT NULL,
 UNIQUE(report_sequence,device_id)
);
CREATE INDEX live_activity_jobs_ready ON live_activity_jobs(status,next_attempt_ms,lease_until_ms);

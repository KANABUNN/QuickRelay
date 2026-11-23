CREATE INDEX streams_tsunami_publication ON streams(julianday(json_extract(payload,'$.occurred_at'))) WHERE telegram_type='VTSE41';
CREATE INDEX reports_by_telegram ON reports(telegram_type,sequence DESC);
ALTER TABLE deliveries ADD COLUMN tsunami_warning_areas TEXT;

-- A VTSE41 publication may wake multiple activities created under earlier event IDs.
CREATE TABLE live_activity_jobs_scoped (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 report_sequence INTEGER NOT NULL REFERENCES reports(sequence),
 device_id TEXT NOT NULL REFERENCES devices(installation_id),
 event_id TEXT NOT NULL,
 force_end INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL DEFAULT 'pending',
 attempts INTEGER NOT NULL DEFAULT 0,
 next_attempt_ms INTEGER NOT NULL,
 expires_ms INTEGER NOT NULL,
 lease_until_ms INTEGER NOT NULL DEFAULT 0,
 reason TEXT NOT NULL DEFAULT '',
 updated_ms INTEGER NOT NULL,
 UNIQUE(report_sequence,device_id,event_id)
);
INSERT INTO live_activity_jobs_scoped
 SELECT j.id,j.report_sequence,j.device_id,r.event_id,j.force_end,j.status,j.attempts,
        j.next_attempt_ms,j.expires_ms,j.lease_until_ms,j.reason,j.updated_ms
 FROM live_activity_jobs j JOIN reports r ON r.sequence=j.report_sequence;
DROP TABLE live_activity_jobs;
ALTER TABLE live_activity_jobs_scoped RENAME TO live_activity_jobs;
CREATE INDEX live_activity_jobs_ready ON live_activity_jobs(status,next_attempt_ms,lease_until_ms);

CREATE TABLE notification_tests (
 id TEXT PRIMARY KEY,
 device_id TEXT NOT NULL REFERENCES devices(installation_id),
 style TEXT NOT NULL,
 status TEXT NOT NULL,
 requested_ms INTEGER NOT NULL,
 updated_ms INTEGER NOT NULL
);
CREATE INDEX notification_tests_device ON notification_tests(device_id,requested_ms DESC);

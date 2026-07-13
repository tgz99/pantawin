-- +goose Up
-- Incidents: one row per DOWN period (spec section 5). started_at set on
-- UP->DOWN; resolved_at filled on DOWN->UP recovery.
CREATE TABLE IF NOT EXISTS incidents (
    id          BIGSERIAL PRIMARY KEY,
    monitor_id  BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ,
    cause       TEXT,
    notified    BOOLEAN NOT NULL DEFAULT false
);

CREATE INDEX IF NOT EXISTS idx_incidents_monitor ON incidents (monitor_id, started_at DESC);

-- At most one unresolved (ongoing) incident per monitor — a DOWN monitor
-- can't have two open incidents at once.
CREATE UNIQUE INDEX IF NOT EXISTS idx_incidents_one_open_per_monitor
    ON incidents (monitor_id) WHERE resolved_at IS NULL;

-- notification_log: the idempotency + audit ledger. The UNIQUE constraint
-- is what guarantees exactly one notification per (incident, channel,
-- event_type) even across worker retries (spec 7.2 item 5).
CREATE TABLE IF NOT EXISTS notification_log (
    id          BIGSERIAL PRIMARY KEY,
    incident_id BIGINT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    channel     TEXT NOT NULL,
    event_type  TEXT NOT NULL,
    sent_at     TIMESTAMPTZ,
    ok          BOOLEAN,
    UNIQUE (incident_id, channel, event_type)
);

-- Per-monitor alert config (spec section 5). alert_email defaults to NULL,
-- in which case the owner's account email is used.
ALTER TABLE monitors ADD COLUMN IF NOT EXISTS alert_channels TEXT[] NOT NULL DEFAULT '{email}';
ALTER TABLE monitors ADD COLUMN IF NOT EXISTS alert_email TEXT;

-- +goose Down
ALTER TABLE monitors DROP COLUMN IF EXISTS alert_email;
ALTER TABLE monitors DROP COLUMN IF EXISTS alert_channels;
DROP TABLE IF EXISTS notification_log;
DROP TABLE IF EXISTS incidents;

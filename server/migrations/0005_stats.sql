-- +goose Up
-- Analytics rollups (spec 3.3, M4). Hourly is the working set; daily is
-- kept forever. up_pct is NULL when the monitor had no activity in the
-- bucket (paused or not yet created) — distinct from 0%, which means down.
CREATE TABLE IF NOT EXISTS stats_hourly (
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    hour_ts    TIMESTAMPTZ NOT NULL, -- start of the UTC hour
    checks     INTEGER NOT NULL DEFAULT 0,
    fails      INTEGER NOT NULL DEFAULT 0,
    up_pct     DOUBLE PRECISION,
    avg_ms     DOUBLE PRECISION,
    p95_ms     DOUBLE PRECISION,
    PRIMARY KEY (monitor_id, hour_ts)
);

CREATE TABLE IF NOT EXISTS stats_daily (
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    day        DATE NOT NULL, -- UTC day
    checks     INTEGER NOT NULL DEFAULT 0,
    fails      INTEGER NOT NULL DEFAULT 0,
    up_pct     DOUBLE PRECISION,
    avg_ms     DOUBLE PRECISION,
    p95_ms     DOUBLE PRECISION,
    downtime_s INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (monitor_id, day)
);

-- +goose Down
DROP TABLE IF EXISTS stats_daily;
DROP TABLE IF EXISTS stats_hourly;

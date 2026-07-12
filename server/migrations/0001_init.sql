-- +goose Up
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS monitors (
    id                   BIGSERIAL PRIMARY KEY,
    user_id              BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    url                  TEXT NOT NULL,
    method               TEXT NOT NULL DEFAULT 'GET',
    interval_seconds     INTEGER NOT NULL DEFAULT 60,
    timeout_ms           INTEGER NOT NULL DEFAULT 10000,
    expected_status_min  INTEGER NOT NULL DEFAULT 200,
    expected_status_max  INTEGER NOT NULL DEFAULT 399,
    failure_threshold    INTEGER NOT NULL DEFAULT 2,
    status               TEXT NOT NULL DEFAULT 'PENDING'
                         CHECK (status IN ('UP', 'DOWN', 'PAUSED', 'PENDING')),
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Not partitioned yet at M0 volume; partitioning by month is a fast-follow
-- once raw retention actually matters (spec section 5).
CREATE TABLE IF NOT EXISTS check_results (
    id               BIGSERIAL PRIMARY KEY,
    monitor_id       BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    checked_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    ok               BOOLEAN NOT NULL,
    http_code        INTEGER,
    response_time_ms INTEGER,
    error_type       TEXT
);

CREATE INDEX IF NOT EXISTS idx_check_results_monitor_checked_at
    ON check_results (monitor_id, checked_at DESC);

-- +goose Down
DROP TABLE IF EXISTS check_results;
DROP TABLE IF EXISTS monitors;
DROP TABLE IF EXISTS users;

-- +goose Up
-- Bounded retry for failed notification sends. `payload` stores the
-- IncidentEvent JSON at claim time so the retrier can re-send without
-- reconstructing state; `attempts` bounds the retries (rows predating this
-- migration have payload NULL and are never retried). ok=false + attempts
-- below the cap marks a row as retryable.
ALTER TABLE notification_log
    ADD COLUMN attempts INTEGER NOT NULL DEFAULT 1,
    ADD COLUMN payload JSONB;

-- +goose Down
ALTER TABLE notification_log
    DROP COLUMN payload,
    DROP COLUMN attempts;

-- +goose Up
-- Registered FCM device tokens (spec section 5). One row per device token;
-- the same token re-registering just updates the timestamp/owner.
CREATE TABLE IF NOT EXISTS devices (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fcm_token  TEXT NOT NULL UNIQUE,
    platform   TEXT NOT NULL DEFAULT 'android',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_devices_user ON devices (user_id);

-- +goose Down
DROP TABLE IF EXISTS devices;

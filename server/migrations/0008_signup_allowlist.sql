-- +goose Up
-- M6.1: in-app team management. Emails invited by the admin; new-account
-- creation (register + Google find-or-create) is only allowed for emails in
-- this table (or the SIGNUP_ALLOWLIST env escape hatch). Stored lowercase.
CREATE TABLE IF NOT EXISTS signup_allowlist (
    email      TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE signup_allowlist;

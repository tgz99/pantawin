-- +goose Up
-- M6.2: email/password registration requires OTP verification; Google SSO
-- registration does not (Google already verified the email). Existing rows
-- (bootstrap admin, prior registrations, Google accounts) default to
-- verified so nobody already using the app gets locked out retroactively.
ALTER TABLE users ADD COLUMN email_verified BOOLEAN NOT NULL DEFAULT true;

CREATE TABLE IF NOT EXISTS email_otps (
    email      TEXT PRIMARY KEY,
    code_hash  TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE email_otps;
ALTER TABLE users DROP COLUMN email_verified;

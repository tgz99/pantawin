-- +goose Up
-- M6: personal vs team monitors. 'personal' keeps the pre-M6 behavior
-- (visible/alerted to the owner only); 'team' monitors are visible to every
-- user and alert everyone. Existing rows stay personal.
ALTER TABLE monitors
    ADD COLUMN scope TEXT NOT NULL DEFAULT 'personal'
    CHECK (scope IN ('personal', 'team'));

-- +goose Down
ALTER TABLE monitors DROP COLUMN scope;

-- +goose Up
-- M6.3: teams become first-class and plural. Previously there was one
-- implicit team (every registered account); now any account can create a
-- team, invite others into it, and belong to several teams at once.
CREATE TABLE teams (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    owner_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE team_members (
    team_id   BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id   BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, user_id)
);

-- A pending invite only ever exists for an email with no account yet; once
-- that email registers, ConsumeInvitesOnAccountCreated turns every matching
-- row here into a team_members row and deletes it. An invite to an email
-- that already has an account skips this table and joins team_members
-- immediately (see team.Repository.Invite).
CREATE TABLE team_invites (
    team_id    BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    email      TEXT NOT NULL,
    invited_by BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (team_id, email)
);

ALTER TABLE monitors ADD COLUMN team_id BIGINT REFERENCES teams(id) ON DELETE CASCADE;

-- Fold the old single implicit team (M6/M6.1) into a real "Legacy Team" so
-- deployments that already have team-scoped monitors and pending invites
-- don't lose them. No-op on a fresh database (no users yet).
-- +goose StatementBegin
DO $$
DECLARE
    legacy_team_id BIGINT;
    admin_id BIGINT;
BEGIN
    SELECT id INTO admin_id FROM users ORDER BY id LIMIT 1;
    IF admin_id IS NOT NULL THEN
        INSERT INTO teams (name, owner_id) VALUES ('Legacy Team', admin_id)
        RETURNING id INTO legacy_team_id;

        INSERT INTO team_members (team_id, user_id)
        SELECT legacy_team_id, id FROM users;

        INSERT INTO team_invites (team_id, email, invited_by)
        SELECT legacy_team_id, email, admin_id FROM signup_allowlist
        ON CONFLICT DO NOTHING;

        UPDATE monitors SET team_id = legacy_team_id WHERE scope = 'team';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE monitors ADD CONSTRAINT monitors_team_scope_check
    CHECK ((scope = 'personal' AND team_id IS NULL) OR (scope = 'team' AND team_id IS NOT NULL));

DROP TABLE signup_allowlist;

-- +goose Down
ALTER TABLE monitors DROP CONSTRAINT monitors_team_scope_check;
ALTER TABLE monitors DROP COLUMN team_id;
DROP TABLE team_invites;
DROP TABLE team_members;
DROP TABLE teams;
CREATE TABLE IF NOT EXISTS signup_allowlist (
    email      TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

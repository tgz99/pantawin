// Package team manages teams (M6.3): any registered account can create a
// team and invite others into it, and an account can belong to any number
// of teams. There is no special "admin" role — every action here is gated
// on the caller being a member of the team in question.
package team

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

type Team struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	OwnerID   int64     `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Member is one row of a team's member list: an invited email and whether
// an account has joined yet.
type Member struct {
	Email   string    `json:"email"`
	Joined  bool      `json:"joined"`
	AddedAt time.Time `json:"added_at"`
}

func normalize(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Create makes a new team owned by userID, with the owner as its first
// member.
func (r *Repository) Create(ctx context.Context, ownerID int64, name string) (Team, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Team{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var t Team
	if err := tx.QueryRow(ctx, `
		INSERT INTO teams (name, owner_id) VALUES ($1, $2)
		RETURNING id, name, owner_id, created_at
	`, name, ownerID).Scan(&t.ID, &t.Name, &t.OwnerID, &t.CreatedAt); err != nil {
		return Team{}, fmt.Errorf("insert team: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO team_members (team_id, user_id) VALUES ($1, $2)`, t.ID, ownerID,
	); err != nil {
		return Team{}, fmt.Errorf("add owner as member: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Team{}, err
	}
	return t, nil
}

// ListForUser returns every team the user belongs to.
func (r *Repository) ListForUser(ctx context.Context, userID int64) ([]Team, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.name, t.owner_id, t.created_at
		FROM teams t JOIN team_members tm ON tm.team_id = t.id
		WHERE tm.user_id = $1
		ORDER BY t.id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.OwnerID, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// IsMember reports whether userID belongs to teamID — the authorization
// check behind every per-team action (invite, view members, create/move a
// team monitor into it).
func (r *Repository) IsMember(ctx context.Context, teamID, userID int64) (bool, error) {
	var ok bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM team_members WHERE team_id = $1 AND user_id = $2)`,
		teamID, userID,
	).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check membership: %w", err)
	}
	return ok, nil
}

// TeamIDsForUser returns every team a user belongs to — used to subscribe a
// WebSocket connection to all of that user's team channels.
func (r *Repository) TeamIDsForUser(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT team_id FROM team_members WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("list team ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan team id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ListMembers returns a team's invited + joined emails (union), so a member
// can see who's in and who's still pending.
func (r *Repository) ListMembers(ctx context.Context, teamID int64) ([]Member, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT lower(u.email) AS email, true AS joined, tm.joined_at AS added_at
		FROM team_members tm JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = $1
		UNION ALL
		SELECT email, false AS joined, created_at AS added_at
		FROM team_invites
		WHERE team_id = $1
		ORDER BY email
	`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Email, &m.Joined, &m.AddedAt); err != nil {
			return nil, fmt.Errorf("scan team member: %w", err)
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// Invite adds email to teamID: immediately, if an account already exists for
// that address; otherwise as a pending invite, consumed the moment that
// email registers (see ConsumeInvitesOnAccountCreated). Idempotent either
// way — re-inviting is a no-op.
func (r *Repository) Invite(ctx context.Context, teamID, invitedBy int64, email string) error {
	email = normalize(email)

	var userID int64
	err := r.pool.QueryRow(ctx, `SELECT id FROM users WHERE lower(email) = $1`, email).Scan(&userID)
	if err == nil {
		_, err = r.pool.Exec(ctx, `
			INSERT INTO team_members (team_id, user_id) VALUES ($1, $2)
			ON CONFLICT (team_id, user_id) DO NOTHING
		`, teamID, userID)
		if err != nil {
			return fmt.Errorf("add existing user to team: %w", err)
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("lookup invited user: %w", err)
	}

	_, err = r.pool.Exec(ctx, `
		INSERT INTO team_invites (team_id, email, invited_by) VALUES ($1, $2, $3)
		ON CONFLICT (team_id, email) DO NOTHING
	`, teamID, email, invitedBy)
	if err != nil {
		return fmt.Errorf("create team invite: %w", err)
	}
	return nil
}

// HasJoined reports whether an account belonging to email is already a
// member of teamID (used to distinguish "pending invite" from "already a
// member" when removing).
func (r *Repository) HasJoined(ctx context.Context, teamID int64, email string) (bool, error) {
	var joined bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM team_members tm JOIN users u ON u.id = tm.user_id
			WHERE tm.team_id = $1 AND lower(u.email) = $2
		)
	`, teamID, normalize(email)).Scan(&joined)
	if err != nil {
		return false, fmt.Errorf("check joined: %w", err)
	}
	return joined, nil
}

// RemoveInvite withdraws a pending (not-yet-joined) invite. Removing a
// joined member is a separate "leave/kick" feature this doesn't cover.
func (r *Repository) RemoveInvite(ctx context.Context, teamID int64, email string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM team_invites WHERE team_id = $1 AND email = $2`, teamID, normalize(email))
	if err != nil {
		return false, fmt.Errorf("remove team invite: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Allowed is the auth service's signup gate (M6.1, repointed at team_invites
// in M6.3): is this email invited to at least one team?
func (r *Repository) Allowed(ctx context.Context, email string) (bool, error) {
	var allowed bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM team_invites WHERE email = $1)`, normalize(email),
	).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check allowlist: %w", err)
	}
	return allowed, nil
}

// ConsumeInvitesOnAccountCreated moves every pending invite for email into
// real memberships for the newly created userID, then clears those invites.
// Called once, at the moment a users row for that email first exists
// (Register, or Google creating a brand-new account) — after that, Invite()
// always finds the account and adds directly, so no invite for this email
// is ever pending again. Safe to call redundantly (e.g. re-registering
// before verifying): a second call just finds nothing left to consume.
func (r *Repository) ConsumeInvitesOnAccountCreated(ctx context.Context, userID int64, email string) error {
	email = normalize(email)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT team_id FROM team_invites WHERE email = $1`, email)
	if err != nil {
		return fmt.Errorf("query pending invites: %w", err)
	}
	var teamIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan pending invite: %w", err)
		}
		teamIDs = append(teamIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, teamID := range teamIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO team_members (team_id, user_id) VALUES ($1, $2)
			ON CONFLICT (team_id, user_id) DO NOTHING
		`, teamID, userID); err != nil {
			return fmt.Errorf("join team from invite: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM team_invites WHERE email = $1`, email); err != nil {
		return fmt.Errorf("clear consumed invites: %w", err)
	}
	return tx.Commit(ctx)
}

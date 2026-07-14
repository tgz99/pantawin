// Package team manages the signup allowlist as in-app "team membership"
// (M6.1): the admin invites an email, the teammate signs in with Google, and
// the account is created because the email is invited. One implicit team.
package team

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Member is one row of the team screen: an invited email and whether an
// account exists for it yet.
type Member struct {
	Email   string    `json:"email"`
	Joined  bool      `json:"joined"`
	AddedAt time.Time `json:"added_at"`
}

func normalize(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// List returns every invited email plus every registered account (the union),
// so the admin sees pre-invite accounts (e.g. the bootstrap admin) too.
func (r *Repository) List(ctx context.Context) ([]Member, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT COALESCE(a.email, lower(u.email)) AS email,
		       u.id IS NOT NULL AS joined,
		       COALESCE(a.created_at, u.created_at) AS added_at
		FROM signup_allowlist a
		FULL OUTER JOIN users u ON lower(u.email) = a.email
		ORDER BY email
	`)
	if err != nil {
		return nil, fmt.Errorf("list team: %w", err)
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

// Add invites an email. Idempotent — re-inviting is a no-op.
func (r *Repository) Add(ctx context.Context, email string) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO signup_allowlist (email) VALUES ($1)
		ON CONFLICT (email) DO NOTHING
	`, normalize(email))
	if err != nil {
		return fmt.Errorf("add team member: %w", err)
	}
	return nil
}

// Remove withdraws an invite. It does NOT delete an account that already
// joined — the handler refuses that case explicitly.
func (r *Repository) Remove(ctx context.Context, email string) (bool, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM signup_allowlist WHERE email = $1`, normalize(email))
	if err != nil {
		return false, fmt.Errorf("remove team member: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// HasJoined reports whether an account already exists for the email.
func (r *Repository) HasJoined(ctx context.Context, email string) (bool, error) {
	var joined bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE lower(email) = $1)`, normalize(email),
	).Scan(&joined)
	if err != nil {
		return false, fmt.Errorf("check joined: %w", err)
	}
	return joined, nil
}

// Allowed is the auth service's signup gate: is this email invited?
func (r *Repository) Allowed(ctx context.Context, email string) (bool, error) {
	var allowed bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM signup_allowlist WHERE email = $1)`, normalize(email),
	).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check allowlist: %w", err)
	}
	return allowed, nil
}

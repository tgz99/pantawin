package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrUserNotFound = errors.New("user not found")
var ErrEmailAlreadyRegistered = errors.New("email already registered")

type User struct {
	ID            int64
	Email         string
	PasswordHash  string
	EmailVerified bool
}

const userColumns = `id, email, password_hash, email_verified`

func (u *User) scanFields() []any {
	return []any{&u.ID, &u.Email, &u.PasswordHash, &u.EmailVerified}
}

type Repository struct {
	pool *pgxpool.Pool
	// otpTTL/otpResendCooldown are tunable for tests; production uses the
	// package defaults (see WithOTPTuning).
	otpTTL            time.Duration
	otpResendCooldown time.Duration
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, otpTTL: defaultOTPTTL, otpResendCooldown: defaultOTPResendCooldown}
}

// CreateUser inserts a brand-new account. verified=true for Google sign-in
// (Google already confirmed the email) and the bootstrap admin; email/password
// registration goes through CreateOrReplaceUnverifiedUser instead so email
// stays unverified until the OTP step completes (M6.2).
func (r *Repository) CreateUser(ctx context.Context, email, passwordHash string, verified bool) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, email_verified) VALUES ($1, $2, $3)
		 RETURNING `+userColumns,
		email, passwordHash, verified,
	).Scan(u.scanFields()...)
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrEmailAlreadyRegistered
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// CreateOrReplaceUnverifiedUser inserts a new unverified account, or — if an
// account with this email already exists but was never verified (e.g. the
// user registered, lost the OTP email, and is trying again) — replaces its
// password hash and returns it. An email belonging to an already-verified
// account is left untouched: ErrEmailAlreadyRegistered, same as a plain
// unique-constraint conflict.
func (r *Repository) CreateOrReplaceUnverifiedUser(ctx context.Context, email, passwordHash string) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, email_verified)
		VALUES ($1, $2, false)
		ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash
		WHERE users.email_verified = false
		RETURNING `+userColumns,
		email, passwordHash,
	).Scan(u.scanFields()...)
	if errors.Is(err, pgx.ErrNoRows) {
		// Conflict existed but the WHERE excluded it — the email belongs to
		// an already-verified account.
		return User{}, ErrEmailAlreadyRegistered
	}
	if err != nil {
		return User{}, fmt.Errorf("upsert unverified user: %w", err)
	}
	return u, nil
}

func (r *Repository) GetUserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = $1`, email,
	).Scan(u.scanFields()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query user by email: %w", err)
	}
	return u, nil
}

func (r *Repository) GetUserByID(ctx context.Context, id int64) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id,
	).Scan(u.scanFields()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query user by id: %w", err)
	}
	return u, nil
}

// MarkEmailVerified flips the verified flag — called once OTP verification
// succeeds (or when Google sign-in vouches for an email that was still
// pending OTP verification).
func (r *Repository) MarkEmailVerified(ctx context.Context, email string) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET email_verified = true WHERE email = $1`, email)
	if err != nil {
		return fmt.Errorf("mark email verified: %w", err)
	}
	return nil
}

// UpdatePassword replaces a user's password hash.
func (r *Repository) UpdatePassword(ctx context.Context, userID int64, passwordHash string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, passwordHash,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

func (r *Repository) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func isUniqueViolation(err error) bool {
	// pgx wraps *pgconn.PgError; SQLSTATE 23505 is unique_violation.
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == "23505"
	}
	return false
}

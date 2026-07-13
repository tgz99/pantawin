package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrRefreshTokenInvalid = errors.New("refresh token invalid, expired, or already used")

// RefreshStore persists hashes of issued refresh tokens so they are
// single-use (rotation) and revocable. Only the SHA-256 of the token is
// stored — a database leak doesn't yield usable tokens.
type RefreshStore struct {
	pool *pgxpool.Pool
}

func NewRefreshStore(pool *pgxpool.Pool) *RefreshStore {
	return &RefreshStore{pool: pool}
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *RefreshStore) Save(ctx context.Context, userID int64, rawToken string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, hashToken(rawToken), expiresAt)
	if err != nil {
		return fmt.Errorf("save refresh token: %w", err)
	}
	return nil
}

// Consume atomically marks the token used (revoked) and returns its owner.
// A token that is unknown, expired, or previously consumed fails — this is
// what makes rotation effective: replaying an old refresh token after it
// was rotated is rejected.
func (s *RefreshStore) Consume(ctx context.Context, rawToken string) (userID int64, err error) {
	err = s.pool.QueryRow(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()
		RETURNING user_id
	`, hashToken(rawToken)).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRefreshTokenInvalid
	}
	if err != nil {
		return 0, fmt.Errorf("consume refresh token: %w", err)
	}
	return userID, nil
}

// RevokeAllForUser deletes every refresh token a user holds — called on
// password change so stolen/old sessions can't silently refresh anymore.
func (s *RefreshStore) RevokeAllForUser(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("revoke refresh tokens: %w", err)
	}
	return nil
}

// PurgeExpired deletes rows that no longer matter (expired or long-revoked)
// — called opportunistically, not on a scheduler, at M1 volume.
func (s *RefreshStore) PurgeExpired(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM refresh_tokens
		WHERE expires_at < now() - interval '7 days'
	`)
	return err
}

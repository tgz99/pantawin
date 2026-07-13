// Package device stores registered FCM push tokens (spec section 4:
// POST /devices).
package device

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Register upserts an FCM token. If the token already exists (same physical
// device re-registering, possibly under a new account), its owner and
// updated_at are refreshed rather than creating a duplicate.
func (r *Repository) Register(ctx context.Context, userID int64, fcmToken, platform string) error {
	if platform == "" {
		platform = "android"
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO devices (user_id, fcm_token, platform)
		VALUES ($1, $2, $3)
		ON CONFLICT (fcm_token)
		DO UPDATE SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform, updated_at = now()
	`, userID, fcmToken, platform)
	if err != nil {
		return fmt.Errorf("register device: %w", err)
	}
	return nil
}

// TokensForUser returns every FCM token registered to a user — the push
// dispatcher fans a notification out to all of them.
func (r *Repository) TokensForUser(ctx context.Context, userID int64) ([]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT fcm_token FROM devices WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("query device tokens: %w", err)
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// Delete removes a token — called when FCM reports it as unregistered/stale.
func (r *Repository) Delete(ctx context.Context, fcmToken string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM devices WHERE fcm_token = $1`, fcmToken)
	return err
}

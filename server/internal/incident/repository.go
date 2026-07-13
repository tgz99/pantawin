// Package incident manages incident rows created on monitor state
// transitions (spec sections 3.2, 5).
package incident

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Incident struct {
	ID         int64
	MonitorID  int64
	StartedAt  time.Time
	ResolvedAt *time.Time
	Cause      string
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Open creates an incident for a monitor going DOWN. The partial unique
// index (one open incident per monitor) makes a duplicate open a no-op:
// ON CONFLICT DO NOTHING returns the existing open incident instead of
// erroring, so a redelivered transition can't create two incidents.
func (r *Repository) Open(ctx context.Context, monitorID int64, cause string) (Incident, error) {
	var inc Incident
	err := r.pool.QueryRow(ctx, `
		INSERT INTO incidents (monitor_id, cause)
		VALUES ($1, $2)
		ON CONFLICT (monitor_id) WHERE resolved_at IS NULL DO NOTHING
		RETURNING id, monitor_id, started_at, resolved_at, cause
	`, monitorID, cause).Scan(&inc.ID, &inc.MonitorID, &inc.StartedAt, &inc.ResolvedAt, &inc.Cause)

	if errors.Is(err, pgx.ErrNoRows) {
		// An open incident already exists — fetch and return it.
		return r.currentOpen(ctx, monitorID)
	}
	if err != nil {
		return Incident{}, fmt.Errorf("open incident: %w", err)
	}
	return inc, nil
}

// Resolve closes the open incident for a monitor going back UP and returns
// it. Returns ErrNoOpenIncident if there wasn't one (e.g. a monitor's very
// first check succeeds — PENDING->UP is not a recovery).
var ErrNoOpenIncident = errors.New("no open incident to resolve")

func (r *Repository) Resolve(ctx context.Context, monitorID int64) (Incident, error) {
	var inc Incident
	err := r.pool.QueryRow(ctx, `
		UPDATE incidents SET resolved_at = now()
		WHERE monitor_id = $1 AND resolved_at IS NULL
		RETURNING id, monitor_id, started_at, resolved_at, cause
	`, monitorID).Scan(&inc.ID, &inc.MonitorID, &inc.StartedAt, &inc.ResolvedAt, &inc.Cause)
	if errors.Is(err, pgx.ErrNoRows) {
		return Incident{}, ErrNoOpenIncident
	}
	if err != nil {
		return Incident{}, fmt.Errorf("resolve incident: %w", err)
	}
	return inc, nil
}

func (r *Repository) currentOpen(ctx context.Context, monitorID int64) (Incident, error) {
	var inc Incident
	err := r.pool.QueryRow(ctx, `
		SELECT id, monitor_id, started_at, resolved_at, cause
		FROM incidents WHERE monitor_id = $1 AND resolved_at IS NULL
	`, monitorID).Scan(&inc.ID, &inc.MonitorID, &inc.StartedAt, &inc.ResolvedAt, &inc.Cause)
	if err != nil {
		return Incident{}, fmt.Errorf("fetch open incident: %w", err)
	}
	return inc, nil
}

// ListForMonitor returns incidents newest-first (M2 incident history).
func (r *Repository) ListForMonitor(ctx context.Context, monitorID int64, limit int) ([]Incident, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, monitor_id, started_at, resolved_at, cause
		FROM incidents WHERE monitor_id = $1
		ORDER BY started_at DESC LIMIT $2
	`, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	var out []Incident
	for rows.Next() {
		var inc Incident
		if err := rows.Scan(&inc.ID, &inc.MonitorID, &inc.StartedAt, &inc.ResolvedAt, &inc.Cause); err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

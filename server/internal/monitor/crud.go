package monitor

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound      = errors.New("monitor not found")
	ErrQuotaExceeded = errors.New("monitor quota exceeded")
)

// MaxMonitorsPerUser protects the shared VPS from unbounded check load
// (spec section 8).
const MaxMonitorsPerUser = 50

const monitorColumns = `id, user_id, name, url, method, interval_seconds, timeout_ms,
	expected_status_min, expected_status_max, failure_threshold,
	status, consecutive_failures, created_at, alert_channels, scope, team_id`

// scanFields returns scan destinations in monitorColumns order — every query
// that selects monitorColumns scans through this so the two can't drift.
func (m *Monitor) scanFields() []any {
	return []any{
		&m.ID, &m.UserID, &m.Name, &m.URL, &m.Method, &m.IntervalSeconds, &m.TimeoutMS,
		&m.ExpectedStatusMin, &m.ExpectedStatusMax, &m.FailureThreshold,
		&m.Status, &m.ConsecutiveFailures, &m.CreatedAt, &m.AlertChannels, &m.Scope, &m.TeamID,
	}
}

// accessPredicate is the authorization rule used by every per-monitor query:
// you can see/manage a monitor if you own it, or it belongs to a team you're
// a member of (M6.3 — team membership is now per-team, not implicit for all
// registered accounts). Team monitors are deliberately editable by any
// member, not just the creator — teams are small and trusted, and orphaned
// monitors after an owner leaves would be worse.
const accessPredicate = `(user_id = $2 OR (scope = 'team' AND team_id IN (
	SELECT team_id FROM team_members WHERE user_id = $2
)))`

// CreateParams carries validated input — URL format and SSRF validation
// happen in the HTTP layer before this is called.
type CreateParams struct {
	UserID            int64
	Name              string
	URL               string
	Method            string
	IntervalSeconds   int
	TimeoutMS         int
	ExpectedStatusMin int
	ExpectedStatusMax int
	FailureThreshold  int
	AlertChannels     []string // e.g. ["email"], ["push"], ["email","push"]
	Scope             string   // ScopePersonal (default) or ScopeTeam
	TeamID            *int64   // required iff Scope == ScopeTeam; caller validates membership
}

func (r *Repository) Create(ctx context.Context, p CreateParams) (Monitor, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Monitor{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialize concurrent creates per user by locking the user row, then
	// count inside the same transaction — two simultaneous creates can't
	// both slip under the quota.
	var lockedUserID int64
	if err := tx.QueryRow(ctx,
		`SELECT id FROM users WHERE id = $1 FOR UPDATE`, p.UserID,
	).Scan(&lockedUserID); err != nil {
		return Monitor{}, fmt.Errorf("lock user row: %w", err)
	}
	var count int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM monitors WHERE user_id = $1`, p.UserID,
	).Scan(&count); err != nil {
		return Monitor{}, fmt.Errorf("count monitors: %w", err)
	}
	if count >= MaxMonitorsPerUser {
		return Monitor{}, ErrQuotaExceeded
	}

	alertChannels := p.AlertChannels
	if len(alertChannels) == 0 {
		alertChannels = []string{"email"}
	}
	scope := p.Scope
	if scope == "" {
		scope = ScopePersonal
	}

	var m Monitor
	if err := tx.QueryRow(ctx, `
		INSERT INTO monitors (user_id, name, url, method, interval_seconds, timeout_ms,
		                      expected_status_min, expected_status_max, failure_threshold,
		                      alert_channels, scope, team_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING `+monitorColumns,
		p.UserID, p.Name, p.URL, p.Method, p.IntervalSeconds, p.TimeoutMS,
		p.ExpectedStatusMin, p.ExpectedStatusMax, p.FailureThreshold, alertChannels, scope, p.TeamID,
	).Scan(m.scanFields()...); err != nil {
		return Monitor{}, fmt.Errorf("insert monitor: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Monitor{}, err
	}
	return m, nil
}

// GetForUser fetches a monitor the user may access (their own, or any
// team-scoped one) — authorization is enforced at the query level, not by
// comparing after the fact.
func (r *Repository) GetForUser(ctx context.Context, userID, monitorID int64) (Monitor, error) {
	var m Monitor
	err := r.pool.QueryRow(ctx,
		`SELECT `+monitorColumns+` FROM monitors WHERE id = $1 AND `+accessPredicate,
		monitorID, userID,
	).Scan(m.scanFields()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, fmt.Errorf("get monitor: %w", err)
	}
	return m, nil
}

// UpdateParams uses pointers for PATCH semantics: nil = leave unchanged.
type UpdateParams struct {
	Name              *string
	URL               *string
	Method            *string
	IntervalSeconds   *int
	TimeoutMS         *int
	ExpectedStatusMin *int
	ExpectedStatusMax *int
	FailureThreshold  *int
	AlertChannels     *[]string // nil = unchanged
	Scope             *string   // nil = unchanged
	// TeamID/ScopeSet apply together: ScopeSet distinguishes "Scope wasn't
	// touched" from "team_id should be written as-is (including to NULL)".
	// The httpapi layer only ever sets ScopeSet when Scope is also being
	// changed, so team_id and scope can't drift out of sync with the
	// monitors_team_scope_check constraint.
	TeamID   *int64
	ScopeSet bool
}

func (r *Repository) Update(ctx context.Context, userID, monitorID int64, p UpdateParams) (Monitor, error) {
	var m Monitor
	err := r.pool.QueryRow(ctx, `
		UPDATE monitors SET
			name                = COALESCE($3, name),
			url                 = COALESCE($4, url),
			method              = COALESCE($5, method),
			interval_seconds    = COALESCE($6, interval_seconds),
			timeout_ms          = COALESCE($7, timeout_ms),
			expected_status_min = COALESCE($8, expected_status_min),
			expected_status_max = COALESCE($9, expected_status_max),
			failure_threshold   = COALESCE($10, failure_threshold),
			alert_channels      = COALESCE($11, alert_channels),
			scope               = COALESCE($12, scope),
			team_id             = CASE WHEN $13 THEN $14 ELSE team_id END
		WHERE id = $1 AND `+accessPredicate+`
		RETURNING `+monitorColumns,
		monitorID, userID,
		p.Name, p.URL, p.Method, p.IntervalSeconds, p.TimeoutMS,
		p.ExpectedStatusMin, p.ExpectedStatusMax, p.FailureThreshold, p.AlertChannels,
		p.Scope, p.ScopeSet, p.TeamID,
	).Scan(m.scanFields()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, fmt.Errorf("update monitor: %w", err)
	}
	return m, nil
}

func (r *Repository) Delete(ctx context.Context, userID, monitorID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM monitors WHERE id = $1 AND `+accessPredicate, monitorID, userID)
	if err != nil {
		return fmt.Errorf("delete monitor: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPaused pauses or resumes. Resume moves PAUSED -> PENDING (not back to
// the prior status) to force a fresh confirmation cycle rather than
// trusting state from before the pause.
func (r *Repository) SetPaused(ctx context.Context, userID, monitorID int64, paused bool) (Monitor, error) {
	newStatus := StatusPending
	if paused {
		newStatus = StatusPaused
	}
	var m Monitor
	err := r.pool.QueryRow(ctx, `
		UPDATE monitors SET status = $3, consecutive_failures = 0
		WHERE id = $1 AND `+accessPredicate+`
		RETURNING `+monitorColumns,
		monitorID, userID, newStatus,
	).Scan(m.scanFields()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Monitor{}, ErrNotFound
	}
	if err != nil {
		return Monitor{}, fmt.Errorf("set paused: %w", err)
	}
	return m, nil
}

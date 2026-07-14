package monitor

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tgz99/pantawin/server/internal/checker"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// SeedMonitor idempotently ensures a monitor with the given URL exists for
// the given owner, so re-deploys don't create duplicate seed rows. There's
// no unique constraint on (user_id, url) to lean on for an ON CONFLICT
// clause, so this does a plain find-or-create instead.
func (r *Repository) SeedMonitor(ctx context.Context, userID int64, name, url string) (Monitor, error) {
	var m Monitor

	existing, findErr := r.findByURL(ctx, userID, url)
	if findErr == nil {
		return existing, nil
	}

	if err := r.pool.QueryRow(ctx, `
		INSERT INTO monitors (user_id, name, url)
		VALUES ($1, $2, $3)
		RETURNING `+monitorColumns+`
	`, userID, name, url).Scan(m.scanFields()...); err != nil {
		return Monitor{}, fmt.Errorf("insert seed monitor: %w", err)
	}
	return m, nil
}

func (r *Repository) findByURL(ctx context.Context, userID int64, url string) (Monitor, error) {
	var m Monitor
	err := r.pool.QueryRow(ctx, `
		SELECT `+monitorColumns+`
		FROM monitors WHERE user_id = $1 AND url = $2
		LIMIT 1
	`, userID, url).Scan(m.scanFields()...)
	if err != nil {
		return Monitor{}, err
	}
	return m, nil
}

// ListForUser returns every monitor the user can access: their personal
// monitors plus every team-scoped monitor belonging to a team they're a
// member of (M6.3).
func (r *Repository) ListForUser(ctx context.Context, userID int64) ([]Monitor, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+monitorColumns+`
		FROM monitors
		WHERE user_id = $1
		   OR (scope = 'team' AND team_id IN (SELECT team_id FROM team_members WHERE user_id = $1))
		ORDER BY id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query monitors: %w", err)
	}
	defer rows.Close()

	var monitors []Monitor
	for rows.Next() {
		var m Monitor
		if err := rows.Scan(m.scanFields()...); err != nil {
			return nil, fmt.Errorf("scan monitor row: %w", err)
		}
		monitors = append(monitors, m)
	}
	return monitors, rows.Err()
}

// AlertConfig resolves how a monitor should be alerted: its enabled channels
// and the destination emails. A per-monitor override wins outright;
// otherwise personal monitors alert the owner and team monitors alert every
// member of that specific team (M6.3). Used by the notification dispatcher.
func (r *Repository) AlertConfig(ctx context.Context, monitorID int64) (channels []string, emails []string, err error) {
	var override *string
	var ownerEmail, scope string
	var teamID *int64
	err = r.pool.QueryRow(ctx, `
		SELECT m.alert_channels, m.alert_email, m.scope, m.team_id, u.email
		FROM monitors m JOIN users u ON u.id = m.user_id
		WHERE m.id = $1
	`, monitorID).Scan(&channels, &override, &scope, &teamID, &ownerEmail)
	if err != nil {
		return nil, nil, fmt.Errorf("load alert config: %w", err)
	}
	if override != nil && *override != "" {
		return channels, []string{*override}, nil
	}
	if scope != ScopeTeam || teamID == nil {
		return channels, []string{ownerEmail}, nil
	}

	rows, err := r.pool.Query(ctx, `
		SELECT u.email FROM team_members tm JOIN users u ON u.id = tm.user_id
		WHERE tm.team_id = $1 ORDER BY u.id
	`, *teamID)
	if err != nil {
		return nil, nil, fmt.Errorf("load team emails: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, nil, fmt.Errorf("scan team email: %w", err)
		}
		emails = append(emails, e)
	}
	return channels, emails, rows.Err()
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Monitor, error) {
	var m Monitor
	err := r.pool.QueryRow(ctx, `
		SELECT `+monitorColumns+`
		FROM monitors WHERE id = $1
	`, id).Scan(m.scanFields()...)
	if err != nil {
		return Monitor{}, fmt.Errorf("get monitor by id: %w", err)
	}
	return m, nil
}

func (r *Repository) ListAll(ctx context.Context) ([]Monitor, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+monitorColumns+`
		FROM monitors
		WHERE status != 'PAUSED'
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("query all monitors: %w", err)
	}
	defer rows.Close()

	var monitors []Monitor
	for rows.Next() {
		var m Monitor
		if err := rows.Scan(m.scanFields()...); err != nil {
			return nil, fmt.Errorf("scan monitor row: %w", err)
		}
		monitors = append(monitors, m)
	}
	return monitors, rows.Err()
}

// CheckOutcome reports what a recorded check did to the monitor's state —
// M2's incident/notification pipeline keys off Transitioned.
type CheckOutcome struct {
	Transitioned bool
	From, To     Status
}

// RecordCheck writes the raw result and advances the failure-threshold
// state machine (spec 3.2) in one transaction. The monitor row is locked
// (FOR UPDATE) so concurrent checks of the same monitor can't interleave
// their read-modify-write of the failure counter.
func (r *Repository) RecordCheck(ctx context.Context, monitorID int64, result checker.Result) (CheckOutcome, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return CheckOutcome{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var current Status
	var consecutiveFailures, failureThreshold int
	if err := tx.QueryRow(ctx, `
		SELECT status, consecutive_failures, failure_threshold
		FROM monitors WHERE id = $1
		FOR UPDATE
	`, monitorID).Scan(&current, &consecutiveFailures, &failureThreshold); err != nil {
		return CheckOutcome{}, fmt.Errorf("lock monitor row: %w", err)
	}

	var httpCode, responseTimeMS *int
	if result.HTTPCode != 0 {
		v := result.HTTPCode
		httpCode = &v
	}
	if result.ResponseTimeMS != 0 {
		v := int(result.ResponseTimeMS)
		responseTimeMS = &v
	}
	var errorType *string
	if result.ErrorType != "" {
		errorType = &result.ErrorType
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO check_results (monitor_id, ok, http_code, response_time_ms, error_type)
		VALUES ($1, $2, $3, $4, $5)
	`, monitorID, result.OK, httpCode, responseTimeMS, errorType); err != nil {
		return CheckOutcome{}, fmt.Errorf("insert check_result: %w", err)
	}

	next, transitioned := Apply(current, consecutiveFailures, result.OK, failureThreshold)
	if _, err := tx.Exec(ctx, `
		UPDATE monitors SET status = $1, consecutive_failures = $2 WHERE id = $3
	`, next.Status, next.ConsecutiveFailures, monitorID); err != nil {
		return CheckOutcome{}, fmt.Errorf("update monitor status: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return CheckOutcome{}, err
	}
	return CheckOutcome{Transitioned: transitioned, From: current, To: next.Status}, nil
}

// StatusViews returns the API-facing view for each monitor the user can
// access (personal, plus team-scoped monitors of teams they belong to,
// M6.3), including the most recent check result and — for team monitors —
// the team's name, so a member of several teams can tell them apart.
func (r *Repository) StatusViews(ctx context.Context, userID int64) ([]StatusView, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.name, m.url, m.status, m.scope, m.team_id, t.name,
		       lc.checked_at, lc.response_time_ms
		FROM monitors m
		LEFT JOIN teams t ON t.id = m.team_id
		LEFT JOIN LATERAL (
			SELECT checked_at, response_time_ms
			FROM check_results cr
			WHERE cr.monitor_id = m.id
			ORDER BY cr.checked_at DESC
			LIMIT 1
		) lc ON true
		WHERE m.user_id = $1
		   OR (m.scope = 'team' AND m.team_id IN (SELECT team_id FROM team_members WHERE user_id = $1))
		ORDER BY m.id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query status views: %w", err)
	}
	defer rows.Close()

	var views []StatusView
	for rows.Next() {
		var v StatusView
		if err := rows.Scan(
			&v.ID, &v.Name, &v.URL, &v.Status, &v.Scope, &v.TeamID, &v.TeamName,
			&v.LastCheckedAt, &v.ResponseTimeMS,
		); err != nil {
			return nil, fmt.Errorf("scan status view: %w", err)
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

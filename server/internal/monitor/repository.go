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
		RETURNING id, user_id, name, url, method, interval_seconds, timeout_ms,
		          expected_status_min, expected_status_max, failure_threshold,
		          status, consecutive_failures, created_at
	`, userID, name, url).Scan(
		&m.ID, &m.UserID, &m.Name, &m.URL, &m.Method, &m.IntervalSeconds, &m.TimeoutMS,
		&m.ExpectedStatusMin, &m.ExpectedStatusMax, &m.FailureThreshold,
		&m.Status, &m.ConsecutiveFailures, &m.CreatedAt,
	); err != nil {
		return Monitor{}, fmt.Errorf("insert seed monitor: %w", err)
	}
	return m, nil
}

func (r *Repository) findByURL(ctx context.Context, userID int64, url string) (Monitor, error) {
	var m Monitor
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, name, url, method, interval_seconds, timeout_ms,
		       expected_status_min, expected_status_max, failure_threshold,
		       status, consecutive_failures, created_at
		FROM monitors WHERE user_id = $1 AND url = $2
		LIMIT 1
	`, userID, url).Scan(
		&m.ID, &m.UserID, &m.Name, &m.URL, &m.Method, &m.IntervalSeconds, &m.TimeoutMS,
		&m.ExpectedStatusMin, &m.ExpectedStatusMax, &m.FailureThreshold,
		&m.Status, &m.ConsecutiveFailures, &m.CreatedAt,
	)
	if err != nil {
		return Monitor{}, err
	}
	return m, nil
}

func (r *Repository) ListForUser(ctx context.Context, userID int64) ([]Monitor, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, name, url, method, interval_seconds, timeout_ms,
		       expected_status_min, expected_status_max, failure_threshold,
		       status, consecutive_failures, created_at
		FROM monitors WHERE user_id = $1
		ORDER BY id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query monitors: %w", err)
	}
	defer rows.Close()

	var monitors []Monitor
	for rows.Next() {
		var m Monitor
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.Name, &m.URL, &m.Method, &m.IntervalSeconds, &m.TimeoutMS,
			&m.ExpectedStatusMin, &m.ExpectedStatusMax, &m.FailureThreshold,
			&m.Status, &m.ConsecutiveFailures, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan monitor row: %w", err)
		}
		monitors = append(monitors, m)
	}
	return monitors, rows.Err()
}

func (r *Repository) GetByID(ctx context.Context, id int64) (Monitor, error) {
	var m Monitor
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, name, url, method, interval_seconds, timeout_ms,
		       expected_status_min, expected_status_max, failure_threshold,
		       status, consecutive_failures, created_at
		FROM monitors WHERE id = $1
	`, id).Scan(
		&m.ID, &m.UserID, &m.Name, &m.URL, &m.Method, &m.IntervalSeconds, &m.TimeoutMS,
		&m.ExpectedStatusMin, &m.ExpectedStatusMax, &m.FailureThreshold,
		&m.Status, &m.ConsecutiveFailures, &m.CreatedAt,
	)
	if err != nil {
		return Monitor{}, fmt.Errorf("get monitor by id: %w", err)
	}
	return m, nil
}

func (r *Repository) ListAll(ctx context.Context) ([]Monitor, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, name, url, method, interval_seconds, timeout_ms,
		       expected_status_min, expected_status_max, failure_threshold,
		       status, consecutive_failures, created_at
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
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.Name, &m.URL, &m.Method, &m.IntervalSeconds, &m.TimeoutMS,
			&m.ExpectedStatusMin, &m.ExpectedStatusMax, &m.FailureThreshold,
			&m.Status, &m.ConsecutiveFailures, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan monitor row: %w", err)
		}
		monitors = append(monitors, m)
	}
	return monitors, rows.Err()
}

// RecordCheck writes the raw result and applies the M0 trivial status rule
// (ok -> UP, !ok -> DOWN). Replaced by the failure_threshold state machine
// in M1.
func (r *Repository) RecordCheck(ctx context.Context, monitorID int64, result checker.Result) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

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
		return fmt.Errorf("insert check_result: %w", err)
	}

	newStatus := StatusDown
	if result.OK {
		newStatus = StatusUp
	}
	if _, err := tx.Exec(ctx, `
		UPDATE monitors SET status = $1 WHERE id = $2
	`, newStatus, monitorID); err != nil {
		return fmt.Errorf("update monitor status: %w", err)
	}

	return tx.Commit(ctx)
}

// StatusViews returns the API-facing view for each of the user's monitors,
// including the most recent check result if one exists.
func (r *Repository) StatusViews(ctx context.Context, userID int64) ([]StatusView, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.name, m.url, m.status, lc.checked_at, lc.response_time_ms
		FROM monitors m
		LEFT JOIN LATERAL (
			SELECT checked_at, response_time_ms
			FROM check_results cr
			WHERE cr.monitor_id = m.id
			ORDER BY cr.checked_at DESC
			LIMIT 1
		) lc ON true
		WHERE m.user_id = $1
		ORDER BY m.id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query status views: %w", err)
	}
	defer rows.Close()

	var views []StatusView
	for rows.Next() {
		var v StatusView
		if err := rows.Scan(&v.ID, &v.Name, &v.URL, &v.Status, &v.LastCheckedAt, &v.ResponseTimeMS); err != nil {
			return nil, fmt.Errorf("scan status view: %w", err)
		}
		views = append(views, v)
	}
	return views, rows.Err()
}

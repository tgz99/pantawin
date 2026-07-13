package analytics

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Rollup writes the stats_hourly / stats_daily tables from check_results and
// incidents. Every write is an idempotent upsert over a trailing window, so
// the job self-heals after downtime and keeps the current (partial) hour and
// day fresh — no "last processed" bookkeeping to corrupt.
type Rollup struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewRollup(pool *pgxpool.Pool, logger *slog.Logger) *Rollup {
	return &Rollup{pool: pool, logger: logger}
}

const (
	rollupInterval   = 5 * time.Minute
	hourlyLookback   = 26 * time.Hour      // steady-state: current day + slack
	dailyLookback    = 48 * time.Hour      // steady-state: yesterday + today
	backfillLookback = 14 * 24 * time.Hour // first run: cover existing history
)

// Run backfills history once, then rolls the trailing window on a ticker
// until ctx is cancelled.
func (r *Rollup) Run(ctx context.Context) {
	// First pass reaches back far enough to roll up whatever check history
	// already exists (idempotent, so re-running on every boot is fine).
	r.tick(ctx, backfillLookback, backfillLookback)
	t := time.NewTicker(rollupInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.tick(ctx, hourlyLookback, dailyLookback)
		}
	}
}

func (r *Rollup) tick(ctx context.Context, hourly, daily time.Duration) {
	now := time.Now().UTC()
	if err := r.RollupHours(ctx, now.Add(-hourly), now); err != nil {
		r.logger.Error("analytics: hourly rollup failed", "error", err)
	}
	if err := r.RollupDays(ctx, now.Add(-daily), now); err != nil {
		r.logger.Error("analytics: daily rollup failed", "error", err)
	}
}

// RollupHours upserts stats_hourly for every UTC hour bucket that overlaps
// [from, to]. Hours with zero checks produce no row — absence means "no
// monitoring activity" (paused or not yet created).
func (r *Rollup) RollupHours(ctx context.Context, from, to time.Time) error {
	now := time.Now().UTC()
	for h := HourStart(from); !h.After(HourStart(to)); h = h.Add(time.Hour) {
		if err := r.rollupOneHour(ctx, Window{Start: h, End: h.Add(time.Hour)}, now); err != nil {
			return err
		}
	}
	return nil
}

type aggRow struct {
	monitorID int64
	checks    int
	fails     int
	avgMS     *float64
	p95MS     *float64
}

func (r *Rollup) aggregate(ctx context.Context, w Window) ([]aggRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT monitor_id, count(*),
		       count(*) FILTER (WHERE NOT ok),
		       avg(response_time_ms) FILTER (WHERE ok),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY response_time_ms) FILTER (WHERE ok)
		FROM check_results
		WHERE checked_at >= $1 AND checked_at < $2
		GROUP BY monitor_id
	`, w.Start, w.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []aggRow
	for rows.Next() {
		var a aggRow
		if err := rows.Scan(&a.monitorID, &a.checks, &a.fails, &a.avgMS, &a.p95MS); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// incidentSpans returns, per monitor, the incident spans overlapping w.
func (r *Rollup) incidentSpans(ctx context.Context, w Window) (map[int64][]Span, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT monitor_id, started_at, resolved_at FROM incidents
		WHERE started_at < $2 AND (resolved_at IS NULL OR resolved_at > $1)
	`, w.Start, w.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	spans := make(map[int64][]Span)
	for rows.Next() {
		var id int64
		var s Span
		if err := rows.Scan(&id, &s.Start, &s.End); err != nil {
			return nil, err
		}
		spans[id] = append(spans[id], s)
	}
	return spans, rows.Err()
}

func (r *Rollup) rollupOneHour(ctx context.Context, w Window, now time.Time) error {
	aggs, err := r.aggregate(ctx, w)
	if err != nil {
		return err
	}
	if len(aggs) == 0 {
		return nil
	}
	spans, err := r.incidentSpans(ctx, w)
	if err != nil {
		return err
	}
	for _, a := range aggs {
		// Monitored time: the active part of the hour, clipped at `now`
		// for the in-progress hour.
		monEnd := w.End
		if monEnd.After(now) {
			monEnd = now
		}
		monitored := monEnd.Sub(w.Start).Seconds()
		down := DownSeconds(Window{Start: w.Start, End: monEnd}, spans[a.monitorID], now)
		up := UptimePct(monitored, down)
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO stats_hourly (monitor_id, hour_ts, checks, fails, up_pct, avg_ms, p95_ms)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (monitor_id, hour_ts) DO UPDATE SET
				checks = EXCLUDED.checks, fails = EXCLUDED.fails,
				up_pct = EXCLUDED.up_pct, avg_ms = EXCLUDED.avg_ms, p95_ms = EXCLUDED.p95_ms
		`, a.monitorID, w.Start, a.checks, a.fails, up, a.avgMS, a.p95MS); err != nil {
			return err
		}
	}
	return nil
}

// RollupDays upserts stats_daily for every UTC day bucket overlapping
// [from, to]. Daily uptime excludes inactive (paused) hours: monitored time
// is 3600s per hour that has recorded checks, and downtime is clipped to
// the day.
func (r *Rollup) RollupDays(ctx context.Context, from, to time.Time) error {
	now := time.Now().UTC()
	for d := DayStart(from); !d.After(DayStart(to)); d = d.Add(24 * time.Hour) {
		if err := r.rollupOneDay(ctx, Window{Start: d, End: d.Add(24 * time.Hour)}, now); err != nil {
			return err
		}
	}
	return nil
}

func (r *Rollup) rollupOneDay(ctx context.Context, w Window, now time.Time) error {
	aggs, err := r.aggregate(ctx, w)
	if err != nil {
		return err
	}
	if len(aggs) == 0 {
		return nil
	}
	spans, err := r.incidentSpans(ctx, w)
	if err != nil {
		return err
	}
	for _, a := range aggs {
		monitored, err := r.activeSeconds(ctx, a.monitorID, w, now)
		if err != nil {
			return err
		}
		clipEnd := w.End
		if clipEnd.After(now) {
			clipEnd = now
		}
		down := DownSeconds(Window{Start: w.Start, End: clipEnd}, spans[a.monitorID], now)
		up := UptimePct(monitored, down)
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO stats_daily (monitor_id, day, checks, fails, up_pct, avg_ms, p95_ms, downtime_s)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (monitor_id, day) DO UPDATE SET
				checks = EXCLUDED.checks, fails = EXCLUDED.fails, up_pct = EXCLUDED.up_pct,
				avg_ms = EXCLUDED.avg_ms, p95_ms = EXCLUDED.p95_ms, downtime_s = EXCLUDED.downtime_s
		`, a.monitorID, w.Start, a.checks, a.fails, up, a.avgMS, a.p95MS, int(down)); err != nil {
			return err
		}
	}
	return nil
}

// activeSeconds approximates the monitored time within w as one full hour
// per stats_hourly bucket with recorded checks (the in-progress hour counts
// partially, clipped at now).
func (r *Rollup) activeSeconds(ctx context.Context, monitorID int64, w Window, now time.Time) (float64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT hour_ts FROM stats_hourly
		WHERE monitor_id = $1 AND hour_ts >= $2 AND hour_ts < $3 AND checks > 0
	`, monitorID, w.Start, w.End)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total float64
	for rows.Next() {
		var h time.Time
		if err := rows.Scan(&h); err != nil {
			return 0, err
		}
		end := h.Add(time.Hour)
		if end.After(now) {
			end = now
		}
		if end.After(h) {
			total += end.Sub(h).Seconds()
		}
	}
	return total, rows.Err()
}

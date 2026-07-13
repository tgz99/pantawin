package analytics

import (
	"context"
	"time"
)

// Bucket is one chart point: an hour (period=day) or a day (period=week).
type Bucket struct {
	Ts     time.Time `json:"ts"`
	Checks int       `json:"checks"`
	Fails  int       `json:"fails"`
	UpPct  *float64  `json:"up_pct"`
	AvgMS  *float64  `json:"avg_ms"`
	P95MS  *float64  `json:"p95_ms"`
}

// StatsResult is the payload of GET /monitors/{id}/stats.
type StatsResult struct {
	Period    string    `json:"period"`
	From      time.Time `json:"from"`
	To        time.Time `json:"to"`
	Checks    int       `json:"checks"`
	Fails     int       `json:"fails"`
	UpPct     *float64  `json:"uptime_pct"`
	AvgMS     *float64  `json:"avg_ms"`
	P95MS     *float64  `json:"p95_ms"`
	DowntimeS int       `json:"downtime_s"`
	Buckets   []Bucket  `json:"buckets"`
}

const (
	PeriodDay  = "day"
	PeriodWeek = "week"
)

// Stats assembles the analytics for one monitor. period=day returns the 24
// hourly buckets of the anchor's UTC day; period=week returns 7 daily
// buckets ending on the anchor's UTC day. The overall row is computed from
// the raw tables over the whole window (not by summing buckets) — the
// consistency suite asserts both routes agree.
func (r *Rollup) Stats(ctx context.Context, monitorID int64, period string, anchor time.Time) (StatsResult, error) {
	now := time.Now().UTC()
	var w Window
	switch period {
	case PeriodWeek:
		dayEnd := DayStart(anchor).Add(24 * time.Hour)
		w = Window{Start: dayEnd.Add(-7 * 24 * time.Hour), End: dayEnd}
	default:
		period = PeriodDay
		w = Window{Start: DayStart(anchor), End: DayStart(anchor).Add(24 * time.Hour)}
	}

	res := StatsResult{Period: period, From: w.Start, To: w.End}

	// Buckets from the rollup tables.
	var err error
	if period == PeriodDay {
		res.Buckets, err = r.hourlyBuckets(ctx, monitorID, w)
	} else {
		res.Buckets, err = r.dailyBuckets(ctx, monitorID, w)
	}
	if err != nil {
		return StatsResult{}, err
	}

	// Overall aggregates straight from check_results.
	err = r.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE NOT ok),
		       avg(response_time_ms) FILTER (WHERE ok),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY response_time_ms) FILTER (WHERE ok)
		FROM check_results
		WHERE monitor_id = $1 AND checked_at >= $2 AND checked_at < $3
	`, monitorID, w.Start, w.End).Scan(&res.Checks, &res.Fails, &res.AvgMS, &res.P95MS)
	if err != nil {
		return StatsResult{}, err
	}

	// Overall uptime from incident durations over the monitored time.
	spans, err := r.incidentSpans(ctx, w)
	if err != nil {
		return StatsResult{}, err
	}
	monitored, err := r.activeSeconds(ctx, monitorID, w, now)
	if err != nil {
		return StatsResult{}, err
	}
	clipEnd := w.End
	if clipEnd.After(now) {
		clipEnd = now
	}
	down := DownSeconds(Window{Start: w.Start, End: clipEnd}, spans[monitorID], now)
	res.DowntimeS = int(down)
	res.UpPct = UptimePct(monitored, down)
	return res, nil
}

func (r *Rollup) hourlyBuckets(ctx context.Context, monitorID int64, w Window) ([]Bucket, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT hour_ts, checks, fails, up_pct, avg_ms, p95_ms FROM stats_hourly
		WHERE monitor_id = $1 AND hour_ts >= $2 AND hour_ts < $3
		ORDER BY hour_ts
	`, monitorID, w.Start, w.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBuckets(rows)
}

func (r *Rollup) dailyBuckets(ctx context.Context, monitorID int64, w Window) ([]Bucket, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT day, checks, fails, up_pct, avg_ms, p95_ms FROM stats_daily
		WHERE monitor_id = $1 AND day >= $2 AND day < $3
		ORDER BY day
	`, monitorID, w.Start, w.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBuckets(rows)
}

type pgxRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanBuckets(rows pgxRows) ([]Bucket, error) {
	buckets := []Bucket{}
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Ts, &b.Checks, &b.Fails, &b.UpPct, &b.AvgMS, &b.P95MS); err != nil {
			return nil, err
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

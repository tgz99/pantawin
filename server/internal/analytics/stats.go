package analytics

import (
	"context"
	"time"
)

// Bucket is one chart point: an hour (period=day), a local day (week/month)
// or a local month (year). A bucket with zero checks has nil UpPct/AvgMS —
// "no monitoring activity", deliberately distinct from 0%.
type Bucket struct {
	Ts     time.Time `json:"ts"`
	Checks int       `json:"checks"`
	Fails  int       `json:"fails"`
	UpPct  *float64  `json:"up_pct"`
	AvgMS  *float64  `json:"avg_ms"`
	P95MS  *float64  `json:"p95_ms"`
	DownS  int       `json:"down_s"`
}

// StatsResult is the payload of GET /monitors/{id}/stats.
type StatsResult struct {
	Period    string    `json:"period"`
	Tz        string    `json:"tz"`
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

// Stats assembles the analytics for one monitor over the period anchored at
// `anchor`, bucketed in `loc` (spec M5: windows follow the viewer's calendar
// — WIB — over UTC storage). Buckets tile the window exactly and every
// bucket window is emitted (empty ones with nil UpPct/AvgMS), so the chart's
// x-axis is uniform. The overall row and the week/month/year buckets are all
// computed from the raw tables — the same math over a partition of the same
// window — which is what makes the sum-of-parts == whole suite hold.
func (r *Rollup) Stats(ctx context.Context, monitorID int64, period string, anchor time.Time, loc *time.Location) (StatsResult, error) {
	now := time.Now().UTC()
	w, bucketWins, err := PeriodWindow(period, anchor, loc)
	if err != nil {
		return StatsResult{}, err
	}

	res := StatsResult{Period: period, Tz: loc.String(), From: w.Start.UTC(), To: w.End.UTC()}

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

	spans, err := r.incidentSpans(ctx, w)
	if err != nil {
		return StatsResult{}, err
	}
	// Monitored time comes from the UTC hours that actually recorded checks
	// (derived from check_results, not stats_hourly, so windows older than
	// the rollup backfill still compute correct uptime).
	hours, err := r.checkedHours(ctx, monitorID, w)
	if err != nil {
		return StatsResult{}, err
	}

	monitored := hoursOverlap(hours, w, now)
	down := DownSeconds(clipAtNow(w, now), spans[monitorID], now)
	res.DowntimeS = int(down)
	res.UpPct = UptimePct(monitored, down)

	if period == PeriodDay {
		res.Buckets, err = r.hourlyBuckets(ctx, monitorID, bucketWins, spans[monitorID], now)
	} else {
		res.Buckets, err = r.rawBuckets(ctx, monitorID, w, bucketWins, loc, spans[monitorID], hours, now)
	}
	if err != nil {
		return StatsResult{}, err
	}
	return res, nil
}

// hourlyBuckets maps stats_hourly rows onto the 24 hour windows of the local
// day (kept from M4: one indexed read, and the rollup keeps the current hour
// at most one tick stale). Downtime is filled from incident spans exactly.
func (r *Rollup) hourlyBuckets(ctx context.Context, monitorID int64, wins []Window, spans []Span, now time.Time) ([]Bucket, error) {
	if len(wins) == 0 {
		return []Bucket{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT hour_ts, checks, fails, up_pct, avg_ms, p95_ms FROM stats_hourly
		WHERE monitor_id = $1 AND hour_ts >= $2 AND hour_ts < $3
	`, monitorID, wins[0].Start, wins[len(wins)-1].End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byHour := map[int64]Bucket{}
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Ts, &b.Checks, &b.Fails, &b.UpPct, &b.AvgMS, &b.P95MS); err != nil {
			return nil, err
		}
		byHour[b.Ts.Unix()] = b
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	buckets := make([]Bucket, 0, len(wins))
	for _, win := range wins {
		b := byHour[win.Start.Unix()]
		b.Ts = win.Start.UTC()
		b.DownS = int(DownSeconds(clipAtNow(win, now), spans, now))
		buckets = append(buckets, b)
	}
	return buckets, nil
}

// rawBuckets computes week/month/year buckets from check_results + incident
// spans directly: one GROUP BY over the window for check aggregates (grouped
// by the local day or month via AT TIME ZONE), plus per-bucket uptime from
// the same checkedHours/spans the overall row uses.
func (r *Rollup) rawBuckets(
	ctx context.Context, monitorID int64, w Window, wins []Window,
	loc *time.Location, spans []Span, hours []hourActivity, now time.Time,
) ([]Bucket, error) {
	trunc := "day"
	if len(wins) > 0 && wins[0].End.Sub(wins[0].Start) > 26*time.Hour {
		trunc = "month"
	}

	// date_trunc in the local zone: timestamptz -> local wall time -> trunc
	// -> back to an absolute instant. DST-safe; keys match bucket starts.
	rows, err := r.pool.Query(ctx, `
		SELECT (date_trunc($4, checked_at AT TIME ZONE $5) AT TIME ZONE $5) AS bucket_ts,
		       count(*), count(*) FILTER (WHERE NOT ok),
		       avg(response_time_ms) FILTER (WHERE ok),
		       percentile_cont(0.95) WITHIN GROUP (ORDER BY response_time_ms) FILTER (WHERE ok)
		FROM check_results
		WHERE monitor_id = $1 AND checked_at >= $2 AND checked_at < $3
		GROUP BY 1
	`, monitorID, w.Start, w.End, trunc, loc.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type agg struct {
		checks, fails int
		avgMS, p95MS  *float64
	}
	byStart := map[int64]agg{}
	for rows.Next() {
		var ts time.Time
		var a agg
		if err := rows.Scan(&ts, &a.checks, &a.fails, &a.avgMS, &a.p95MS); err != nil {
			return nil, err
		}
		byStart[ts.Unix()] = a
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	buckets := make([]Bucket, 0, len(wins))
	for _, win := range wins {
		a := byStart[win.Start.Unix()]
		monitored := hoursOverlap(hours, win, now)
		downF := DownSeconds(clipAtNow(win, now), spans, now)
		b := Bucket{
			Ts: win.Start.UTC(), Checks: a.checks, Fails: a.fails,
			AvgMS: a.avgMS, P95MS: a.p95MS,
			UpPct: UptimePct(monitored, downF),
			DownS: int(downF),
		}
		buckets = append(buckets, b)
	}
	return buckets, nil
}

// hourActivity is a UTC hour that recorded at least one check.
type hourActivity struct{ start time.Time }

func (r *Rollup) checkedHours(ctx context.Context, monitorID int64, w Window) ([]hourActivity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT date_trunc('hour', checked_at) FROM check_results
		WHERE monitor_id = $1 AND checked_at >= $2 AND checked_at < $3
	`, monitorID, w.Start, w.End)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []hourActivity
	for rows.Next() {
		var h hourActivity
		if err := rows.Scan(&h.start); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// hoursOverlap sums the monitored seconds the given active hours contribute
// to window w: each hour counts its overlap with w, clipped at `now` so the
// in-progress hour counts partially.
func hoursOverlap(hours []hourActivity, w Window, now time.Time) float64 {
	end := w.End
	if end.After(now) {
		end = now
	}
	var total float64
	for _, h := range hours {
		hs, he := h.start, h.start.Add(time.Hour)
		if hs.Before(w.Start) {
			hs = w.Start
		}
		if he.After(end) {
			he = end
		}
		if he.After(hs) {
			total += he.Sub(hs).Seconds()
		}
	}
	return total
}

func clipAtNow(w Window, now time.Time) Window {
	if w.End.After(now) {
		return Window{Start: w.Start, End: now}
	}
	return w
}

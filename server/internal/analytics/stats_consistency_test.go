//go:build integration

package analytics_test

import (
	"context"
	"math"
	"testing"
	"time"
	_ "time/tzdata"

	"log/slog"

	"github.com/tgz99/pantawin/server/internal/analytics"
)

// TestStatsConsistency_AllPeriods is the M5 exit-criteria suite: for every
// period (day/week/month/year) computed in WIB, the buckets tile the window
// and their parts sum to the whole — checks, fails and downtime alike.
//
// Scenario (UTC storage, WIB rendering; dates safely in the past so the
// in-progress clipping at `now` never bites): checks every 60s over two WIB
// days 2026-06-10 and 2026-06-11 with a pause gap in between, plus two
// incidents — one crossing WIB midnight, one crossing UTC midnight. The
// splits land on different buckets in the two zones, which is what proves
// the bucketing is genuinely timezone-aware.
func TestStatsConsistency_AllPeriods(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	wib, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("load Asia/Jakarta: %v", err)
	}

	var userID, monitorID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('c@t.test','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO monitors (user_id, name, url) VALUES ($1,'m','https://example.com') RETURNING id`, userID).Scan(&monitorID); err != nil {
		t.Fatal(err)
	}

	// Two active stretches with a paused gap (times in WIB = UTC+7):
	//   stretch A: Jun 10 20:00 -> Jun 11 02:00  (13:00Z -> 19:00Z Jun 10)
	//   stretch B: Jun 11 06:00 -> Jun 11 09:00  (23:00Z Jun 10 -> 02:00Z Jun 11)
	aStart := time.Date(2026, 6, 10, 20, 0, 0, 0, wib)
	aEnd := time.Date(2026, 6, 11, 2, 0, 0, 0, wib)
	bStart := time.Date(2026, 6, 11, 6, 0, 0, 0, wib)
	bEnd := time.Date(2026, 6, 11, 9, 0, 0, 0, wib)

	// Incident 1 crosses WIB midnight: 23:30 -> 00:15 (45m, splits across
	// the two WIB days but stays inside UTC Jun 10).
	inc1Start := time.Date(2026, 6, 10, 23, 30, 0, 0, wib)
	inc1End := time.Date(2026, 6, 11, 0, 15, 0, 0, wib)
	// Incident 2 crosses UTC midnight: 06:30 -> 07:20 WIB = 23:30Z -> 00:20Z
	// (50m, single WIB day but splits across the two UTC days).
	inc2Start := time.Date(2026, 6, 11, 6, 30, 0, 0, wib)
	inc2End := time.Date(2026, 6, 11, 7, 20, 0, 0, wib)

	seed := func(from, to time.Time) {
		if _, err := pool.Exec(ctx, `
			INSERT INTO check_results (monitor_id, checked_at, ok, response_time_ms)
			SELECT $1, ts,
			       NOT ((ts >= $4::timestamptz AND ts < $5::timestamptz) OR (ts >= $6::timestamptz AND ts < $7::timestamptz)),
			       CASE WHEN (ts >= $4::timestamptz AND ts < $5::timestamptz) OR (ts >= $6::timestamptz AND ts < $7::timestamptz)
			            THEN NULL ELSE 150 + (extract(minute from ts)::int % 7) * 10 END
			FROM generate_series($2::timestamptz, $3::timestamptz - interval '60 seconds', interval '60 seconds') ts
		`, monitorID, from, to, inc1Start, inc1End, inc2Start, inc2End); err != nil {
			t.Fatal(err)
		}
	}
	seed(aStart, aEnd)
	seed(bStart, bEnd)
	for _, inc := range [][2]time.Time{{inc1Start, inc1End}, {inc2Start, inc2End}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO incidents (monitor_id, started_at, resolved_at, cause) VALUES ($1,$2,$3,'timeout')`,
			monitorID, inc[0], inc[1]); err != nil {
			t.Fatal(err)
		}
	}

	r := analytics.NewRollup(pool, slog.Default())
	// Hourly rollups feed the day view; raw buckets don't need them but the
	// day-period pass below does.
	if err := r.RollupHours(ctx, aStart, bEnd); err != nil {
		t.Fatalf("hourly rollup: %v", err)
	}

	anchor := time.Date(2026, 6, 11, 12, 0, 0, 0, wib)
	wantBuckets := map[string]int{"day": 24, "week": 7, "month": 30, "year": 12}

	for _, period := range []string{"day", "week", "month", "year"} {
		res, err := r.Stats(ctx, monitorID, period, anchor, wib)
		if err != nil {
			t.Fatalf("%s stats: %v", period, err)
		}
		if res.Tz != "Asia/Jakarta" {
			t.Errorf("%s: tz = %q", period, res.Tz)
		}
		if len(res.Buckets) != wantBuckets[period] {
			t.Errorf("%s: expected %d buckets, got %d", period, wantBuckets[period], len(res.Buckets))
		}

		// Sum-of-parts == whole: checks and fails exactly, downtime within
		// per-bucket integer truncation.
		var sumChecks, sumFails, sumDown int
		for _, b := range res.Buckets {
			sumChecks += b.Checks
			sumFails += b.Fails
			sumDown += b.DownS
		}
		if sumChecks != res.Checks {
			t.Errorf("%s: sum of bucket checks %d != whole %d", period, sumChecks, res.Checks)
		}
		if sumFails != res.Fails {
			t.Errorf("%s: sum of bucket fails %d != whole %d", period, sumFails, res.Fails)
		}
		if diff := int(math.Abs(float64(sumDown - res.DowntimeS))); diff > len(res.Buckets) {
			t.Errorf("%s: sum of bucket downtime %d vs whole %d (diff %d)", period, sumDown, res.DowntimeS, diff)
		}
	}

	// Ground truth for the whole scenario: 9h monitored = 540 checks;
	// incidents 45m + 50m = 5700s down, 95 failing checks.
	month, err := r.Stats(ctx, monitorID, "month", anchor, wib)
	if err != nil {
		t.Fatal(err)
	}
	if month.Checks != 540 || month.Fails != 95 {
		t.Errorf("month: expected 540 checks / 95 fails, got %d/%d", month.Checks, month.Fails)
	}
	if month.DowntimeS != 5700 {
		t.Errorf("month: expected 5700s downtime, got %d", month.DowntimeS)
	}
	// Uptime over monitored time only (paused gap excluded): 1 - 5700/32400.
	wantUp := (1 - 5700.0/32400.0) * 100
	if month.UpPct == nil || math.Abs(*month.UpPct-wantUp) > 0.01 {
		t.Errorf("month: expected %.3f%% uptime, got %v", wantUp, month.UpPct)
	}

	// TZ-correctness of the split. WIB Jun 10 (month bucket index 9) hosts
	// stretch A up to WIB midnight (4h = 240 checks) and incident 1's first
	// 30 minutes; incident 2 stays whole inside WIB Jun 11.
	day10 := month.Buckets[9]
	if !day10.Ts.Equal(time.Date(2026, 6, 9, 17, 0, 0, 0, time.UTC)) {
		t.Fatalf("bucket for WIB Jun 10 starts %v", day10.Ts)
	}
	if day10.Checks != 240 {
		t.Errorf("WIB Jun 10: expected 240 checks, got %d", day10.Checks)
	}
	if day10.DownS != 1800 {
		t.Errorf("WIB Jun 10: expected 1800s down (incident split at WIB midnight), got %d", day10.DownS)
	}
	day11 := month.Buckets[10]
	if day11.Checks != 300 {
		t.Errorf("WIB Jun 11: expected 300 checks, got %d", day11.Checks)
	}
	if day11.DownS != 3900 {
		t.Errorf("WIB Jun 11: expected 3900s down, got %d", day11.DownS)
	}

	// The same data rendered in UTC slices differently: incident 1 stays
	// whole inside UTC Jun 10, incident 2 splits at 00:00Z instead.
	utcWeek, err := r.Stats(ctx, monitorID, "week",
		time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	var utc10 *analytics.Bucket
	for i := range utcWeek.Buckets {
		if utcWeek.Buckets[i].Ts.Equal(time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)) {
			utc10 = &utcWeek.Buckets[i]
		}
	}
	if utc10 == nil {
		t.Fatal("no UTC Jun 10 bucket in week view")
	}
	// UTC Jun 10 holds stretch A entirely (6h) + stretch B's first hour
	// (23:00Z -> 24:00Z) = 420 checks; downtime = incident 1 whole (2700s)
	// + incident 2's first 30 minutes (1800s) = 4500s.
	if utc10.Checks != 420 {
		t.Errorf("UTC Jun 10: expected 420 checks, got %d", utc10.Checks)
	}
	if utc10.DownS != 4500 {
		t.Errorf("UTC Jun 10: expected 4500s down, got %d", utc10.DownS)
	}
}

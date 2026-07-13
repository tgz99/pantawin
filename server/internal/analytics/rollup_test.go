//go:build integration

package analytics_test

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tgz99/pantawin/server/internal/analytics"
	pgdb "github.com/tgz99/pantawin/server/internal/db"
)

func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "postgres:16-alpine", ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER": "pantawin", "POSTGRES_PASSWORD": "pantawin", "POSTGRES_DB": "pantawin_test",
			},
			WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432")
	dsn := fmt.Sprintf("postgres://pantawin:pantawin@%s:%s/pantawin_test?sslmode=disable", host, port.Port())
	if err := pgdb.Migrate(dsn, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestRollupConsistency is the M4 exit-criteria rollup suite: hourly and
// daily rollups agree with each other and with the incident math.
//
// Scenario (all in the past, UTC): checks every 60s from 10:00 to 13:00,
// incident 11:30 -> 11:45 (900s down, checks failing during it). The monitor
// is inactive (no checks) outside those 3 hours — a "paused window".
func TestRollupConsistency(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)

	var userID, monitorID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('a@t.test','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO monitors (user_id, name, url) VALUES ($1,'m','https://example.com') RETURNING id`, userID).Scan(&monitorID); err != nil {
		t.Fatal(err)
	}

	day := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	from := day.Add(10 * time.Hour) // 10:00
	to := day.Add(13 * time.Hour)   // 13:00 (exclusive)
	incidentStart := day.Add(11*time.Hour + 30*time.Minute)
	incidentEnd := day.Add(11*time.Hour + 45*time.Minute)

	// Checks every 60s; failing during the incident, 200ms otherwise.
	if _, err := pool.Exec(ctx, `
		INSERT INTO check_results (monitor_id, checked_at, ok, response_time_ms)
		SELECT $1, ts,
		       NOT (ts >= $4::timestamptz AND ts < $5::timestamptz),
		       CASE WHEN ts >= $4::timestamptz AND ts < $5::timestamptz THEN NULL ELSE 200 END
		FROM generate_series($2::timestamptz, $3::timestamptz - interval '60 seconds', interval '60 seconds') ts
	`, monitorID, from, to, incidentStart, incidentEnd); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO incidents (monitor_id, started_at, resolved_at, cause) VALUES ($1,$2,$3,'http')`,
		monitorID, incidentStart, incidentEnd); err != nil {
		t.Fatal(err)
	}

	r := analytics.NewRollup(pool, slog.Default())
	if err := r.RollupHours(ctx, from, to); err != nil {
		t.Fatalf("hourly rollup: %v", err)
	}
	if err := r.RollupDays(ctx, day, day); err != nil {
		t.Fatalf("daily rollup: %v", err)
	}

	// Hourly expectations.
	type hourly struct {
		checks, fails int
		upPct         *float64
	}
	readHour := func(h time.Time) hourly {
		var row hourly
		err := pool.QueryRow(ctx,
			`SELECT checks, fails, up_pct FROM stats_hourly WHERE monitor_id=$1 AND hour_ts=$2`,
			monitorID, h).Scan(&row.checks, &row.fails, &row.upPct)
		if err != nil {
			t.Fatalf("read hour %v: %v", h, err)
		}
		return row
	}
	h10 := readHour(day.Add(10 * time.Hour))
	if h10.checks != 60 || h10.fails != 0 || h10.upPct == nil || *h10.upPct != 100 {
		t.Errorf("hour 10: unexpected %+v", h10)
	}
	h11 := readHour(day.Add(11 * time.Hour))
	if h11.checks != 60 || h11.fails != 15 {
		t.Errorf("hour 11: expected 60 checks / 15 fails, got %+v", h11)
	}
	// 900s down of 3600 monitored -> 75%.
	if h11.upPct == nil || math.Abs(*h11.upPct-75) > 0.01 {
		t.Errorf("hour 11: expected 75%% uptime, got %v", h11.upPct)
	}

	// No row for a paused hour.
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM stats_hourly WHERE monitor_id=$1 AND hour_ts=$2`,
		monitorID, day.Add(9*time.Hour)).Scan(&n)
	if n != 0 {
		t.Error("expected no hourly row for an hour with zero activity")
	}

	// Daily row and consistency: sum of hourly == daily.
	var dChecks, dFails, dDowntime int
	var dUp *float64
	if err := pool.QueryRow(ctx,
		`SELECT checks, fails, downtime_s, up_pct FROM stats_daily WHERE monitor_id=$1 AND day=$2`,
		monitorID, day).Scan(&dChecks, &dFails, &dDowntime, &dUp); err != nil {
		t.Fatalf("read daily: %v", err)
	}
	var sumChecks, sumFails int
	pool.QueryRow(ctx,
		`SELECT coalesce(sum(checks),0), coalesce(sum(fails),0) FROM stats_hourly WHERE monitor_id=$1 AND hour_ts >= $2 AND hour_ts < $3`,
		monitorID, day, day.Add(24*time.Hour)).Scan(&sumChecks, &sumFails)
	if dChecks != sumChecks || dFails != sumFails {
		t.Errorf("daily (%d/%d) != sum of hourly (%d/%d)", dChecks, dFails, sumChecks, sumFails)
	}
	if dChecks != 180 || dFails != 15 {
		t.Errorf("daily: expected 180 checks / 15 fails, got %d/%d", dChecks, dFails)
	}
	if dDowntime != 900 {
		t.Errorf("daily: expected 900s downtime, got %d", dDowntime)
	}
	// Paused windows excluded: monitored = 3 active hours -> 1 - 900/10800.
	wantUp := (1 - 900.0/10800.0) * 100
	if dUp == nil || math.Abs(*dUp-wantUp) > 0.01 {
		t.Errorf("daily: expected %.3f%% uptime, got %v", wantUp, dUp)
	}

	// Stats() agrees with the rollups it serves. Since M5 every bucket
	// window is emitted (empty ones with nil up_pct), so a day is always 24.
	dayStats, err := r.Stats(ctx, monitorID, analytics.PeriodDay, day, time.UTC)
	if err != nil {
		t.Fatalf("day stats: %v", err)
	}
	if len(dayStats.Buckets) != 24 {
		t.Errorf("expected 24 hourly buckets, got %d", len(dayStats.Buckets))
	}
	active := 0
	for _, b := range dayStats.Buckets {
		if b.Checks > 0 {
			active++
		}
	}
	if active != 3 {
		t.Errorf("expected 3 hourly buckets with checks, got %d", active)
	}
	if dayStats.Checks != 180 || dayStats.DowntimeS != 900 {
		t.Errorf("day stats: expected 180 checks / 900s down, got %d/%d", dayStats.Checks, dayStats.DowntimeS)
	}
	if dayStats.UpPct == nil || math.Abs(*dayStats.UpPct-wantUp) > 0.01 {
		t.Errorf("day stats: expected %.3f%% uptime, got %v", wantUp, dayStats.UpPct)
	}

	weekStats, err := r.Stats(ctx, monitorID, analytics.PeriodWeek, day, time.UTC)
	if err != nil {
		t.Fatalf("week stats: %v", err)
	}
	if len(weekStats.Buckets) != 7 {
		t.Errorf("expected 7 daily buckets in week view, got %d", len(weekStats.Buckets))
	}
	activeDays := 0
	for _, b := range weekStats.Buckets {
		if b.Checks > 0 {
			activeDays++
		}
	}
	if activeDays != 1 {
		t.Errorf("expected 1 daily bucket with checks, got %d", activeDays)
	}
	if weekStats.Checks != dayStats.Checks || weekStats.DowntimeS != dayStats.DowntimeS {
		t.Errorf("week stats disagree with day stats: %+v vs %+v", weekStats, dayStats)
	}
}

//go:build integration

package notify_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"log/slog"

	pgdb "github.com/tgz99/pantawin/server/internal/db"
	"github.com/tgz99/pantawin/server/internal/notify"
)

// captureChannel records every Send so the test can assert exactly-once.
type captureChannel struct {
	name string
	mu   sync.Mutex
	sent []notify.IncidentEvent
}

func (c *captureChannel) Name() string { return c.name }
func (c *captureChannel) Send(ctx context.Context, e notify.IncidentEvent, t notify.ChannelTarget) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, e)
	return nil
}
func (c *captureChannel) count(eventType notify.EventType) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.sent {
		if e.EventType == eventType {
			n++
		}
	}
	return n
}

func startContainer(t *testing.T, image, port string, env map[string]string) (host, mapped string) {
	t.Helper()
	ctx := context.Background()
	natPort := port + "/tcp"
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: image, ExposedPorts: []string{natPort}, Env: env,
			WaitingFor: wait.ForListeningPort(natPort).WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start %s: %v", image, err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	h, _ := c.Host(ctx)
	p, _ := c.MappedPort(ctx, port)
	return h, p.Port()
}

// TestDispatcher_ExactlyOncePerTransition is the M2 exit-criteria test at the
// unit-integration level: enqueueing the same DOWN event repeatedly (as
// worker retries or redelivery would) results in exactly one Send, enforced
// by the notification_log UNIQUE claim.
func TestDispatcher_ExactlyOncePerTransition(t *testing.T) {
	ctx := context.Background()

	pgHost, pgPort := startContainer(t, "postgres:16-alpine", "5432", map[string]string{
		"POSTGRES_USER": "pantawin", "POSTGRES_PASSWORD": "pantawin", "POSTGRES_DB": "pantawin_test",
	})
	dsn := fmt.Sprintf("postgres://pantawin:pantawin@%s:%s/pantawin_test?sslmode=disable", pgHost, pgPort)
	if err := pgdb.Migrate(dsn, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rHost, rPort := startContainer(t, "redis:7-alpine", "6379", nil)
	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", rHost, rPort)})
	defer rdb.Close()

	// Seed a user, monitor, and an open incident so the FK targets exist.
	var userID, monitorID, incidentID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('t@t.test','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO monitors (user_id, name, url) VALUES ($1,'m','https://example.com') RETURNING id`, userID).Scan(&monitorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO incidents (monitor_id, cause) VALUES ($1,'timeout') RETURNING id`, monitorID).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}

	capture := &captureChannel{name: "email"}
	dispatcher := notify.NewDispatcher(pool, rdb, []notify.AlertChannel{capture},
		func(ctx context.Context, mID int64) (notify.ChannelTarget, []string, error) {
			return notify.ChannelTarget{Email: "t@t.test"}, []string{"email"}, nil
		}, slog.Default())

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go dispatcher.Run(runCtx)

	downEvent := notify.IncidentEvent{
		IncidentID: incidentID, MonitorID: monitorID, MonitorName: "m", MonitorURL: "https://example.com",
		EventType: notify.EventDown, Cause: "timeout", At: time.Now(),
	}
	// Enqueue the SAME event five times — simulating redelivery / retries.
	for i := 0; i < 5; i++ {
		if err := dispatcher.Enqueue(ctx, downEvent); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	// Give the worker time to drain.
	waitForCount(t, capture, notify.EventDown, 1)

	if got := capture.count(notify.EventDown); got != 1 {
		t.Errorf("expected exactly 1 DOWN send across 5 enqueues, got %d", got)
	}

	// notification_log has exactly one row for this (incident, channel, DOWN).
	var logRows int
	pool.QueryRow(ctx, `SELECT count(*) FROM notification_log WHERE incident_id=$1 AND channel='email' AND event_type='DOWN'`, incidentID).Scan(&logRows)
	if logRows != 1 {
		t.Errorf("expected 1 notification_log row, got %d", logRows)
	}

	// A RECOVERED event for the same incident is a different (incident,
	// channel, event_type) tuple, so it's delivered exactly once too.
	recoveredEvent := downEvent
	recoveredEvent.EventType = notify.EventRecovered
	for i := 0; i < 3; i++ {
		dispatcher.Enqueue(ctx, recoveredEvent)
	}
	waitForCount(t, capture, notify.EventRecovered, 1)
	if got := capture.count(notify.EventRecovered); got != 1 {
		t.Errorf("expected exactly 1 RECOVERED send, got %d", got)
	}
}

// TestDispatcher_PushAndEmailBothFireOnce is the M3 exit-criteria test: a
// monitor configured with alert_channels = {email, push} gets exactly one
// send on EACH channel per transition, even under redelivery. The claim key
// is (incident, channel, event_type), so the two channels claim independently
// without double-sending either.
func TestDispatcher_PushAndEmailBothFireOnce(t *testing.T) {
	ctx := context.Background()

	pgHost, pgPort := startContainer(t, "postgres:16-alpine", "5432", map[string]string{
		"POSTGRES_USER": "pantawin", "POSTGRES_PASSWORD": "pantawin", "POSTGRES_DB": "pantawin_test",
	})
	dsn := fmt.Sprintf("postgres://pantawin:pantawin@%s:%s/pantawin_test?sslmode=disable", pgHost, pgPort)
	if err := pgdb.Migrate(dsn, "../../migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	rHost, rPort := startContainer(t, "redis:7-alpine", "6379", nil)
	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", rHost, rPort)})
	defer rdb.Close()

	var userID, monitorID, incidentID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('t@t.test','x') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO monitors (user_id, name, url, alert_channels) VALUES ($1,'m','https://example.com','{email,push}') RETURNING id`, userID).Scan(&monitorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO incidents (monitor_id, cause) VALUES ($1,'timeout') RETURNING id`, monitorID).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}

	email := &captureChannel{name: "email"}
	push := &captureChannel{name: "push"}
	dispatcher := notify.NewDispatcher(pool, rdb, []notify.AlertChannel{email, push},
		func(ctx context.Context, mID int64) (notify.ChannelTarget, []string, error) {
			return notify.ChannelTarget{Email: "t@t.test"}, []string{"email", "push"}, nil
		}, slog.Default())

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go dispatcher.Run(runCtx)

	downEvent := notify.IncidentEvent{
		IncidentID: incidentID, MonitorID: monitorID, MonitorName: "m", MonitorURL: "https://example.com",
		EventType: notify.EventDown, Cause: "timeout", At: time.Now(),
	}
	for i := 0; i < 5; i++ {
		if err := dispatcher.Enqueue(ctx, downEvent); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	waitForCount(t, email, notify.EventDown, 1)
	waitForCount(t, push, notify.EventDown, 1)

	if got := email.count(notify.EventDown); got != 1 {
		t.Errorf("email: expected exactly 1 DOWN send, got %d", got)
	}
	if got := push.count(notify.EventDown); got != 1 {
		t.Errorf("push: expected exactly 1 DOWN send, got %d", got)
	}

	recoveredEvent := downEvent
	recoveredEvent.EventType = notify.EventRecovered
	for i := 0; i < 3; i++ {
		dispatcher.Enqueue(ctx, recoveredEvent)
	}
	waitForCount(t, email, notify.EventRecovered, 1)
	waitForCount(t, push, notify.EventRecovered, 1)
	if got := email.count(notify.EventRecovered); got != 1 {
		t.Errorf("email: expected exactly 1 RECOVERED send, got %d", got)
	}
	if got := push.count(notify.EventRecovered); got != 1 {
		t.Errorf("push: expected exactly 1 RECOVERED send, got %d", got)
	}

	// The ledger has one row per (channel, event_type): 4 total.
	var logRows int
	pool.QueryRow(ctx, `SELECT count(*) FROM notification_log WHERE incident_id=$1`, incidentID).Scan(&logRows)
	if logRows != 4 {
		t.Errorf("expected 4 notification_log rows (2 channels x 2 events), got %d", logRows)
	}
}

func waitForCount(t *testing.T, c *captureChannel, et notify.EventType, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if c.count(et) >= want {
			// Extra settle time to catch any erroneous duplicate sends.
			time.Sleep(500 * time.Millisecond)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s sends (got %d)", want, et, c.count(et))
}

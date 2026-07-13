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

// flakyChannel records sends like captureChannel but fails the first
// failFirst of them, so tests can exercise the retry path.
type flakyChannel struct {
	captureChannel
	failFirst int
}

func (f *flakyChannel) Send(ctx context.Context, e notify.IncidentEvent, t notify.ChannelTarget) error {
	_ = f.captureChannel.Send(ctx, e, t)
	f.mu.Lock()
	n := len(f.sent)
	f.mu.Unlock()
	if n <= f.failFirst {
		return fmt.Errorf("simulated transport failure %d", n)
	}
	return nil
}

// TestDispatcher_RetriesFailedSendsBounded covers the retry fast-follow: a
// send that fails transiently is re-sent by RunRetrier until it succeeds
// (email here: fails twice, lands on attempt 3), while a permanently dead
// transport (push here) stops at maxSendAttempts=4 instead of retrying
// forever. Attempts and outcome are asserted against the notification_log
// ledger, which is what makes the retries restart-safe.
func TestDispatcher_RetriesFailedSendsBounded(t *testing.T) {
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

	email := &flakyChannel{captureChannel: captureChannel{name: "email"}, failFirst: 2}
	push := &flakyChannel{captureChannel: captureChannel{name: "push"}, failFirst: 999} // never succeeds
	dispatcher := notify.NewDispatcher(pool, rdb, []notify.AlertChannel{email, push},
		func(ctx context.Context, mID int64) (notify.ChannelTarget, []string, error) {
			return notify.ChannelTarget{Email: "t@t.test"}, []string{"email", "push"}, nil
		}, slog.Default()).
		// Shrink the cadence so the 1m/4m/16m production backoff becomes
		// 50ms/200ms/800ms and the whole schedule fits in a test run.
		WithRetryTuning(100*time.Millisecond, 50*time.Millisecond)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go dispatcher.Run(runCtx)
	go dispatcher.RunRetrier(runCtx)

	downEvent := notify.IncidentEvent{
		IncidentID: incidentID, MonitorID: monitorID, MonitorName: "m", MonitorURL: "https://example.com",
		EventType: notify.EventDown, Cause: "timeout", At: time.Now(),
	}
	if err := dispatcher.Enqueue(ctx, downEvent); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Email: initial send + 2 retries, third send succeeds.
	waitForCount(t, &email.captureChannel, notify.EventDown, 3)
	var emailOK bool
	var emailAttempts int
	if err := pool.QueryRow(ctx, `SELECT ok, attempts FROM notification_log WHERE incident_id=$1 AND channel='email' AND event_type='DOWN'`,
		incidentID).Scan(&emailOK, &emailAttempts); err != nil {
		t.Fatalf("email ledger row: %v", err)
	}
	if !emailOK || emailAttempts != 3 {
		t.Errorf("email: expected ok=true attempts=3, got ok=%v attempts=%d", emailOK, emailAttempts)
	}

	// Push: never succeeds — must stop at exactly maxSendAttempts (4) sends.
	waitForCount(t, &push.captureChannel, notify.EventDown, 4)
	// Several retry polls' worth of settle time to catch a 5th send.
	time.Sleep(1 * time.Second)
	if got := push.count(notify.EventDown); got != 4 {
		t.Errorf("push: expected exactly 4 sends (attempt cap), got %d", got)
	}
	var pushOK bool
	var pushAttempts int
	if err := pool.QueryRow(ctx, `SELECT ok, attempts FROM notification_log WHERE incident_id=$1 AND channel='push' AND event_type='DOWN'`,
		incidentID).Scan(&pushOK, &pushAttempts); err != nil {
		t.Fatalf("push ledger row: %v", err)
	}
	if pushOK || pushAttempts != 4 {
		t.Errorf("push: expected ok=false attempts=4, got ok=%v attempts=%d", pushOK, pushAttempts)
	}

	// Email must not have been re-sent while push was retrying.
	if got := email.count(notify.EventDown); got != 3 {
		t.Errorf("email: expected sends to stay at 3 after success, got %d", got)
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

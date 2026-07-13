//go:build integration

// Integration suite — needs a Docker daemon (testcontainers-go spins up
// real Postgres + Redis). Run with: go test -tags=integration ./...
// Deliberately excluded from the default `go test ./...` run so unit tests
// stay fast and don't require Docker to be available.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tgz99/pantawin/server/internal/auth"
	"github.com/tgz99/pantawin/server/internal/checker"
	pgdb "github.com/tgz99/pantawin/server/internal/db"
	"github.com/tgz99/pantawin/server/internal/httpapi"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/scheduler"
	"github.com/tgz99/pantawin/server/internal/ssrf"
)

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "pantawin",
			"POSTGRES_PASSWORD": "pantawin",
			"POSTGRES_DB":       "pantawin_test",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get postgres host: %v", err)
	}
	port, err := c.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("failed to get postgres port: %v", err)
	}

	return fmt.Sprintf("postgres://pantawin:pantawin@%s:%s/pantawin_test?sslmode=disable", host, port.Port())
}

func startRedis(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start redis container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get redis host: %v", err)
	}
	port, err := c.MappedPort(ctx, "6379")
	if err != nil {
		t.Fatalf("failed to get redis port: %v", err)
	}

	return fmt.Sprintf("%s:%s", host, port.Port())
}

// allowLoopbackResolver lets integration tests point monitors at local
// httptest servers, which the production SSRF guard would (correctly) block.
type allowAllResolver struct{}

func (allowAllResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
}

type testEnv struct {
	server      *httptest.Server
	pool        *pgxpool.Pool
	redisClient *redis.Client
	monitorRepo *monitor.Repository
	sched       *scheduler.Scheduler
	issuer      *auth.TokenIssuer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()

	dsn := startPostgres(t)
	redisAddr := startRedis(t)

	if err := pgdb.Migrate(dsn, "../../migrations"); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect pool: %v", err)
	}
	t.Cleanup(pool.Close)

	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = redisClient.Close() })

	authRepo := auth.NewRepository(pool)
	issuer := auth.NewTokenIssuer("test-secret", 15*time.Minute, 30*24*time.Hour)
	refreshStore := auth.NewRefreshStore(pool)
	authService := auth.NewService(authRepo, issuer, refreshStore, 30*24*time.Hour)

	monitorRepo := monitor.NewRepository(pool)

	// Guard with a permissive resolver + loopback allowance: URL-scheme and
	// non-loopback range checks still run, but httptest targets (literal
	// 127.0.0.1 URLs) are reachable. The real range checks have their own
	// dedicated unit suite in internal/ssrf.
	guard := ssrf.NewGuardWithResolver(allowAllResolver{})
	guard.AllowLoopback = true
	chk := checker.New(5 * time.Second)
	sched := scheduler.New(redisClient, monitorRepo, chk, guard, slog.Default())

	router := httpapi.NewRouter(httpapi.RouterDeps{
		AuthService: authService,
		Issuer:      issuer,
		MonitorRepo: monitorRepo,
		Guard:       guard,
		Scheduler:   sched,
		Redis:       redisClient,
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &testEnv{
		server: server, pool: pool, redisClient: redisClient,
		monitorRepo: monitorRepo, sched: sched, issuer: issuer,
	}
}

type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (e *testEnv) register(t *testing.T, email, password string) tokens {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
	resp, err := http.Post(e.server.URL+"/v1/auth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from register, got %d", resp.StatusCode)
	}
	var tk tokens
	if err := json.NewDecoder(resp.Body).Decode(&tk); err != nil {
		t.Fatalf("failed to decode register response: %v", err)
	}
	return tk
}

func (e *testEnv) do(t *testing.T, method, path, accessToken string, body any) *http.Response {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	return resp
}

func TestMonitorCRUDLifecycle(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "crud@pantawin.test", "correct horse battery staple")

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	// Create
	resp := env.do(t, http.MethodPost, "/v1/monitors", tk.AccessToken, map[string]any{
		"name": "target", "url": target.URL, "interval_seconds": 30,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from create, got %d", resp.StatusCode)
	}
	var created struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.Status != "PENDING" {
		t.Errorf("new monitor should be PENDING, got %s", created.Status)
	}

	// Read
	resp = env.do(t, http.MethodGet, fmt.Sprintf("/v1/monitors/%d", created.ID), tk.AccessToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from get, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Update
	resp = env.do(t, http.MethodPatch, fmt.Sprintf("/v1/monitors/%d", created.ID), tk.AccessToken, map[string]any{
		"name": "renamed", "failure_threshold": 3,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from patch, got %d", resp.StatusCode)
	}
	var updated struct {
		Name             string `json:"name"`
		FailureThreshold int    `json:"failure_threshold"`
	}
	json.NewDecoder(resp.Body).Decode(&updated)
	resp.Body.Close()
	if updated.Name != "renamed" || updated.FailureThreshold != 3 {
		t.Errorf("patch not applied: %+v", updated)
	}

	// Pause / resume
	resp = env.do(t, http.MethodPost, fmt.Sprintf("/v1/monitors/%d/pause", created.ID), tk.AccessToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from pause, got %d", resp.StatusCode)
	}
	var paused struct {
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&paused)
	resp.Body.Close()
	if paused.Status != "PAUSED" {
		t.Errorf("expected PAUSED after pause, got %s", paused.Status)
	}

	resp = env.do(t, http.MethodPost, fmt.Sprintf("/v1/monitors/%d/resume", created.ID), tk.AccessToken, nil)
	var resumed struct {
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&resumed)
	resp.Body.Close()
	if resumed.Status != "PENDING" {
		t.Errorf("resume should force a fresh PENDING confirmation cycle, got %s", resumed.Status)
	}

	// Delete
	resp = env.do(t, http.MethodDelete, fmt.Sprintf("/v1/monitors/%d", created.ID), tk.AccessToken, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 from delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.do(t, http.MethodGet, fmt.Sprintf("/v1/monitors/%d", created.ID), tk.AccessToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMonitorValidation(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "validation@pantawin.test", "correct horse battery staple")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing url", map[string]any{"name": "x"}},
		{"bad scheme", map[string]any{"url": "ftp://example.com"}},
		{"interval too low", map[string]any{"url": "https://example.com", "interval_seconds": 5}},
		{"bad method", map[string]any{"url": "https://example.com", "method": "DELETE"}},
		{"threshold zero", map[string]any{"url": "https://example.com", "failure_threshold": 0}},
	}
	for _, tc := range cases {
		resp := env.do(t, http.MethodPost, "/v1/monitors", tk.AccessToken, tc.body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", tc.name, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestMonitorOwnershipIsolation(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice@pantawin.test", "correct horse battery staple")
	bob := env.register(t, "bob@pantawin.test", "correct horse battery staple")

	resp := env.do(t, http.MethodPost, "/v1/monitors", alice.AccessToken, map[string]any{
		"url": "https://example.com",
	})
	var created struct {
		ID int64 `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Bob cannot see, edit, or delete Alice's monitor.
	for _, probe := range []struct {
		method, path string
	}{
		{http.MethodGet, fmt.Sprintf("/v1/monitors/%d", created.ID)},
		{http.MethodDelete, fmt.Sprintf("/v1/monitors/%d", created.ID)},
		{http.MethodPost, fmt.Sprintf("/v1/monitors/%d/pause", created.ID)},
	} {
		resp := env.do(t, probe.method, probe.path, bob.AccessToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s as non-owner: expected 404, got %d", probe.method, probe.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestRefreshRotation(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "rotate@pantawin.test", "correct horse battery staple")

	// First refresh succeeds and yields new tokens.
	resp := env.do(t, http.MethodPost, "/v1/auth/refresh", "", map[string]any{
		"refresh_token": tk.RefreshToken,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first refresh: expected 200, got %d", resp.StatusCode)
	}
	var rotated tokens
	json.NewDecoder(resp.Body).Decode(&rotated)
	resp.Body.Close()
	if rotated.RefreshToken == tk.RefreshToken {
		t.Error("refresh should rotate the refresh token, got the same one back")
	}

	// Replaying the consumed token must fail.
	resp = env.do(t, http.MethodPost, "/v1/auth/refresh", "", map[string]any{
		"refresh_token": tk.RefreshToken,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("replayed refresh token: expected 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The rotated token still works.
	resp = env.do(t, http.MethodPost, "/v1/auth/refresh", "", map[string]any{
		"refresh_token": rotated.RefreshToken,
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("rotated refresh token: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// End-to-end state machine: a monitor with failure_threshold 2 pointed at a
// target that starts failing goes DOWN only after the second consecutive
// failed check, and recovers on the first success (spec 7.2 item 1, at the
// integration level — the exhaustive cases live in the statemachine unit
// suite).
func TestStateMachineEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "sm@pantawin.test", "correct horse battery staple")
	ctx := context.Background()

	healthy := true
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer target.Close()

	resp := env.do(t, http.MethodPost, "/v1/monitors", tk.AccessToken, map[string]any{
		"url": target.URL, "failure_threshold": 2,
	})
	var created struct {
		ID int64 `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	chk := checker.New(2 * time.Second)
	runCheck := func() monitor.CheckOutcome {
		m, err := env.monitorRepo.GetByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("load monitor: %v", err)
		}
		result := chk.Check(ctx, m.Method, m.URL, m.ExpectedStatusMin, m.ExpectedStatusMax)
		outcome, err := env.monitorRepo.RecordCheck(ctx, m.ID, result)
		if err != nil {
			t.Fatalf("record check: %v", err)
		}
		return outcome
	}

	// PENDING -> UP on first success.
	out := runCheck()
	if !out.Transitioned || out.To != monitor.StatusUp {
		t.Fatalf("first successful check: expected transition to UP, got %+v", out)
	}

	// First failure: still UP (threshold 2).
	healthy = false
	out = runCheck()
	if out.Transitioned || out.To != monitor.StatusUp {
		t.Fatalf("first failure: expected no transition (still UP), got %+v", out)
	}

	// Second consecutive failure: DOWN.
	out = runCheck()
	if !out.Transitioned || out.To != monitor.StatusDown {
		t.Fatalf("second consecutive failure: expected transition to DOWN, got %+v", out)
	}

	// Recovery on first success.
	healthy = true
	out = runCheck()
	if !out.Transitioned || out.To != monitor.StatusUp {
		t.Fatalf("recovery: expected transition to UP, got %+v", out)
	}
}

func TestListMonitors_RejectsMissingAuth(t *testing.T) {
	env := newTestEnv(t)

	resp, err := http.Get(env.server.URL + "/v1/monitors")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without an Authorization header, got %d", resp.StatusCode)
	}
}

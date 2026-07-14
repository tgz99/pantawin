//go:build integration

// Integration suite â€” needs a Docker daemon (testcontainers-go spins up
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

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/tgz99/pantawin/server/internal/analytics"
	"github.com/tgz99/pantawin/server/internal/auth"
	"github.com/tgz99/pantawin/server/internal/checker"
	pgdb "github.com/tgz99/pantawin/server/internal/db"
	"github.com/tgz99/pantawin/server/internal/device"
	"github.com/tgz99/pantawin/server/internal/httpapi"
	"github.com/tgz99/pantawin/server/internal/incident"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/realtime"
	"github.com/tgz99/pantawin/server/internal/scheduler"
	"github.com/tgz99/pantawin/server/internal/ssrf"
	"github.com/tgz99/pantawin/server/internal/team"
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
	publisher   *realtime.Publisher
	adminID     int64 // only set by newGatedTestEnv
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return buildTestEnv(t, false)
}

// newGatedTestEnv wires the M6.1 invite store (signup closed-by-default) and
// bootstraps an admin account ("admin@pantawin.test" / "Admin-Pass-42") that
// may manage the team. The plain newTestEnv leaves signup open so unrelated
// tests can keep registering users freely.
func newGatedTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return buildTestEnv(t, true)
}

func buildTestEnv(t *testing.T, gated bool) *testEnv {
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
	// Fake Google verifier: token "google-ok:<email>" verifies as that email.
	fakeGoogle := func(ctx context.Context, raw string) (auth.GoogleIdentity, error) {
		if email, ok := strings.CutPrefix(raw, "google-ok:"); ok {
			return auth.GoogleIdentity{Email: email, Verified: true}, nil
		}
		return auth.GoogleIdentity{}, auth.ErrGoogleTokenInvalid
	}
	authService := auth.NewService(authRepo, issuer, refreshStore, 30*24*time.Hour).
		WithGoogleVerifier(fakeGoogle)

	teamRepo := team.NewRepository(pool)
	var adminID int64
	if gated {
		authService = authService.WithSignupAllowlistStore(teamRepo.Allowed)
		hash, err := auth.HashPassword("Admin-Pass-42")
		if err != nil {
			t.Fatalf("hash admin password: %v", err)
		}
		admin, err := authRepo.CreateUser(ctx, "admin@pantawin.test", hash)
		if err != nil {
			t.Fatalf("create admin user: %v", err)
		}
		adminID = admin.ID
	}

	monitorRepo := monitor.NewRepository(pool)

	// Guard with a permissive resolver + loopback allowance: URL-scheme and
	// non-loopback range checks still run, but httptest targets (literal
	// 127.0.0.1 URLs) are reachable. The real range checks have their own
	// dedicated unit suite in internal/ssrf.
	guard := ssrf.NewGuardWithResolver(allowAllResolver{})
	guard.AllowLoopback = true
	chk := checker.New(5 * time.Second)
	sched := scheduler.New(redisClient, monitorRepo, chk, guard, slog.Default())

	deviceRepo := device.NewRepository(pool)
	publisher := realtime.NewPublisher(redisClient)
	wsHandler := realtime.NewHandler(redisClient, slog.Default())

	router := httpapi.NewRouter(httpapi.RouterDeps{
		AuthService: authService,
		Issuer:      issuer,
		MonitorRepo: monitorRepo,
		DeviceRepo:  deviceRepo,
		Guard:       guard,
		Scheduler:   sched,
		Realtime:    wsHandler,
		Redis:        redisClient,
		Rollup:       analytics.NewRollup(pool, slog.Default()),
		IncidentRepo: incident.NewRepository(pool),
		TeamRepo:     teamRepo,
		AdminUserID:  adminID,
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &testEnv{
		server: server, pool: pool, redisClient: redisClient,
		monitorRepo: monitorRepo, sched: sched, issuer: issuer, publisher: publisher,
		adminID: adminID,
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

func TestGoogleLogin(t *testing.T) {
	env := newTestEnv(t)

	// Invalid token rejected.
	resp := env.do(t, http.MethodPost, "/v1/auth/google", "", map[string]any{"id_token": "garbage"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad google token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// First google login creates the account and returns tokens.
	resp = env.do(t, http.MethodPost, "/v1/auth/google", "", map[string]any{"id_token": "google-ok:g@pantawin.test"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for google login, got %d", resp.StatusCode)
	}
	var tk tokens
	if err := json.NewDecoder(resp.Body).Decode(&tk); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if tk.AccessToken == "" {
		t.Fatal("expected access token from google login")
	}

	// The session works like any other.
	resp = env.do(t, http.MethodGet, "/v1/monitors", tk.AccessToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing monitors with google session, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Second login reuses the account (no duplicate user).
	resp = env.do(t, http.MethodPost, "/v1/auth/google", "", map[string]any{"id_token": "google-ok:g@pantawin.test"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for repeat google login, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	var count int
	env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE email = 'g@pantawin.test'`).Scan(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 user after two google logins, got %d", count)
	}

	// A google login for an EXISTING password account links to it — same
	// account, not a duplicate.
	env.register(t, "linked@pantawin.test", "Correct-Horse-42-staple")
	resp = env.do(t, http.MethodPost, "/v1/auth/google", "", map[string]any{"id_token": "google-ok:linked@pantawin.test"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for google login on existing account, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM users WHERE email = 'linked@pantawin.test'`).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 user for linked account, got %d", count)
	}
}

func TestChangePasswordFlow(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "chpw@pantawin.test", "Correct-Horse-42-staple")

	// Weak new password rejected (policy: min 8, 1 upper, 1 digit).
	resp := env.do(t, http.MethodPost, "/v1/auth/change-password", tk.AccessToken, map[string]any{
		"current_password": "Correct-Horse-42-staple", "new_password": "weakpass",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for weak password, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong current password rejected with 400 (not 401 — access token is valid).
	resp = env.do(t, http.MethodPost, "/v1/auth/change-password", tk.AccessToken, map[string]any{
		"current_password": "not-my-password", "new_password": "NewSecret99",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong current password, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Unauthenticated request rejected.
	resp = env.do(t, http.MethodPost, "/v1/auth/change-password", "", map[string]any{
		"current_password": "Correct-Horse-42-staple", "new_password": "NewSecret99",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Valid change succeeds and returns a fresh token pair.
	resp = env.do(t, http.MethodPost, "/v1/auth/change-password", tk.AccessToken, map[string]any{
		"current_password": "Correct-Horse-42-staple", "new_password": "NewSecret99",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for valid change, got %d", resp.StatusCode)
	}
	var fresh tokens
	if err := json.NewDecoder(resp.Body).Decode(&fresh); err != nil {
		t.Fatalf("decode change-password response: %v", err)
	}
	resp.Body.Close()
	if fresh.AccessToken == "" || fresh.RefreshToken == "" {
		t.Fatal("expected fresh tokens after password change")
	}

	// Old password no longer logs in; new one does.
	resp, err := http.Post(env.server.URL+"/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"chpw@pantawin.test","password":"Correct-Horse-42-staple"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 logging in with old password, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, err = http.Post(env.server.URL+"/v1/auth/login", "application/json",
		strings.NewReader(`{"email":"chpw@pantawin.test","password":"NewSecret99"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 logging in with new password, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Pre-change refresh token was revoked (all sessions invalidated).
	resp = env.do(t, http.MethodPost, "/v1/auth/refresh", "", map[string]any{
		"refresh_token": tk.RefreshToken,
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked pre-change refresh token, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The fresh post-change refresh token still works.
	resp = env.do(t, http.MethodPost, "/v1/auth/refresh", "", map[string]any{
		"refresh_token": fresh.RefreshToken,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for post-change refresh token, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestMonitorCRUDLifecycle(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "crud@pantawin.test", "Correct-Horse-42-staple")

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
	tk := env.register(t, "validation@pantawin.test", "Correct-Horse-42-staple")

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
	alice := env.register(t, "alice@pantawin.test", "Correct-Horse-42-staple")
	bob := env.register(t, "bob@pantawin.test", "Correct-Horse-42-staple")

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
	tk := env.register(t, "rotate@pantawin.test", "Correct-Horse-42-staple")

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
// integration level â€” the exhaustive cases live in the statemachine unit
// suite).
func TestStateMachineEndToEnd(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "sm@pantawin.test", "Correct-Horse-42-staple")
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

func TestWebSocketReceivesPublishedEvents(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "ws@pantawin.test", "Correct-Horse-42-staple")
	userID, err := env.issuer.ParseAccessToken(tk.AccessToken)
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(env.server.URL, "http") + "/v1/ws?access_token=" + tk.AccessToken

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v", err)
	}
	defer conn.CloseNow()

	// Give the server a beat to establish its Redis subscription before we
	// publish, otherwise the message can be missed.
	time.Sleep(300 * time.Millisecond)

	rtMS := 123
	want := realtime.Event{
		Type: "status", MonitorID: 42, MonitorName: "gratisaja.com",
		Status: "UP", ResponseTimeMS: &rtMS, At: time.Now().UTC().Format(time.RFC3339),
	}
	if err := env.publisher.Publish(ctx, userID, want); err != nil {
		t.Fatalf("publish: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read failed: %v", err)
	}
	var got realtime.Event
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal ws event: %v", err)
	}
	if got.Type != "status" || got.MonitorID != 42 || got.Status != "UP" {
		t.Errorf("unexpected ws event: %+v", got)
	}
}

func TestWebSocketRejectsBadToken(t *testing.T) {
	env := newTestEnv(t)
	wsURL := "ws" + strings.TrimPrefix(env.server.URL, "http") + "/v1/ws?access_token=garbage"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		conn.CloseNow()
		t.Fatal("expected ws dial to fail with an invalid token")
	}
}

func TestRegisterDevice(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "device@pantawin.test", "Correct-Horse-42-staple")

	resp := env.do(t, http.MethodPost, "/v1/devices", tk.AccessToken, map[string]any{
		"fcm_token": "fake-fcm-token-abc123", "platform": "android",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from device register, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Re-registering the same token is idempotent (upsert), not a conflict.
	resp = env.do(t, http.MethodPost, "/v1/devices", tk.AccessToken, map[string]any{
		"fcm_token": "fake-fcm-token-abc123",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected 201 on idempotent re-register, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	var count int
	env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM devices WHERE fcm_token = 'fake-fcm-token-abc123'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 device row after re-register, got %d", count)
	}
}

func TestAlertChannelsSettableAndValidated(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "channels@pantawin.test", "Correct-Horse-42-staple")

	// Create with push+email.
	resp := env.do(t, http.MethodPost, "/v1/monitors", tk.AccessToken, map[string]any{
		"url": "https://example.com", "alert_channels": []string{"email", "push"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var created struct {
		ID            int64    `json:"id"`
		AlertChannels []string `json:"alert_channels"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if len(created.AlertChannels) != 2 {
		t.Errorf("expected 2 alert channels, got %v", created.AlertChannels)
	}

	// Invalid channel rejected.
	resp = env.do(t, http.MethodPost, "/v1/monitors", tk.AccessToken, map[string]any{
		"url": "https://example.com", "alert_channels": []string{"carrier-pigeon"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid channel, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Default when unspecified is email.
	resp = env.do(t, http.MethodPost, "/v1/monitors", tk.AccessToken, map[string]any{
		"url": "https://default.example.com",
	})
	var def struct {
		AlertChannels []string `json:"alert_channels"`
	}
	json.NewDecoder(resp.Body).Decode(&def)
	resp.Body.Close()
	if len(def.AlertChannels) != 1 || def.AlertChannels[0] != "email" {
		t.Errorf("expected default [email], got %v", def.AlertChannels)
	}
}

// TestStatsAndIncidentsEndpoints covers the M5 API surface: month/year
// periods, the tz parameter, and the incident history endpoint — including
// the ownership boundary.
func TestStatsAndIncidentsEndpoints(t *testing.T) {
	env := newTestEnv(t)
	tk := env.register(t, "m5@pantawin.test", "Correct-Horse-42-staple")

	resp := env.do(t, http.MethodPost, "/v1/monitors", tk.AccessToken, map[string]any{
		"name": "m5", "url": "https://example.com",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from create, got %d", resp.StatusCode)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Seed one resolved and one ongoing incident directly.
	var resolvedID int64
	if err := env.pool.QueryRow(context.Background(), `
		INSERT INTO incidents (monitor_id, started_at, resolved_at, cause)
		VALUES ($1, now() - interval '2 hours', now() - interval '1 hour', 'timeout') RETURNING id
	`, created.ID).Scan(&resolvedID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(context.Background(), `
		INSERT INTO incidents (monitor_id, started_at, cause)
		VALUES ($1, now() - interval '10 minutes', 'http_500')
	`, created.ID); err != nil {
		t.Fatal(err)
	}

	// All four periods work; tz is echoed back.
	for _, period := range []string{"day", "week", "month", "year"} {
		resp := env.do(t, http.MethodGet,
			fmt.Sprintf("/v1/monitors/%d/stats?period=%s&tz=Asia/Jakarta", created.ID, period),
			tk.AccessToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("period=%s: expected 200, got %d", period, resp.StatusCode)
		}
		var stats struct {
			Period string `json:"period"`
			Tz     string `json:"tz"`
		}
		json.NewDecoder(resp.Body).Decode(&stats)
		resp.Body.Close()
		if stats.Period != period || stats.Tz != "Asia/Jakarta" {
			t.Errorf("expected period=%s tz=Asia/Jakarta, got %+v", period, stats)
		}
	}

	// Bad period and bad tz are 400s.
	resp = env.do(t, http.MethodGet,
		fmt.Sprintf("/v1/monitors/%d/stats?period=quarter", created.ID), tk.AccessToken, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad period, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodGet,
		fmt.Sprintf("/v1/monitors/%d/stats?tz=Mars/Olympus", created.ID), tk.AccessToken, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for bad tz, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Incident history: newest-first, ongoing has null duration.
	resp = env.do(t, http.MethodGet,
		fmt.Sprintf("/v1/monitors/%d/incidents", created.ID), tk.AccessToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from incidents, got %d", resp.StatusCode)
	}
	var history struct {
		Incidents []struct {
			ID         int64   `json:"id"`
			ResolvedAt *string `json:"resolved_at"`
			Cause      string  `json:"cause"`
			DurationS  *int64  `json:"duration_s"`
		} `json:"incidents"`
	}
	json.NewDecoder(resp.Body).Decode(&history)
	resp.Body.Close()
	if len(history.Incidents) != 2 {
		t.Fatalf("expected 2 incidents, got %d", len(history.Incidents))
	}
	ongoing, resolved := history.Incidents[0], history.Incidents[1]
	if ongoing.ResolvedAt != nil || ongoing.DurationS != nil || ongoing.Cause != "http_500" {
		t.Errorf("ongoing incident should be first with null resolved/duration: %+v", ongoing)
	}
	if resolved.ID != resolvedID || resolved.DurationS == nil || *resolved.DurationS != 3600 {
		t.Errorf("resolved incident wrong: %+v", resolved)
	}

	// Another user can see neither stats nor incidents.
	other := env.register(t, "m5-other@pantawin.test", "Correct-Horse-42-staple")
	for _, path := range []string{"stats", "incidents"} {
		resp := env.do(t, http.MethodGet,
			fmt.Sprintf("/v1/monitors/%d/%s", created.ID, path), other.AccessToken, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: expected 404 for other user, got %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

//go:build integration

// Integration suite — needs a Docker daemon (testcontainers-go spins up
// real Postgres + Redis). Run with: go test -tags=integration ./...
// Deliberately excluded from the default `go test ./...` run so unit tests
// stay fast and don't require Docker to be available.
package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"log/slog"
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

func TestRegisterLoginAndListMonitors_EndToEnd(t *testing.T) {
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
	defer pool.Close()

	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer redisClient.Close()

	authRepo := auth.NewRepository(pool)
	issuer := auth.NewTokenIssuer("test-secret", 15*time.Minute, 30*24*time.Hour)
	authService := auth.NewService(authRepo, issuer)

	monitorRepo := monitor.NewRepository(pool)

	router := httpapi.NewRouter(authService, issuer, monitorRepo)
	server := httptest.NewServer(router)
	defer server.Close()

	// 1. Register a new user.
	registerBody := `{"email":"tester@pantawin.gratisaja.com","password":"correct horse battery staple"}`
	resp, err := http.Post(server.URL+"/v1/auth/register", "application/json", strings.NewReader(registerBody))
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 from register, got %d", resp.StatusCode)
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		t.Fatalf("failed to decode register response: %v", err)
	}
	resp.Body.Close()
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("expected non-empty access and refresh tokens from register")
	}

	// 2. GET /v1/monitors with no monitors yet -> empty array, not null.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list monitors request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from list monitors, got %d", resp.StatusCode)
	}
	var views []monitor.StatusView
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		t.Fatalf("failed to decode list monitors response: %v", err)
	}
	resp.Body.Close()
	if len(views) != 0 {
		t.Fatalf("expected 0 monitors before seeding, got %d", len(views))
	}

	// 3. Seed a monitor pointed at a local httptest target and run one
	// scheduler tick manually, then confirm it shows up as UP.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	userID, err := issuer.ParseAccessToken(tokens.AccessToken)
	if err != nil {
		t.Fatalf("failed to parse access token: %v", err)
	}
	seeded, err := monitorRepo.SeedMonitor(ctx, userID, "test target", target.URL)
	if err != nil {
		t.Fatalf("failed to seed monitor: %v", err)
	}

	chk := checker.New(2 * time.Second)
	sched := scheduler.New(redisClient, monitorRepo, chk, slog.Default())
	if err := sched.EnsureScheduled(ctx); err != nil {
		t.Fatalf("EnsureScheduled failed: %v", err)
	}

	result := chk.Check(ctx, seeded.Method, seeded.URL, seeded.ExpectedStatusMin, seeded.ExpectedStatusMax)
	if err := monitorRepo.RecordCheck(ctx, seeded.ID, result); err != nil {
		t.Fatalf("RecordCheck failed: %v", err)
	}

	req, _ = http.NewRequest(http.MethodGet, server.URL+"/v1/monitors", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("second list monitors request failed: %v", err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&views); err != nil {
		t.Fatalf("failed to decode second list monitors response: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 monitor after seeding, got %d", len(views))
	}
	if views[0].Status != monitor.StatusUp {
		t.Errorf("expected seeded monitor status UP, got %s", views[0].Status)
	}
	if views[0].LastCheckedAt == nil {
		t.Error("expected last_checked_at to be populated after a check")
	}
}

func TestListMonitors_RejectsMissingAuth(t *testing.T) {
	ctx := context.Background()
	dsn := startPostgres(t)
	if err := pgdb.Migrate(dsn, "../../migrations"); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect pool: %v", err)
	}
	defer pool.Close()

	issuer := auth.NewTokenIssuer("test-secret", 15*time.Minute, 30*24*time.Hour)
	authService := auth.NewService(auth.NewRepository(pool), issuer)
	monitorRepo := monitor.NewRepository(pool)

	router := httpapi.NewRouter(authService, issuer, monitorRepo)
	server := httptest.NewServer(router)
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/monitors")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without an Authorization header, got %d", resp.StatusCode)
	}
}

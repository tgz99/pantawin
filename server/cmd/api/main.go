// Command api boots the Pantawin backend: HTTP server + background check
// scheduler. See prd/sentrynow-spec.md section 3 for the target architecture
// (this Go server implements it in place of the spec's Kotlin/Ktor default —
// see the M0-M2 plan for why).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tgz99/pantawin/server/internal/auth"
	"github.com/tgz99/pantawin/server/internal/checker"
	"github.com/tgz99/pantawin/server/internal/config"
	pgdb "github.com/tgz99/pantawin/server/internal/db"
	"github.com/tgz99/pantawin/server/internal/httpapi"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/scheduler"
)

const migrationsDir = "migrations"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("fatal startup error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if err := pgdb.Migrate(cfg.DatabaseURL, migrationsDir); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgdb.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, DB: cfg.RedisDB})
	defer redisClient.Close()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return err
	}

	authRepo := auth.NewRepository(pool)
	if err := auth.Bootstrap(ctx, authRepo, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		return err
	}

	issuer := auth.NewTokenIssuer(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)
	authService := auth.NewService(authRepo, issuer)

	monitorRepo := monitor.NewRepository(pool)
	adminUser, err := authRepo.GetUserByEmail(ctx, cfg.AdminEmail)
	if err != nil {
		return err
	}
	if _, err := monitorRepo.SeedMonitor(ctx, adminUser.ID, cfg.SeedMonitorName, cfg.SeedMonitorURL); err != nil {
		return err
	}

	chk := checker.New(cfg.CheckTimeout)
	sched := scheduler.New(redisClient, monitorRepo, chk, logger)
	if err := sched.EnsureScheduled(ctx); err != nil {
		return err
	}
	go sched.Run(ctx)

	router := httpapi.NewRouter(authService, issuer, monitorRepo)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info("pantawin api listening", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

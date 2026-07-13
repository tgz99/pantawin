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
	"strconv"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/tgz99/pantawin/server/internal/auth"
	"github.com/tgz99/pantawin/server/internal/checker"
	"github.com/tgz99/pantawin/server/internal/config"
	pgdb "github.com/tgz99/pantawin/server/internal/db"
	"github.com/tgz99/pantawin/server/internal/httpapi"
	"github.com/tgz99/pantawin/server/internal/incident"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/notify"
	"github.com/tgz99/pantawin/server/internal/scheduler"
	"github.com/tgz99/pantawin/server/internal/ssrf"
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
	refreshStore := auth.NewRefreshStore(pool)
	authService := auth.NewService(authRepo, issuer, refreshStore, cfg.RefreshTokenTTL)

	monitorRepo := monitor.NewRepository(pool)
	adminUser, err := authRepo.GetUserByEmail(ctx, cfg.AdminEmail)
	if err != nil {
		return err
	}
	if _, err := monitorRepo.SeedMonitor(ctx, adminUser.ID, cfg.SeedMonitorName, cfg.SeedMonitorURL); err != nil {
		return err
	}

	guard := ssrf.NewGuard()
	chk := checker.New(cfg.CheckTimeout)
	sched := scheduler.New(redisClient, monitorRepo, chk, guard, logger)

	// M2: incidents + email alerts. The dispatcher consumes a Redis queue
	// so the check engine never blocks on SMTP; the scheduler's transition
	// hook opens/resolves incidents and enqueues events.
	incidentRepo := incident.NewRepository(pool)
	emailChannel := notify.NewEmailChannel(cfg.SMTPAddr, cfg.AlertFrom)
	dispatcher := notify.NewDispatcher(
		pool, redisClient, []notify.AlertChannel{emailChannel},
		func(ctx context.Context, monitorID int64) (notify.ChannelTarget, []string, error) {
			channels, email, err := monitorRepo.AlertConfig(ctx, monitorID)
			return notify.ChannelTarget{Email: email}, channels, err
		},
		logger,
	)
	go dispatcher.Run(ctx)

	sched.SetTransitionHook(func(hookCtx context.Context, m monitor.Monitor, outcome monitor.CheckOutcome, result checker.Result) {
		handleTransition(hookCtx, cfg, incidentRepo, dispatcher, m, outcome, result, logger)
	})

	if err := sched.EnsureScheduled(ctx); err != nil {
		return err
	}
	go sched.Run(ctx)

	router := httpapi.NewRouter(httpapi.RouterDeps{
		AuthService: authService,
		Issuer:      issuer,
		MonitorRepo: monitorRepo,
		Guard:       guard,
		Scheduler:   sched,
		Redis:       redisClient,
	})

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

// handleTransition maps a monitor state change to incident lifecycle +
// notification enqueue. UP->DOWN opens an incident and queues a DOWN alert;
// DOWN->UP resolves the open incident and queues a RECOVERED alert with the
// downtime. PENDING->UP (a monitor's first successful check) is a transition
// but not a recovery — there's no open incident, so nothing is sent.
func handleTransition(
	ctx context.Context,
	cfg config.Config,
	incidentRepo *incident.Repository,
	dispatcher *notify.Dispatcher,
	m monitor.Monitor,
	outcome monitor.CheckOutcome,
	result checker.Result,
	logger *slog.Logger,
) {
	deepLink := cfg.DeepLinkBase + itoa(m.ID)

	switch outcome.To {
	case monitor.StatusDown:
		cause := result.ErrorType
		if cause == "" {
			cause = "check failed"
		}
		inc, err := incidentRepo.Open(ctx, m.ID, cause)
		if err != nil {
			logger.Error("transition: failed to open incident", "monitor_id", m.ID, "error", err)
			return
		}
		event := notify.IncidentEvent{
			IncidentID: inc.ID, MonitorID: m.ID, MonitorName: m.Name, MonitorURL: m.URL,
			EventType: notify.EventDown, Cause: cause, At: time.Now(), DeepLink: deepLink,
		}
		if err := dispatcher.Enqueue(ctx, event); err != nil {
			logger.Error("transition: failed to enqueue DOWN alert", "monitor_id", m.ID, "error", err)
		}

	case monitor.StatusUp:
		inc, err := incidentRepo.Resolve(ctx, m.ID)
		if err != nil {
			// ErrNoOpenIncident on a first-check PENDING->UP is expected.
			return
		}
		var downtime time.Duration
		if inc.ResolvedAt != nil {
			downtime = inc.ResolvedAt.Sub(inc.StartedAt)
		}
		event := notify.IncidentEvent{
			IncidentID: inc.ID, MonitorID: m.ID, MonitorName: m.Name, MonitorURL: m.URL,
			EventType: notify.EventRecovered, At: time.Now(), DownDuration: downtime, DeepLink: deepLink,
		}
		if err := dispatcher.Enqueue(ctx, event); err != nil {
			logger.Error("transition: failed to enqueue RECOVERED alert", "monitor_id", m.ID, "error", err)
		}
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

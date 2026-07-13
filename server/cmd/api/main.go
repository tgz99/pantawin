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
	"github.com/tgz99/pantawin/server/internal/device"
	"github.com/tgz99/pantawin/server/internal/httpapi"
	"github.com/tgz99/pantawin/server/internal/incident"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/notify"
	"github.com/tgz99/pantawin/server/internal/realtime"
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

	deviceRepo := device.NewRepository(pool)
	guard := ssrf.NewGuard()
	chk := checker.New(cfg.CheckTimeout)
	sched := scheduler.New(redisClient, monitorRepo, chk, guard, logger)

	// M2: incidents + notifications. The dispatcher consumes a Redis queue
	// so the check engine never blocks on SMTP/HTTP delivery.
	incidentRepo := incident.NewRepository(pool)
	channels := []notify.AlertChannel{notify.NewEmailChannel(cfg.SMTPAddr, cfg.AlertFrom)}

	// M3: FCM push channel — only registered when credentials are configured.
	// Dormant otherwise; monitors requesting "push" are then skipped cleanly.
	fcmCreds := loadFCMCredentials(cfg, logger)
	fcmChannel, err := notify.NewFcmChannel(ctx, fcmCreds, cfg.FCMProjectID,
		func(ctx context.Context, monitorID int64) (int64, []string, error) {
			m, err := monitorRepo.GetByID(ctx, monitorID)
			if err != nil {
				return 0, nil, err
			}
			tokens, err := deviceRepo.TokensForUser(ctx, m.UserID)
			return m.UserID, tokens, err
		},
		func(ctx context.Context, token string) { _ = deviceRepo.Delete(ctx, token) },
	)
	if err != nil {
		return err
	}
	if fcmChannel != nil {
		channels = append(channels, fcmChannel)
		logger.Info("fcm push channel enabled", "project", cfg.FCMProjectID)
	} else {
		logger.Info("fcm push channel dormant (no credentials configured)")
	}

	dispatcher := notify.NewDispatcher(
		pool, redisClient, channels,
		func(ctx context.Context, monitorID int64) (notify.ChannelTarget, []string, error) {
			ch, email, err := monitorRepo.AlertConfig(ctx, monitorID)
			return notify.ChannelTarget{Email: email}, ch, err
		},
		logger,
	)
	go dispatcher.Run(ctx)

	// M3: realtime WebSocket feed.
	publisher := realtime.NewPublisher(redisClient)
	wsHandler := realtime.NewHandler(redisClient, logger)

	sched.SetAfterCheckHook(func(hookCtx context.Context, m monitor.Monitor, outcome monitor.CheckOutcome, result checker.Result) {
		// Live status on every check (M3 dashboard).
		publishStatus(hookCtx, publisher, m, result, logger)
		// Incidents + persistent notifications only on transitions (M2).
		if outcome.Transitioned {
			handleTransition(hookCtx, cfg, incidentRepo, dispatcher, publisher, m, outcome, result, logger)
		}
	})

	if err := sched.EnsureScheduled(ctx); err != nil {
		return err
	}
	go sched.Run(ctx)

	router := httpapi.NewRouter(httpapi.RouterDeps{
		AuthService: authService,
		Issuer:      issuer,
		MonitorRepo: monitorRepo,
		DeviceRepo:  deviceRepo,
		Guard:       guard,
		Scheduler:   sched,
		Realtime:    wsHandler,
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
	publisher *realtime.Publisher,
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
		publishIncident(ctx, publisher, m, "DOWN", logger)

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
		publishIncident(ctx, publisher, m, "RECOVERED", logger)
	}
}

// publishStatus pushes a live status event to the owner's WebSocket feed on
// every check (spec 6.4 — foreground dashboard updates instantly).
func publishStatus(ctx context.Context, publisher *realtime.Publisher, m monitor.Monitor, result checker.Result, logger *slog.Logger) {
	status := string(monitor.StatusUp)
	if !result.OK {
		status = string(monitor.StatusDown)
	}
	var rt *int
	if result.ResponseTimeMS != 0 {
		v := int(result.ResponseTimeMS)
		rt = &v
	}
	evt := realtime.Event{
		Type: "status", MonitorID: m.ID, MonitorName: m.Name,
		Status: status, ResponseTimeMS: rt, At: time.Now().UTC().Format(time.RFC3339),
	}
	if err := publisher.Publish(ctx, m.UserID, evt); err != nil {
		logger.Debug("realtime: failed to publish status", "monitor_id", m.ID, "error", err)
	}
}

func publishIncident(ctx context.Context, publisher *realtime.Publisher, m monitor.Monitor, incidentEvent string, logger *slog.Logger) {
	status := string(monitor.StatusDown)
	if incidentEvent == "RECOVERED" {
		status = string(monitor.StatusUp)
	}
	evt := realtime.Event{
		Type: "incident", MonitorID: m.ID, MonitorName: m.Name,
		Status: status, IncidentEvent: incidentEvent, At: time.Now().UTC().Format(time.RFC3339),
	}
	if err := publisher.Publish(ctx, m.UserID, evt); err != nil {
		logger.Debug("realtime: failed to publish incident", "monitor_id", m.ID, "error", err)
	}
}

// loadFCMCredentials reads the service-account JSON from the configured file
// or inline env var. Returns nil when neither is set (push stays dormant).
func loadFCMCredentials(cfg config.Config, logger *slog.Logger) []byte {
	if cfg.FCMCredentialsJSON != "" {
		return []byte(cfg.FCMCredentialsJSON)
	}
	if cfg.FCMCredentialsFile != "" {
		data, err := os.ReadFile(cfg.FCMCredentialsFile)
		if err != nil {
			logger.Error("failed to read FCM credentials file; push stays dormant", "path", cfg.FCMCredentialsFile, "error", err)
			return nil
		}
		return data
	}
	return nil
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

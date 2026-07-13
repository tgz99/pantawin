// Package scheduler drives the check loop off a Redis sorted set keyed by
// next-run epoch (spec section 3.2). Each monitor's next run is independent
// state in the sorted set, so a slow or timing-out monitor never delays
// another's checks (spec 7.2 item 6).
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tgz99/pantawin/server/internal/checker"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/ssrf"
)

const scheduleKey = "pantawin:checks:schedule"

// AfterCheckHook fires after EVERY recorded check (not only on transitions).
// The handler publishes a live WebSocket status update for every check (M3
// realtime dashboard) and, when outcome.Transitioned is true, drives the
// M2 incident/notification pipeline.
type AfterCheckHook func(ctx context.Context, m monitor.Monitor, outcome monitor.CheckOutcome, result checker.Result)

type Scheduler struct {
	redis        *redis.Client
	repo         *monitor.Repository
	checker      *checker.Checker
	guard        *ssrf.Guard
	afterCheck   AfterCheckHook
	tickInterval time.Duration
	logger       *slog.Logger
}

func New(redisClient *redis.Client, repo *monitor.Repository, chk *checker.Checker, guard *ssrf.Guard, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		redis:        redisClient,
		repo:         repo,
		checker:      chk,
		guard:        guard,
		tickInterval: 1 * time.Second,
		logger:       logger,
	}
}

// SetAfterCheckHook installs the post-check hook (realtime + notifications).
// Must be called before Run.
func (s *Scheduler) SetAfterCheckHook(hook AfterCheckHook) {
	s.afterCheck = hook
}

// Schedule (re)queues a monitor for its next check at now + delay. Called
// on creation (delay 0 = check immediately), on resume, and internally
// after every completed check.
func (s *Scheduler) Schedule(ctx context.Context, monitorID int64, delay time.Duration) error {
	return s.redis.ZAdd(ctx, scheduleKey, redis.Z{
		Score:  float64(time.Now().Add(delay).Unix()),
		Member: strconv.FormatInt(monitorID, 10),
	}).Err()
}

// Unschedule removes a monitor from the queue — used by pause and delete.
// (Defense in depth: processMonitor also drops paused/missing monitors.)
func (s *Scheduler) Unschedule(ctx context.Context, monitorID int64) error {
	return s.redis.ZRem(ctx, scheduleKey, strconv.FormatInt(monitorID, 10)).Err()
}

// EnsureScheduled seeds the Redis sorted set with every active (non-PAUSED)
// monitor on startup, so a fresh Redis instance (or one that lost its data)
// doesn't leave monitors permanently unchecked. Uses NX so a monitor already
// scheduled — e.g. a container restart while checks are in flight — doesn't
// have its next-run time clobbered back to "now".
func (s *Scheduler) EnsureScheduled(ctx context.Context) error {
	monitors, err := s.repo.ListAll(ctx)
	if err != nil {
		return err
	}
	for _, m := range monitors {
		member := strconv.FormatInt(m.ID, 10)
		if err := s.redis.ZAddNX(ctx, scheduleKey, redis.Z{
			Score:  float64(time.Now().Unix()),
			Member: member,
		}).Err(); err != nil {
			return err
		}
	}
	return nil
}

// Run blocks, ticking once per second until ctx is cancelled. Each tick
// pops every monitor whose next-run score has elapsed and checks it in its
// own goroutine.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	now := strconv.FormatInt(time.Now().Unix(), 10)

	due, err := s.redis.ZRangeByScore(ctx, scheduleKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: now,
	}).Result()
	if err != nil {
		s.logger.Error("scheduler: failed to query due checks", "error", err)
		return
	}

	for _, member := range due {
		// ZRem returns 0 if another process already claimed this member
		// between the ZRangeByScore read and now — skip rather than
		// double-check. Single-instance at M1, but this makes the queue
		// safe to scale out later without a rework.
		removed, err := s.redis.ZRem(ctx, scheduleKey, member).Result()
		if err != nil {
			s.logger.Error("scheduler: failed to claim due check", "monitor_id", member, "error", err)
			continue
		}
		if removed == 0 {
			continue
		}

		go s.processMonitor(ctx, member)
	}
}

func (s *Scheduler) processMonitor(ctx context.Context, monitorIDStr string) {
	id, err := strconv.ParseInt(monitorIDStr, 10, 64)
	if err != nil {
		s.logger.Error("scheduler: invalid monitor id in schedule", "raw", monitorIDStr, "error", err)
		return
	}

	m, err := s.repo.GetByID(ctx, id)
	if err != nil {
		// Deleted monitors simply drop out of the schedule — not an error
		// worth alerting on, just debug noise.
		if strings.Contains(err.Error(), "no rows") {
			s.logger.Debug("scheduler: monitor no longer exists, dropping", "monitor_id", id)
			return
		}
		s.logger.Error("scheduler: failed to load monitor", "monitor_id", id, "error", err)
		// Transient DB error: reschedule so the monitor isn't silently lost.
		if rescheduleErr := s.Schedule(ctx, id, 30*time.Second); rescheduleErr != nil {
			s.logger.Error("scheduler: failed to reschedule after load error", "monitor_id", id, "error", rescheduleErr)
		}
		return
	}

	if m.Status == monitor.StatusPaused {
		return // dropped, not rescheduled — resume re-adds it
	}

	// Re-validate the target on EVERY check, not just at creation — this is
	// the DNS-rebinding defense (spec 7.2 item 4). A monitor whose DNS now
	// points somewhere forbidden records a failed check rather than probing
	// internal infrastructure.
	var result checker.Result
	if err := s.guard.Validate(ctx, m.URL); err != nil {
		if errors.Is(err, ssrf.ErrForbiddenTarget) {
			s.logger.Warn("scheduler: monitor target now resolves to a forbidden address", "monitor_id", m.ID, "url", m.URL)
			result = checker.Result{OK: false, ErrorType: checker.ErrorTypeDNS}
		} else {
			// Plain resolution failure — the checker would classify this
			// as a DNS error anyway; record it as such without probing.
			result = checker.Result{OK: false, ErrorType: checker.ErrorTypeDNS}
		}
	} else {
		checkCtx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutMS)*time.Millisecond)
		result = s.checker.Check(checkCtx, m.Method, m.URL, m.ExpectedStatusMin, m.ExpectedStatusMax)
		cancel()
	}

	outcome, err := s.repo.RecordCheck(ctx, m.ID, result)
	if err != nil {
		s.logger.Error("scheduler: failed to record check result", "monitor_id", m.ID, "error", err)
	} else if s.afterCheck != nil {
		// Fires on every check: the handler publishes a live WS status
		// update, and handles incidents/notifications when transitioned.
		s.afterCheck(ctx, m, outcome, result)
	}

	if err := s.Schedule(ctx, m.ID, time.Duration(m.IntervalSeconds)*time.Second); err != nil {
		s.logger.Error("scheduler: failed to reschedule monitor", "monitor_id", m.ID, "error", err)
	}
}

// Package scheduler drives the check loop off a Redis sorted set keyed by
// next-run epoch (spec section 3.2). Standing up the real queue at M0 —
// even with a single monitor — avoids reworking the scheduling algorithm
// when M1 introduces multiple monitors with independent intervals; a slow
// monitor's check will never block another's, since each monitor's next
// run is independent state in the sorted set rather than a shared loop.
package scheduler

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tgz99/pantawin/server/internal/checker"
	"github.com/tgz99/pantawin/server/internal/monitor"
)

const scheduleKey = "pantawin:checks:schedule"

type Scheduler struct {
	redis        *redis.Client
	repo         *monitor.Repository
	checker      *checker.Checker
	tickInterval time.Duration
	logger       *slog.Logger
}

func New(redisClient *redis.Client, repo *monitor.Repository, chk *checker.Checker, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		redis:        redisClient,
		repo:         repo,
		checker:      chk,
		tickInterval: 1 * time.Second,
		logger:       logger,
	}
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
		// double-check. Single-instance at M0, but this makes the queue
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
		s.logger.Error("scheduler: failed to load monitor", "monitor_id", id, "error", err)
		return
	}

	if m.Status == monitor.StatusPaused {
		return // dropped, not rescheduled — resuming re-adds it (M1)
	}

	checkCtx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutMS)*time.Millisecond)
	defer cancel()

	result := s.checker.Check(checkCtx, m.Method, m.URL, m.ExpectedStatusMin, m.ExpectedStatusMax)

	if err := s.repo.RecordCheck(ctx, m.ID, result); err != nil {
		s.logger.Error("scheduler: failed to record check result", "monitor_id", m.ID, "error", err)
	}

	nextRun := time.Now().Add(time.Duration(m.IntervalSeconds) * time.Second).Unix()
	if err := s.redis.ZAdd(ctx, scheduleKey, redis.Z{
		Score:  float64(nextRun),
		Member: monitorIDStr,
	}).Err(); err != nil {
		s.logger.Error("scheduler: failed to reschedule monitor", "monitor_id", m.ID, "error", err)
	}
}

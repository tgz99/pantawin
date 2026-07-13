package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const queueKey = "pantawin:notify:queue"

// Dispatcher fans incident events out to channels, using notification_log
// as the idempotency ledger (spec 7.2 item 5). Events are queued in Redis
// so the check engine never blocks on SMTP I/O and a pending notification
// survives a process restart.
type Dispatcher struct {
	pool     *pgxpool.Pool
	redis    *redis.Client
	channels map[string]AlertChannel
	logger   *slog.Logger
	// resolveTarget maps a monitor to its delivery address (owner email or
	// per-monitor override); injected so notify doesn't import monitor.
	resolveTarget func(ctx context.Context, monitorID int64) (ChannelTarget, []string, error)
}

func NewDispatcher(
	pool *pgxpool.Pool,
	redisClient *redis.Client,
	channels []AlertChannel,
	resolveTarget func(ctx context.Context, monitorID int64) (ChannelTarget, []string, error),
	logger *slog.Logger,
) *Dispatcher {
	byName := make(map[string]AlertChannel, len(channels))
	for _, c := range channels {
		byName[c.Name()] = c
	}
	return &Dispatcher{
		pool: pool, redis: redisClient, channels: byName,
		resolveTarget: resolveTarget, logger: logger,
	}
}

// Enqueue pushes an event onto the Redis queue. Called synchronously from
// the scheduler's transition hook — a fast LPUSH, no I/O to the channel.
func (d *Dispatcher) Enqueue(ctx context.Context, event IncidentEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return d.redis.LPush(ctx, queueKey, payload).Err()
}

// Run consumes the queue until ctx is cancelled. Single worker at M2 volume;
// the claim-based idempotency makes it safe to run several later.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// BRPOP blocks up to 5s so shutdown stays responsive.
		res, err := d.redis.BRPop(ctx, 5*time.Second, queueKey).Result()
		if errors.Is(err, redis.Nil) {
			continue // timed out, no item
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			d.logger.Error("notify: queue pop failed", "error", err)
			time.Sleep(time.Second)
			continue
		}
		// res is [key, value].
		var event IncidentEvent
		if err := json.Unmarshal([]byte(res[1]), &event); err != nil {
			d.logger.Error("notify: bad queue payload", "error", err)
			continue
		}
		d.deliver(ctx, event)
	}
}

func (d *Dispatcher) deliver(ctx context.Context, event IncidentEvent) {
	target, channelNames, err := d.resolveTarget(ctx, event.MonitorID)
	if err != nil {
		d.logger.Error("notify: failed to resolve target", "monitor_id", event.MonitorID, "error", err)
		return
	}

	for _, chName := range channelNames {
		channel, ok := d.channels[chName]
		if !ok {
			continue // unknown/disabled channel (e.g. "push" before M3)
		}
		d.deliverOne(ctx, channel, event, target)
	}
}

// deliverOne implements claim -> send -> confirm. The INSERT ... ON CONFLICT
// DO NOTHING RETURNING id is the claim: if it returns no row, another worker
// (or a prior delivery of the same event) already handled this
// (incident, channel, event_type), so we skip. This makes "exactly one
// notification per transition" a database-level guarantee, not a convention.
func (d *Dispatcher) deliverOne(ctx context.Context, channel AlertChannel, event IncidentEvent, target ChannelTarget) {
	var logID int64
	err := d.pool.QueryRow(ctx, `
		INSERT INTO notification_log (incident_id, channel, event_type)
		VALUES ($1, $2, $3)
		ON CONFLICT (incident_id, channel, event_type) DO NOTHING
		RETURNING id
	`, event.IncidentID, channel.Name(), string(event.EventType)).Scan(&logID)

	if errors.Is(err, pgx.ErrNoRows) {
		// Already claimed — exactly-once holds.
		return
	}
	if err != nil {
		d.logger.Error("notify: failed to claim notification", "incident_id", event.IncidentID, "channel", channel.Name(), "error", err)
		return
	}

	sendErr := channel.Send(ctx, event, target)
	ok := sendErr == nil

	if _, err := d.pool.Exec(ctx, `
		UPDATE notification_log SET ok = $1, sent_at = now() WHERE id = $2
	`, ok, logID); err != nil {
		d.logger.Error("notify: failed to confirm notification", "id", logID, "error", err)
	}

	if sendErr != nil {
		// Delivery failed — the claim row stays (ok=false), so we don't
		// retry-spam. At M2 a failed send is logged and surfaced via the
		// log table; a retry policy is a fast-follow if deliverability
		// proves flaky.
		d.logger.Error("notify: channel send failed", "channel", channel.Name(), "incident_id", event.IncidentID, "error", sendErr)
	} else {
		d.logger.Info("notify: sent", "channel", channel.Name(), "event", event.EventType, "monitor", event.MonitorName)
	}
}

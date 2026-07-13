package notify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const queueKey = "pantawin:notify:queue"

// Retry policy for failed sends: after the initial attempt fails, the
// retrier re-sends with exponential backoff (base x 4 per attempt:
// 1m, 4m, 16m) up to maxSendAttempts total sends. The ~21-minute span is
// sized to outlast a transient network outage on the VPS without keeping
// stale alerts alive for hours.
const (
	maxSendAttempts = 4
	retryBaseDelay  = time.Minute
	retryPollEvery  = 30 * time.Second
	retryBatchLimit = 20
)

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
	// Retry cadence — the constants above in production, shrunk in tests.
	retryPoll time.Duration
	retryBase time.Duration
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
		retryPoll: retryPollEvery, retryBase: retryBaseDelay,
	}
}

// WithRetryTuning overrides the retry poll interval and backoff base.
// Tests only — production uses the package defaults.
func (d *Dispatcher) WithRetryTuning(poll, base time.Duration) *Dispatcher {
	d.retryPoll = poll
	d.retryBase = base
	return d
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

	// Channels deliver concurrently so one slow/broken transport (e.g. an
	// SMTP relay that is down) never delays the others. deliverOne is
	// self-contained: each goroutine claims, sends, and confirms its own
	// (incident, channel, event_type) row.
	var wg sync.WaitGroup
	for _, chName := range channelNames {
		channel, ok := d.channels[chName]
		if !ok {
			continue // unknown/disabled channel (e.g. "push" before M3)
		}
		wg.Add(1)
		go func(ch AlertChannel) {
			defer wg.Done()
			d.deliverOne(ctx, ch, event, target)
		}(channel)
	}
	wg.Wait()
}

// deliverOne implements claim -> send -> confirm. The INSERT ... ON CONFLICT
// DO NOTHING RETURNING id is the claim: if it returns no row, another worker
// (or a prior delivery of the same event) already handled this
// (incident, channel, event_type), so we skip. This makes "exactly one
// notification per transition" a database-level guarantee, not a convention.
func (d *Dispatcher) deliverOne(ctx context.Context, channel AlertChannel, event IncidentEvent, target ChannelTarget) {
	// The event JSON rides along on the claim so the retrier can re-send
	// after a failure without reconstructing the event.
	payload, err := json.Marshal(event)
	if err != nil {
		d.logger.Error("notify: failed to marshal event payload", "incident_id", event.IncidentID, "error", err)
		payload = nil
	}

	var logID int64
	err = d.pool.QueryRow(ctx, `
		INSERT INTO notification_log (incident_id, channel, event_type, payload)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (incident_id, channel, event_type) DO NOTHING
		RETURNING id
	`, event.IncidentID, channel.Name(), string(event.EventType), payload).Scan(&logID)

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
		// Delivery failed — the claim row stays (ok=false, attempts=1) and
		// RunRetrier picks it up with backoff. The claim still guarantees
		// no duplicate sends of a SUCCESSFUL delivery.
		d.logger.Error("notify: channel send failed", "channel", channel.Name(), "incident_id", event.IncidentID, "error", sendErr)
	} else {
		d.logger.Info("notify: sent", "channel", channel.Name(), "event", event.EventType, "monitor", event.MonitorName)
	}
}

// RunRetrier re-sends failed notifications until ctx is cancelled. State
// lives entirely in notification_log (ok=false rows carry the event payload
// and an attempts counter), so retries survive a process restart and the
// optimistic attempts bump keeps this safe if several workers ever run.
func (d *Dispatcher) RunRetrier(ctx context.Context) {
	ticker := time.NewTicker(d.retryPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.retryFailed(ctx)
		}
	}
}

// retryFailed sweeps one batch of due failures: rows with ok=false whose
// backoff window (base x 4^(attempts-1) since the last attempt) has elapsed
// and that haven't hit maxSendAttempts. Rows without a payload (pre-retry
// migrations) are never picked up.
func (d *Dispatcher) retryFailed(ctx context.Context) {
	rows, err := d.pool.Query(ctx, `
		SELECT id, channel, attempts, payload
		FROM notification_log
		WHERE ok = false
		  AND payload IS NOT NULL
		  AND attempts < $1
		  AND sent_at + make_interval(secs => $2 * pow(4, attempts - 1)) < now()
		ORDER BY sent_at
		LIMIT $3
	`, maxSendAttempts, d.retryBase.Seconds(), retryBatchLimit)
	if err != nil {
		d.logger.Error("notify: retry query failed", "error", err)
		return
	}
	type due struct {
		id       int64
		chName   string
		attempts int
		payload  []byte
	}
	var batch []due
	for rows.Next() {
		var r due
		if err := rows.Scan(&r.id, &r.chName, &r.attempts, &r.payload); err != nil {
			d.logger.Error("notify: retry scan failed", "error", err)
			rows.Close()
			return
		}
		batch = append(batch, r)
	}
	rows.Close()

	for _, r := range batch {
		d.retryOne(ctx, r.id, r.chName, r.attempts, r.payload)
	}
}

func (d *Dispatcher) retryOne(ctx context.Context, logID int64, chName string, attempts int, payload []byte) {
	channel, ok := d.channels[chName]
	if !ok {
		// Channel not registered this boot (e.g. push while FCM is dormant);
		// leave the row for a boot that has it.
		return
	}

	// Optimistic claim: bump attempts only if nobody else has. Losing the
	// race (or the row flipping to ok=true) means another worker took it.
	tag, err := d.pool.Exec(ctx, `
		UPDATE notification_log SET attempts = attempts + 1
		WHERE id = $1 AND attempts = $2 AND ok = false
	`, logID, attempts)
	if err != nil || tag.RowsAffected() == 0 {
		if err != nil {
			d.logger.Error("notify: retry claim failed", "id", logID, "error", err)
		}
		return
	}

	var event IncidentEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		d.logger.Error("notify: retry payload unmarshal failed", "id", logID, "error", err)
		return
	}

	// Re-resolve at retry time: the target address or the monitor's channel
	// selection may have changed since the original claim.
	target, channelNames, err := d.resolveTarget(ctx, event.MonitorID)
	if err != nil {
		d.logger.Error("notify: retry failed to resolve target", "id", logID, "monitor_id", event.MonitorID, "error", err)
		return
	}
	if !slices.Contains(channelNames, chName) {
		// Owner disabled this channel meanwhile — the bumped attempts
		// counter walks the row to the cap and it quietly expires.
		return
	}

	sendErr := channel.Send(ctx, event, target)
	if _, err := d.pool.Exec(ctx, `
		UPDATE notification_log SET ok = $1, sent_at = now() WHERE id = $2
	`, sendErr == nil, logID); err != nil {
		d.logger.Error("notify: failed to confirm retry", "id", logID, "error", err)
	}

	if sendErr != nil {
		d.logger.Error("notify: retry send failed", "channel", chName, "incident_id", event.IncidentID, "attempt", attempts+1, "error", sendErr)
	} else {
		d.logger.Info("notify: sent on retry", "channel", chName, "event", event.EventType, "monitor", event.MonitorName, "attempt", attempts+1)
	}
}

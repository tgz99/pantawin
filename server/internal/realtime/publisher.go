// Package realtime provides the WebSocket live feed (spec sections 3.1,
// 6.4). Events are fanned out through Redis pub/sub so the check engine and
// the HTTP/WS servers stay decoupled and the design scales past one process.
package realtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Event is what a connected dashboard receives. "status" fires on every
// check (live status/latency); "incident" fires on a DOWN/RECOVERED
// transition.
type Event struct {
	Type           string `json:"type"` // "status" | "incident"
	MonitorID      int64  `json:"monitor_id"`
	MonitorName    string `json:"monitor_name,omitempty"`
	Status         string `json:"status"`
	ResponseTimeMS *int   `json:"response_time_ms,omitempty"`
	IncidentEvent  string `json:"incident_event,omitempty"` // "DOWN" | "RECOVERED"
	At             string `json:"at"`
}

// userChannel is the Redis pub/sub channel a user's dashboard subscribes to.
func userChannel(userID int64) string {
	return fmt.Sprintf("pantawin:ws:user:%d", userID)
}

// teamChannel carries events for team-scoped monitors (M6); every connected
// dashboard subscribes to it in addition to its own user channel.
const teamChannel = "pantawin:ws:team"

type Publisher struct {
	redis *redis.Client
}

func NewPublisher(redisClient *redis.Client) *Publisher {
	return &Publisher{redis: redisClient}
}

func (p *Publisher) Publish(ctx context.Context, userID int64, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return p.redis.Publish(ctx, userChannel(userID), payload).Err()
}

// PublishTeam broadcasts an event for a team-scoped monitor to every
// connected dashboard.
func (p *Publisher) PublishTeam(ctx context.Context, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return p.redis.Publish(ctx, teamChannel, payload).Err()
}

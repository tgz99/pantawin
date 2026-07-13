// Package notify implements the multi-channel alert dispatcher (spec section
// 3.2b). v1 has one channel, EmailChannel; the interface exists so v2
// channels (WhatsApp, Telegram, push) slot in without touching the engine.
package notify

import (
	"context"
	"time"
)

type EventType string

const (
	EventDown      EventType = "DOWN"
	EventRecovered EventType = "RECOVERED"
)

// IncidentEvent is the channel-agnostic payload a transition produces.
type IncidentEvent struct {
	IncidentID   int64
	MonitorID    int64
	MonitorName  string
	MonitorURL   string
	EventType    EventType
	Cause        string        // error_type on DOWN
	At           time.Time
	DownDuration time.Duration // populated on RECOVERED
	DeepLink     string        // pantawin://monitor/{id}
}

// ChannelTarget is where a channel delivers — for email, the address.
type ChannelTarget struct {
	Email string
}

// AlertChannel is the pluggable delivery abstraction (spec 3.2b).
type AlertChannel interface {
	// Name is the channel identifier stored in notification_log.
	Name() string
	Send(ctx context.Context, event IncidentEvent, target ChannelTarget) error
}

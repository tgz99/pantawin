// Package monitor holds monitor records and their check-result history.
//
// M0 status handling is intentionally trivial (each check result directly
// sets UP/DOWN) — the real failure_threshold-confirmed state machine
// (spec section 3.2 / 7.2 item 1) lands in M1 without needing a schema
// change, since the `status` and `consecutive_failures` columns already
// exist from the M0 migration.
package monitor

import "time"

type Status string

const (
	StatusUp      Status = "UP"
	StatusDown    Status = "DOWN"
	StatusPaused  Status = "PAUSED"
	StatusPending Status = "PENDING"
)

// Scope: personal monitors belong to their owner; team monitors (M6, teams
// made plural and self-service in M6.3) belong to one specific team and are
// visible to / alert every member of that team.
const (
	ScopePersonal = "personal"
	ScopeTeam     = "team"
)

func ValidScope(s string) bool { return s == ScopePersonal || s == ScopeTeam }

type Monitor struct {
	ID                  int64
	UserID              int64
	Name                string
	URL                 string
	Method              string
	IntervalSeconds     int
	TimeoutMS           int
	ExpectedStatusMin   int
	ExpectedStatusMax   int
	FailureThreshold    int
	Status              Status
	ConsecutiveFailures int
	AlertChannels       []string
	Scope               string
	TeamID              *int64 // set iff Scope == ScopeTeam
	CreatedAt           time.Time
}

// StatusView is the API-facing shape for GET /v1/monitors (spec section 4).
type StatusView struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	URL            string     `json:"url"`
	Status         Status     `json:"status"`
	Scope          string     `json:"scope"`
	TeamID         *int64     `json:"team_id"`
	TeamName       *string    `json:"team_name"`
	LastCheckedAt  *time.Time `json:"last_checked_at"`
	ResponseTimeMS *int       `json:"response_time_ms"`
}

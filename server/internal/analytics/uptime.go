// Package analytics implements the uptime math and stats rollups (spec 3.3).
// Uptime is computed from incident DURATIONS, not naive check counts: a
// 90-second outage between two successful checks still counts as 90 seconds
// of downtime.
package analytics

import "time"

// Window is a half-open UTC interval [Start, End).
type Window struct {
	Start time.Time
	End   time.Time
}

func (w Window) Seconds() float64 {
	if w.End.Before(w.Start) {
		return 0
	}
	return w.End.Sub(w.Start).Seconds()
}

// Span is an incident interval. End == nil means the incident is ongoing.
type Span struct {
	Start time.Time
	End   *time.Time
}

// DownSeconds returns how many seconds of the window are covered by the
// given incident spans. Spans are clipped to the window; an ongoing span
// (End == nil) is treated as extending to `now` (then clipped). Spans are
// assumed non-overlapping — the state machine allows at most one open
// incident per monitor at a time — but each is clipped independently, so
// accidental overlap can only over-count, never crash.
func DownSeconds(w Window, spans []Span, now time.Time) float64 {
	var total float64
	for _, s := range spans {
		end := now
		if s.End != nil {
			end = *s.End
		}
		start := s.Start
		if start.Before(w.Start) {
			start = w.Start
		}
		if end.After(w.End) {
			end = w.End
		}
		if end.After(start) {
			total += end.Sub(start).Seconds()
		}
	}
	if max := w.Seconds(); total > max {
		total = max
	}
	return total
}

// UptimePct returns 1 - down/monitored as a percentage, or nil when
// monitoredSeconds is zero (paused / not yet created — "no data", which is
// deliberately distinct from 0%). Downtime is capped at the monitored time.
func UptimePct(monitoredSeconds, downSeconds float64) *float64 {
	if monitoredSeconds <= 0 {
		return nil
	}
	if downSeconds > monitoredSeconds {
		downSeconds = monitoredSeconds
	}
	pct := (1 - downSeconds/monitoredSeconds) * 100
	return &pct
}

// HourStart truncates t to the start of its UTC hour.
func HourStart(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour)
}

// DayStart truncates t to the start of its UTC day.
func DayStart(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

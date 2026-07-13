package analytics

import (
	"testing"
	"time"
)

// The M4 exit-criteria boundary suite (spec section 9 item 2): incident
// spanning midnight, ongoing incident, paused windows, plus clipping edges.

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func day(t *testing.T, date string) Window {
	start := mustTime(t, date+"T00:00:00Z")
	return Window{Start: start, End: start.Add(24 * time.Hour)}
}

func TestDownSeconds_IncidentSpanningMidnight(t *testing.T) {
	// Incident 23:30 day 1 -> 00:30 day 2: exactly 1800s in each day.
	start := mustTime(t, "2026-07-01T23:30:00Z")
	end := mustTime(t, "2026-07-02T00:30:00Z")
	spans := []Span{{Start: start, End: &end}}
	now := mustTime(t, "2026-07-03T00:00:00Z")

	if got := DownSeconds(day(t, "2026-07-01"), spans, now); got != 1800 {
		t.Errorf("day 1: expected 1800s down, got %v", got)
	}
	if got := DownSeconds(day(t, "2026-07-02"), spans, now); got != 1800 {
		t.Errorf("day 2: expected 1800s down, got %v", got)
	}
	// A day the incident doesn't touch.
	if got := DownSeconds(day(t, "2026-07-03"), spans, now); got != 0 {
		t.Errorf("day 3: expected 0s down, got %v", got)
	}
}

func TestDownSeconds_OngoingIncident(t *testing.T) {
	// Unresolved incident extends to `now`, not to infinity.
	start := mustTime(t, "2026-07-01T10:00:00Z")
	spans := []Span{{Start: start, End: nil}}
	now := mustTime(t, "2026-07-01T12:00:00Z")

	if got := DownSeconds(day(t, "2026-07-01"), spans, now); got != 7200 {
		t.Errorf("expected 7200s down for 2h ongoing incident, got %v", got)
	}

	// Ongoing incident that started before the window covers the window
	// up to now.
	w := Window{Start: mustTime(t, "2026-07-01T11:00:00Z"), End: mustTime(t, "2026-07-01T13:00:00Z")}
	if got := DownSeconds(w, spans, now); got != 3600 {
		t.Errorf("expected 3600s down (11:00->now), got %v", got)
	}
}

func TestDownSeconds_IncidentOutsideWindow(t *testing.T) {
	start := mustTime(t, "2026-07-01T10:00:00Z")
	end := mustTime(t, "2026-07-01T11:00:00Z")
	spans := []Span{{Start: start, End: &end}}
	now := mustTime(t, "2026-07-05T00:00:00Z")

	if got := DownSeconds(day(t, "2026-07-02"), spans, now); got != 0 {
		t.Errorf("expected 0s down for incident entirely outside window, got %v", got)
	}
}

func TestDownSeconds_IncidentCoveringWholeWindow(t *testing.T) {
	start := mustTime(t, "2026-06-30T00:00:00Z")
	end := mustTime(t, "2026-07-03T00:00:00Z")
	spans := []Span{{Start: start, End: &end}}
	now := end

	if got := DownSeconds(day(t, "2026-07-01"), spans, now); got != 86400 {
		t.Errorf("expected full 86400s down, got %v", got)
	}
}

func TestDownSeconds_MultipleIncidentsSum(t *testing.T) {
	e1 := mustTime(t, "2026-07-01T01:10:00Z")
	e2 := mustTime(t, "2026-07-01T09:05:00Z")
	spans := []Span{
		{Start: mustTime(t, "2026-07-01T01:00:00Z"), End: &e1}, // 600s
		{Start: mustTime(t, "2026-07-01T09:00:00Z"), End: &e2}, // 300s
	}
	now := mustTime(t, "2026-07-02T00:00:00Z")
	if got := DownSeconds(day(t, "2026-07-01"), spans, now); got != 900 {
		t.Errorf("expected 900s total down, got %v", got)
	}
}

func TestUptimePct_PausedWindowIsNoData(t *testing.T) {
	// Zero monitored seconds (paused all bucket) -> nil, NOT 0% or 100%.
	if got := UptimePct(0, 0); got != nil {
		t.Errorf("expected nil uptime for zero monitored time, got %v", *got)
	}
}

func TestUptimePct_PartiallyPausedWindow(t *testing.T) {
	// Monitor active 6h of a day (paused the rest), down 36min of those 6h:
	// uptime = 1 - 2160/21600 = 90%. The paused 18h is excluded entirely.
	got := UptimePct(6*3600, 2160)
	if got == nil {
		t.Fatal("expected uptime value")
	}
	if *got < 89.999 || *got > 90.001 {
		t.Errorf("expected 90%%, got %v", *got)
	}
}

func TestUptimePct_FullUptime(t *testing.T) {
	got := UptimePct(86400, 0)
	if got == nil || *got != 100 {
		t.Fatalf("expected 100%%, got %v", got)
	}
}

func TestUptimePct_DownCappedAtMonitored(t *testing.T) {
	// Rounding/overlap can make down > monitored; clamp to 0%, never negative.
	got := UptimePct(3600, 5000)
	if got == nil || *got != 0 {
		t.Fatalf("expected 0%%, got %v", got)
	}
}

func TestHourAndDayStart(t *testing.T) {
	ts := mustTime(t, "2026-07-01T15:42:31Z")
	if got := HourStart(ts); !got.Equal(mustTime(t, "2026-07-01T15:00:00Z")) {
		t.Errorf("HourStart: got %v", got)
	}
	if got := DayStart(ts); !got.Equal(mustTime(t, "2026-07-01T00:00:00Z")) {
		t.Errorf("DayStart: got %v", got)
	}
}

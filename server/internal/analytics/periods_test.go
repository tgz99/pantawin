package analytics

import (
	"testing"
	"time"
	// The suite must pass on hosts without a system tzdb (alpine, Windows).
	_ "time/tzdata"
)

// The M5 TZ suite: period windows are computed in the viewer's zone (WIB
// rendering over UTC storage) and the bucket windows always tile the overall
// window exactly — the precondition for the sum-of-parts == whole guarantee.

func wib(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Fatalf("load Asia/Jakarta: %v", err)
	}
	return loc
}

func assertTiling(t *testing.T, w Window, buckets []Window) {
	t.Helper()
	if len(buckets) == 0 {
		t.Fatal("no bucket windows")
	}
	if !buckets[0].Start.Equal(w.Start) {
		t.Errorf("first bucket starts %v, window starts %v", buckets[0].Start, w.Start)
	}
	if !buckets[len(buckets)-1].End.Equal(w.End) {
		t.Errorf("last bucket ends %v, window ends %v", buckets[len(buckets)-1].End, w.End)
	}
	for i := 1; i < len(buckets); i++ {
		if !buckets[i].Start.Equal(buckets[i-1].End) {
			t.Errorf("gap/overlap between bucket %d and %d: %v != %v",
				i-1, i, buckets[i-1].End, buckets[i].Start)
		}
	}
}

func TestPeriodWindow_DayWIB(t *testing.T) {
	loc := wib(t)
	// Anchor mid-day WIB. The local day 2026-07-14 runs 2026-07-13T17:00Z
	// to 2026-07-14T17:00Z — this boundary crossing UTC midnight is the
	// whole point of TZ-correct bucketing.
	anchor := time.Date(2026, 7, 14, 9, 30, 0, 0, loc)
	w, buckets, err := PeriodWindow(PeriodDay, anchor, loc)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 7, 13, 17, 0, 0, 0, time.UTC)
	if !w.Start.Equal(wantStart) {
		t.Errorf("WIB day starts %v in UTC, want %v", w.Start.UTC(), wantStart)
	}
	if got := w.Seconds(); got != 24*3600 {
		t.Errorf("WIB day = %vs, want 86400", got)
	}
	if len(buckets) != 24 {
		t.Errorf("expected 24 hour buckets, got %d", len(buckets))
	}
	assertTiling(t, w, buckets)
}

func TestPeriodWindow_WeekWIB(t *testing.T) {
	loc := wib(t)
	anchor := time.Date(2026, 7, 14, 23, 59, 0, 0, loc)
	w, buckets, err := PeriodWindow(PeriodWeek, anchor, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 7 {
		t.Errorf("expected 7 day buckets, got %d", len(buckets))
	}
	// Ends at the end of the anchor's local day.
	wantEnd := time.Date(2026, 7, 15, 0, 0, 0, 0, loc)
	if !w.End.Equal(wantEnd) {
		t.Errorf("week ends %v, want %v", w.End, wantEnd)
	}
	assertTiling(t, w, buckets)
}

func TestPeriodWindow_MonthWIB(t *testing.T) {
	loc := wib(t)

	// July: 31 local-day buckets.
	w, buckets, err := PeriodWindow(PeriodMonth, time.Date(2026, 7, 14, 12, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 31 {
		t.Errorf("July: expected 31 day buckets, got %d", len(buckets))
	}
	if !w.Start.Equal(time.Date(2026, 6, 30, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("WIB July starts %v in UTC", w.Start.UTC())
	}
	assertTiling(t, w, buckets)

	// February in a leap year: 29.
	_, buckets, err = PeriodWindow(PeriodMonth, time.Date(2028, 2, 10, 0, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 29 {
		t.Errorf("Feb 2028: expected 29 day buckets, got %d", len(buckets))
	}
}

func TestPeriodWindow_YearWIB(t *testing.T) {
	loc := wib(t)
	w, buckets, err := PeriodWindow(PeriodYear, time.Date(2026, 7, 14, 12, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 12 {
		t.Errorf("expected 12 month buckets, got %d", len(buckets))
	}
	if !w.Start.Equal(time.Date(2025, 12, 31, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("WIB 2026 starts %v in UTC", w.Start.UTC())
	}
	// 2026 is not a leap year.
	if got := w.Seconds(); got != 365*24*3600 {
		t.Errorf("year = %vs, want %v", got, 365*24*3600)
	}
	assertTiling(t, w, buckets)
}

// DST zones: local-day arithmetic must absorb the shift into a 23- or
// 25-hour day instead of drifting the boundaries. (WIB has no DST; this
// guards the general math.)
func TestPeriodWindow_DSTTransitions(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatalf("load Europe/Amsterdam: %v", err)
	}

	// March 2026: spring-forward on the 29th -> a 23-hour day, 743-hour month.
	w, buckets, err := PeriodWindow(PeriodMonth, time.Date(2026, 3, 15, 12, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 31 {
		t.Errorf("March: expected 31 buckets, got %d", len(buckets))
	}
	if got := w.Seconds(); got != 743*3600 {
		t.Errorf("March 2026 (spring-forward) = %vh, want 743", got/3600)
	}
	assertTiling(t, w, buckets)
	short := buckets[28] // March 29
	if got := short.Seconds(); got != 23*3600 {
		t.Errorf("spring-forward day = %vh, want 23", got/3600)
	}

	// October 2026: fall-back on the 25th -> a 25-hour day, 745-hour month.
	w, buckets, err = PeriodWindow(PeriodMonth, time.Date(2026, 10, 1, 0, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Seconds(); got != 745*3600 {
		t.Errorf("October 2026 (fall-back) = %vh, want 745", got/3600)
	}
	assertTiling(t, w, buckets)
	long := buckets[24] // October 25
	if got := long.Seconds(); got != 25*3600 {
		t.Errorf("fall-back day = %vh, want 25", got/3600)
	}

	// The year that contains both transitions still tiles exactly.
	w, buckets, err = PeriodWindow(PeriodYear, time.Date(2026, 6, 1, 0, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	assertTiling(t, w, buckets)
}

func TestPeriodWindow_BadPeriod(t *testing.T) {
	if _, _, err := PeriodWindow("quarter", time.Now(), time.UTC); err != ErrBadPeriod {
		t.Errorf("expected ErrBadPeriod, got %v", err)
	}
}

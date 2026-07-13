package analytics

import (
	"errors"
	"time"
)

const (
	PeriodMonth = "month"
	PeriodYear  = "year"
)

// ErrBadPeriod is returned for a period outside day|week|month|year.
var ErrBadPeriod = errors.New("period must be day, week, month or year")

// PeriodWindow computes the overall stats window for a period anchored at
// `anchor`, plus the bucket windows that tile it, in the given timezone
// (spec M5: WIB rendering over UTC storage — buckets follow the viewer's
// calendar, storage stays UTC). All returned times are absolute instants;
// callers never see wall-clock values. Bucket windows always partition the
// overall window exactly, which is what makes the sum-of-parts == whole
// consistency guarantee possible.
//
//	day   -> the anchor's local day, 24 one-hour buckets
//	week  -> 7 local days ending on the anchor's local day
//	month -> the anchor's local calendar month, one bucket per local day
//	year  -> the anchor's local calendar year, 12 local-month buckets
//
// Local-day arithmetic uses time.Date in loc, so DST transitions yield 23-
// or 25-hour days rather than drifting bucket boundaries. (For period=day
// the hour buckets are local-midnight-aligned; on whole-hour-offset zones
// like WIB they coincide with the UTC-hour stats_hourly rows.)
func PeriodWindow(period string, anchor time.Time, loc *time.Location) (Window, []Window, error) {
	a := anchor.In(loc)
	y, m, d := a.Date()

	switch period {
	case PeriodDay:
		start := time.Date(y, m, d, 0, 0, 0, 0, loc)
		end := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
		return Window{Start: start, End: end}, hourWindows(start, end), nil

	case PeriodWeek:
		end := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
		start := time.Date(y, m, d-6, 0, 0, 0, 0, loc)
		return Window{Start: start, End: end}, dayWindows(start, end, loc), nil

	case PeriodMonth:
		start := time.Date(y, m, 1, 0, 0, 0, 0, loc)
		end := time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
		return Window{Start: start, End: end}, dayWindows(start, end, loc), nil

	case PeriodYear:
		start := time.Date(y, 1, 1, 0, 0, 0, 0, loc)
		end := time.Date(y+1, 1, 1, 0, 0, 0, 0, loc)
		var buckets []Window
		for mm := 1; mm <= 12; mm++ {
			buckets = append(buckets, Window{
				Start: time.Date(y, time.Month(mm), 1, 0, 0, 0, 0, loc),
				End:   time.Date(y, time.Month(mm)+1, 1, 0, 0, 0, 0, loc),
			})
		}
		return Window{Start: start, End: end}, buckets, nil
	}
	return Window{}, nil, ErrBadPeriod
}

func hourWindows(start, end time.Time) []Window {
	var out []Window
	for h := start; h.Before(end); h = h.Add(time.Hour) {
		out = append(out, Window{Start: h, End: h.Add(time.Hour)})
	}
	return out
}

func dayWindows(start, end time.Time, loc *time.Location) []Window {
	var out []Window
	for d := start; d.Before(end); {
		y, m, dd := d.In(loc).Date()
		next := time.Date(y, m, dd+1, 0, 0, 0, 0, loc)
		out = append(out, Window{Start: d, End: next})
		d = next
	}
	return out
}

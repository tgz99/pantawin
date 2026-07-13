package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/tgz99/pantawin/server/internal/analytics"
	"github.com/tgz99/pantawin/server/internal/monitor"
)

type statsHandlers struct {
	repo   *monitor.Repository
	rollup *analytics.Rollup
}

// GET /v1/monitors/{id}/stats?period=day|week|month|year&date=YYYY-MM-DD&tz=Area/City
// (spec 4; M4 day/week, M5 month/year + timezone-correct bucketing).
// tz is the IANA zone the windows are computed in (default UTC); date
// anchors the window and is interpreted in that zone.
func (h *statsHandlers) getStats(w http.ResponseWriter, r *http.Request) {
	userID, _ := userIDFromContext(r.Context())
	mh := &monitorHandlers{repo: h.repo}
	id, ok := mh.monitorIDFromPath(w, r)
	if !ok {
		return
	}
	// Ownership check — stats are as private as the monitor itself.
	if _, err := h.repo.GetForUser(r.Context(), userID, id); err != nil {
		if errors.Is(err, monitor.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}

	period := r.URL.Query().Get("period")
	if period == "" {
		period = analytics.PeriodDay
	}

	loc := time.UTC
	if raw := r.URL.Query().Get("tz"); raw != "" {
		parsed, err := time.LoadLocation(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "tz must be a valid IANA timezone name")
			return
		}
		loc = parsed
	}

	anchor := time.Now().In(loc)
	if raw := r.URL.Query().Get("date"); raw != "" {
		parsed, err := time.ParseInLocation("2006-01-02", raw, loc)
		if err != nil {
			writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
			return
		}
		anchor = parsed
	}

	res, err := h.rollup.Stats(r.Context(), id, period, anchor, loc)
	if err != nil {
		if errors.Is(err, analytics.ErrBadPeriod) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to compute stats")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

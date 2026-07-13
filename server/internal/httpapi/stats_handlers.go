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

// GET /v1/monitors/{id}/stats?period=day|week&date=YYYY-MM-DD (spec 4, M4).
// date anchors the window (defaults to today, UTC).
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
	if period != analytics.PeriodDay && period != analytics.PeriodWeek {
		if period != "" {
			writeError(w, http.StatusBadRequest, "period must be day or week")
			return
		}
		period = analytics.PeriodDay
	}
	anchor := time.Now().UTC()
	if raw := r.URL.Query().Get("date"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "date must be YYYY-MM-DD")
			return
		}
		anchor = parsed
	}

	res, err := h.rollup.Stats(r.Context(), id, period, anchor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute stats")
		return
	}
	writeJSON(w, http.StatusOK, res)
}

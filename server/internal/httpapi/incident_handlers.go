package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/tgz99/pantawin/server/internal/incident"
	"github.com/tgz99/pantawin/server/internal/monitor"
)

type incidentHandlers struct {
	monitors  *monitor.Repository
	incidents *incident.Repository
}

type incidentResponse struct {
	ID         int64      `json:"id"`
	MonitorID  int64      `json:"monitor_id"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	Cause      string     `json:"cause"`
	// DurationS is nil while the incident is ongoing — clients render
	// "ongoing" and compute the elapsed time themselves.
	DurationS *int64 `json:"duration_s"`
}

type incidentListResponse struct {
	Incidents []incidentResponse `json:"incidents"`
}

// GET /v1/monitors/{id}/incidents?limit=N — incident history newest-first
// (spec 5, M5).
func (h *incidentHandlers) listIncidents(w http.ResponseWriter, r *http.Request) {
	userID, _ := userIDFromContext(r.Context())
	mh := &monitorHandlers{repo: h.monitors}
	id, ok := mh.monitorIDFromPath(w, r)
	if !ok {
		return
	}
	// Ownership check — history is as private as the monitor itself.
	if _, err := h.monitors.GetForUser(r.Context(), userID, id); err != nil {
		if errors.Is(err, monitor.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}

	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be an integer")
			return
		}
		limit = parsed
	}

	list, err := h.incidents.ListForMonitor(r.Context(), id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list incidents")
		return
	}

	out := incidentListResponse{Incidents: make([]incidentResponse, 0, len(list))}
	for _, inc := range list {
		resp := incidentResponse{
			ID: inc.ID, MonitorID: inc.MonitorID,
			StartedAt: inc.StartedAt, ResolvedAt: inc.ResolvedAt, Cause: inc.Cause,
		}
		if inc.ResolvedAt != nil {
			d := int64(inc.ResolvedAt.Sub(inc.StartedAt).Seconds())
			resp.DurationS = &d
		}
		out.Incidents = append(out.Incidents, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

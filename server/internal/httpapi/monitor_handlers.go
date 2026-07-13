package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tgz99/pantawin/server/internal/monitor"
	"github.com/tgz99/pantawin/server/internal/ssrf"
)

// SchedulerControl is what the HTTP layer needs from the scheduler —
// (re)queueing and removing monitors as CRUD operations happen.
type SchedulerControl interface {
	Schedule(ctx context.Context, monitorID int64, delay time.Duration) error
	Unschedule(ctx context.Context, monitorID int64) error
}

type monitorHandlers struct {
	repo  *monitor.Repository
	guard *ssrf.Guard
	sched SchedulerControl
}

type monitorRequest struct {
	Name              *string   `json:"name"`
	URL               *string   `json:"url"`
	Method            *string   `json:"method"`
	IntervalSeconds   *int      `json:"interval_seconds"`
	TimeoutMS         *int      `json:"timeout_ms"`
	ExpectedStatusMin *int      `json:"expected_status_min"`
	ExpectedStatusMax *int      `json:"expected_status_max"`
	FailureThreshold  *int      `json:"failure_threshold"`
	AlertChannels     *[]string `json:"alert_channels"`
}

type monitorResponse struct {
	ID                int64          `json:"id"`
	Name              string         `json:"name"`
	URL               string         `json:"url"`
	Method            string         `json:"method"`
	IntervalSeconds   int            `json:"interval_seconds"`
	TimeoutMS         int            `json:"timeout_ms"`
	ExpectedStatusMin int            `json:"expected_status_min"`
	ExpectedStatusMax int            `json:"expected_status_max"`
	FailureThreshold  int            `json:"failure_threshold"`
	AlertChannels     []string       `json:"alert_channels"`
	Status            monitor.Status `json:"status"`
	CreatedAt         time.Time      `json:"created_at"`
}

func toMonitorResponse(m monitor.Monitor) monitorResponse {
	return monitorResponse{
		ID: m.ID, Name: m.Name, URL: m.URL, Method: m.Method,
		IntervalSeconds: m.IntervalSeconds, TimeoutMS: m.TimeoutMS,
		ExpectedStatusMin: m.ExpectedStatusMin, ExpectedStatusMax: m.ExpectedStatusMax,
		FailureThreshold: m.FailureThreshold, AlertChannels: m.AlertChannels,
		Status: m.Status, CreatedAt: m.CreatedAt,
	}
}

// validAlertChannels checks the requested channels are a subset of the
// supported set. "push" is accepted even when FCM is dormant — the
// dispatcher simply skips an unregistered channel, so a monitor can opt into
// push ahead of the server being configured for it.
func validAlertChannels(channels []string) bool {
	if len(channels) == 0 {
		return false
	}
	for _, c := range channels {
		if c != "email" && c != "push" {
			return false
		}
	}
	return true
}

// Spec section 3.2: min interval 30s, default 60s; timeout default 10s;
// expected status default 200-399; failure threshold default 2.
const (
	minIntervalSeconds = 30
	maxIntervalSeconds = 24 * 60 * 60
	minTimeoutMS       = 1000
	maxTimeoutMS       = 60000
	maxFailureThreshold = 10
)

func validateMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead
}

func (h *monitorHandlers) validateAndApplyDefaults(ctx context.Context, req monitorRequest) (monitor.CreateParams, string) {
	p := monitor.CreateParams{
		Method:            http.MethodGet,
		IntervalSeconds:   60,
		TimeoutMS:         10000,
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 399,
		FailureThreshold:  2,
	}

	if req.URL == nil || *req.URL == "" {
		return p, "url is required"
	}
	if err := h.guard.Validate(ctx, *req.URL); err != nil {
		if errors.Is(err, ssrf.ErrForbiddenTarget) {
			return p, "url targets a private or forbidden address"
		}
		return p, "url is invalid or its host cannot be resolved"
	}
	p.URL = *req.URL

	p.Name = p.URL
	if req.Name != nil && *req.Name != "" {
		p.Name = *req.Name
	}
	if req.Method != nil {
		if !validateMethod(*req.Method) {
			return p, "method must be GET or HEAD"
		}
		p.Method = *req.Method
	}
	if req.IntervalSeconds != nil {
		if *req.IntervalSeconds < minIntervalSeconds || *req.IntervalSeconds > maxIntervalSeconds {
			return p, "interval_seconds must be between 30 and 86400"
		}
		p.IntervalSeconds = *req.IntervalSeconds
	}
	if req.TimeoutMS != nil {
		if *req.TimeoutMS < minTimeoutMS || *req.TimeoutMS > maxTimeoutMS {
			return p, "timeout_ms must be between 1000 and 60000"
		}
		p.TimeoutMS = *req.TimeoutMS
	}
	if req.ExpectedStatusMin != nil {
		p.ExpectedStatusMin = *req.ExpectedStatusMin
	}
	if req.ExpectedStatusMax != nil {
		p.ExpectedStatusMax = *req.ExpectedStatusMax
	}
	if p.ExpectedStatusMin < 100 || p.ExpectedStatusMax > 599 || p.ExpectedStatusMin > p.ExpectedStatusMax {
		return p, "expected status range must be within 100-599 and min <= max"
	}
	if req.FailureThreshold != nil {
		if *req.FailureThreshold < 1 || *req.FailureThreshold > maxFailureThreshold {
			return p, "failure_threshold must be between 1 and 10"
		}
		p.FailureThreshold = *req.FailureThreshold
	}
	if req.AlertChannels != nil {
		if !validAlertChannels(*req.AlertChannels) {
			return p, "alert_channels must be a non-empty subset of [email, push]"
		}
		p.AlertChannels = *req.AlertChannels
	}
	return p, ""
}

func (h *monitorHandlers) createMonitor(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	var req monitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	params, problem := h.validateAndApplyDefaults(r.Context(), req)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	params.UserID = userID

	m, err := h.repo.Create(r.Context(), params)
	if err != nil {
		if errors.Is(err, monitor.ErrQuotaExceeded) {
			writeError(w, http.StatusUnprocessableEntity, "monitor quota exceeded")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create monitor")
		return
	}

	// First check immediately — the user is watching the dashboard.
	if err := h.sched.Schedule(r.Context(), m.ID, 0); err != nil {
		// Non-fatal: EnsureScheduled on next boot picks it up; log via
		// middleware. The monitor row exists either way.
		writeJSON(w, http.StatusCreated, toMonitorResponse(m))
		return
	}

	writeJSON(w, http.StatusCreated, toMonitorResponse(m))
}

func (h *monitorHandlers) monitorIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid monitor id")
		return 0, false
	}
	return id, true
}

func (h *monitorHandlers) getMonitor(w http.ResponseWriter, r *http.Request) {
	userID, _ := userIDFromContext(r.Context())
	id, ok := h.monitorIDFromPath(w, r)
	if !ok {
		return
	}
	m, err := h.repo.GetForUser(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, monitor.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load monitor")
		return
	}
	writeJSON(w, http.StatusOK, toMonitorResponse(m))
}

func (h *monitorHandlers) updateMonitor(w http.ResponseWriter, r *http.Request) {
	userID, _ := userIDFromContext(r.Context())
	id, ok := h.monitorIDFromPath(w, r)
	if !ok {
		return
	}

	var req monitorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// PATCH semantics: validate only what's being changed.
	if req.URL != nil {
		if err := h.guard.Validate(r.Context(), *req.URL); err != nil {
			if errors.Is(err, ssrf.ErrForbiddenTarget) {
				writeError(w, http.StatusBadRequest, "url targets a private or forbidden address")
				return
			}
			writeError(w, http.StatusBadRequest, "url is invalid or its host cannot be resolved")
			return
		}
	}
	if req.Method != nil && !validateMethod(*req.Method) {
		writeError(w, http.StatusBadRequest, "method must be GET or HEAD")
		return
	}
	if req.IntervalSeconds != nil && (*req.IntervalSeconds < minIntervalSeconds || *req.IntervalSeconds > maxIntervalSeconds) {
		writeError(w, http.StatusBadRequest, "interval_seconds must be between 30 and 86400")
		return
	}
	if req.TimeoutMS != nil && (*req.TimeoutMS < minTimeoutMS || *req.TimeoutMS > maxTimeoutMS) {
		writeError(w, http.StatusBadRequest, "timeout_ms must be between 1000 and 60000")
		return
	}
	if req.FailureThreshold != nil && (*req.FailureThreshold < 1 || *req.FailureThreshold > maxFailureThreshold) {
		writeError(w, http.StatusBadRequest, "failure_threshold must be between 1 and 10")
		return
	}
	if req.AlertChannels != nil && !validAlertChannels(*req.AlertChannels) {
		writeError(w, http.StatusBadRequest, "alert_channels must be a non-empty subset of [email, push]")
		return
	}

	m, err := h.repo.Update(r.Context(), userID, id, monitor.UpdateParams{
		Name: req.Name, URL: req.URL, Method: req.Method,
		IntervalSeconds: req.IntervalSeconds, TimeoutMS: req.TimeoutMS,
		ExpectedStatusMin: req.ExpectedStatusMin, ExpectedStatusMax: req.ExpectedStatusMax,
		FailureThreshold: req.FailureThreshold, AlertChannels: req.AlertChannels,
	})
	if err != nil {
		if errors.Is(err, monitor.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update monitor")
		return
	}
	writeJSON(w, http.StatusOK, toMonitorResponse(m))
}

func (h *monitorHandlers) deleteMonitor(w http.ResponseWriter, r *http.Request) {
	userID, _ := userIDFromContext(r.Context())
	id, ok := h.monitorIDFromPath(w, r)
	if !ok {
		return
	}
	if err := h.repo.Delete(r.Context(), userID, id); err != nil {
		if errors.Is(err, monitor.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete monitor")
		return
	}
	_ = h.sched.Unschedule(r.Context(), id) // best-effort; scheduler also drops missing monitors
	writeJSON(w, http.StatusNoContent, nil)
}

func (h *monitorHandlers) pauseMonitor(w http.ResponseWriter, r *http.Request) {
	h.setPaused(w, r, true)
}

func (h *monitorHandlers) resumeMonitor(w http.ResponseWriter, r *http.Request) {
	h.setPaused(w, r, false)
}

func (h *monitorHandlers) setPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	userID, _ := userIDFromContext(r.Context())
	id, ok := h.monitorIDFromPath(w, r)
	if !ok {
		return
	}
	m, err := h.repo.SetPaused(r.Context(), userID, id, paused)
	if err != nil {
		if errors.Is(err, monitor.ErrNotFound) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update monitor")
		return
	}
	if paused {
		_ = h.sched.Unschedule(r.Context(), id)
	} else {
		_ = h.sched.Schedule(r.Context(), id, 0) // fresh PENDING confirmation cycle now
	}
	writeJSON(w, http.StatusOK, toMonitorResponse(m))
}

// listMonitors handles GET /v1/monitors — returns the authenticated user's
// monitors with their current status (spec section 4).
func (h *monitorHandlers) listMonitors(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	views, err := h.repo.StatusViews(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load monitors")
		return
	}
	if views == nil {
		views = []monitor.StatusView{}
	}

	writeJSON(w, http.StatusOK, views)
}

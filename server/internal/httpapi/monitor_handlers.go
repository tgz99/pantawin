package httpapi

import (
	"net/http"

	"github.com/tgz99/pantawin/server/internal/monitor"
)

type monitorHandlers struct {
	repo *monitor.Repository
}

// listMonitors handles GET /v1/monitors — returns the authenticated user's
// monitors with their current status (spec section 4). M0 has exactly one
// seeded monitor; M1 adds full CRUD on the same resource.
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

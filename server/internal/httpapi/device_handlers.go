package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/tgz99/pantawin/server/internal/device"
)

type deviceHandlers struct {
	repo *device.Repository
}

type registerDeviceRequest struct {
	FCMToken string `json:"fcm_token"`
	Platform string `json:"platform"`
}

// POST /v1/devices — register the caller's FCM token (spec section 4).
func (h *deviceHandlers) register(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}
	var req registerDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.FCMToken == "" {
		writeError(w, http.StatusBadRequest, "fcm_token is required")
		return
	}
	if err := h.repo.Register(r.Context(), userID, req.FCMToken, req.Platform); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to register device")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "registered"})
}

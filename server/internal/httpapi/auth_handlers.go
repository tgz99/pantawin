package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tgz99/pantawin/server/internal/auth"
)

type authHandlers struct {
	service *auth.Service
}

type registerLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type tokensResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func (h *authHandlers) register(w http.ResponseWriter, r *http.Request) {
	var req registerLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	tokens, err := h.service.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrEmailAlreadyRegistered) {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to register")
		return
	}

	writeJSON(w, http.StatusCreated, tokensResponse{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken})
}

func (h *authHandlers) login(w http.ResponseWriter, r *http.Request) {
	var req registerLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	tokens, err := h.service.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid email or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to log in")
		return
	}

	writeJSON(w, http.StatusOK, tokensResponse{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// changePassword rotates the authenticated user's password. A wrong current
// password is a 400 (NOT 401 — the access token is valid, and a 401 would
// trigger the app's silent-refresh-then-logout path).
func (h *authHandlers) changePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "current_password and new_password are required")
		return
	}

	tokens, err := h.service.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeError(w, http.StatusBadRequest, "current password is incorrect")
		case errors.Is(err, auth.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, auth.ErrWeakPassword.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to change password")
		}
		return
	}

	writeJSON(w, http.StatusOK, tokensResponse{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken})
}

func (h *authHandlers) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	tokens, err := h.service.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to refresh")
		return
	}

	writeJSON(w, http.StatusOK, tokensResponse{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken})
}

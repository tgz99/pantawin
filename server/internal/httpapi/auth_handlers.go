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

// register starts email/password signup: it does NOT return a session.
// The account is unverified until verifyOTP succeeds (M6.2) — the client
// must show a code-entry screen next.
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

	if err := h.service.Register(r.Context(), req.Email, req.Password); err != nil {
		switch {
		case errors.Is(err, auth.ErrEmailAlreadyRegistered):
			writeError(w, http.StatusConflict, "email already registered")
		case errors.Is(err, auth.ErrSignupNotAllowed):
			writeError(w, http.StatusForbidden, "signup is not allowed for this email")
		case errors.Is(err, auth.ErrWeakPassword):
			writeError(w, http.StatusBadRequest, auth.ErrWeakPassword.Error())
		case errors.Is(err, auth.ErrOTPResendTooSoon):
			writeError(w, http.StatusTooManyRequests, auth.ErrOTPResendTooSoon.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to register")
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"status": "verification_required", "email": req.Email})
}

type otpEmailRequest struct {
	Email string `json:"email"`
}

type verifyOTPRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// verifyOTP completes email/password registration and issues a session.
func (h *authHandlers) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var req verifyOTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Email == "" || req.Code == "" {
		writeError(w, http.StatusBadRequest, "email and code are required")
		return
	}

	tokens, err := h.service.VerifyOTP(r.Context(), req.Email, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrOTPInvalid):
			writeError(w, http.StatusBadRequest, auth.ErrOTPInvalid.Error())
		case errors.Is(err, auth.ErrOTPExpired):
			writeError(w, http.StatusBadRequest, auth.ErrOTPExpired.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to verify code")
		}
		return
	}

	writeJSON(w, http.StatusOK, tokensResponse{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken})
}

// resendOTP re-sends the verification code for an account still pending
// verification (lost email, expired code, etc).
func (h *authHandlers) resendOTP(w http.ResponseWriter, r *http.Request) {
	var req otpEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	if err := h.service.ResendOTP(r.Context(), req.Email); err != nil {
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			writeError(w, http.StatusNotFound, "no pending registration for this email")
		case errors.Is(err, auth.ErrEmailAlreadyRegistered):
			writeError(w, http.StatusConflict, "this account is already verified — sign in instead")
		case errors.Is(err, auth.ErrOTPResendTooSoon):
			writeError(w, http.StatusTooManyRequests, auth.ErrOTPResendTooSoon.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to resend code")
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (h *authHandlers) login(w http.ResponseWriter, r *http.Request) {
	var req registerLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	tokens, err := h.service.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "invalid email or password")
		case errors.Is(err, auth.ErrEmailNotVerified):
			// 428 Precondition Required: distinct from bad credentials so the
			// client can route straight to the OTP screen instead of showing
			// a generic login error.
			writeError(w, http.StatusPreconditionRequired, "please verify your email before signing in")
		default:
			writeError(w, http.StatusInternalServerError, "failed to log in")
		}
		return
	}

	writeJSON(w, http.StatusOK, tokensResponse{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken})
}

type googleLoginRequest struct {
	IDToken string `json:"id_token"`
}

// googleLogin exchanges a Google ID token (obtained by the app via
// Credential Manager) for a Pantawin session.
func (h *authHandlers) googleLogin(w http.ResponseWriter, r *http.Request) {
	var req googleLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.IDToken == "" {
		writeError(w, http.StatusBadRequest, "id_token is required")
		return
	}

	tokens, err := h.service.GoogleLogin(r.Context(), req.IDToken)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrGoogleNotConfigured):
			writeError(w, http.StatusNotImplemented, "google sign-in is not configured")
		case errors.Is(err, auth.ErrGoogleTokenInvalid):
			writeError(w, http.StatusUnauthorized, "invalid google id token")
		case errors.Is(err, auth.ErrSignupNotAllowed):
			writeError(w, http.StatusForbidden, "signup is not allowed for this email")
		default:
			writeError(w, http.StatusInternalServerError, "failed to sign in with google")
		}
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

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tgz99/pantawin/server/internal/team"
)

// Team management (M6.1): the admin invites teammate emails; invited emails
// can create an account (Google SSO in practice). Admin-only — the team is
// flat otherwise, and monitors are already shared by scope.
type teamHandlers struct {
	repo        *team.Repository
	adminUserID int64
}

func (h *teamHandlers) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return false
	}
	if userID != h.adminUserID {
		writeError(w, http.StatusForbidden, "only the admin can manage the team")
		return false
	}
	return true
}

type teamListResponse struct {
	Members []team.Member `json:"members"`
}

func (h *teamHandlers) list(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	members, err := h.repo.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list team")
		return
	}
	if members == nil {
		members = []team.Member{}
	}
	writeJSON(w, http.StatusOK, teamListResponse{Members: members})
}

type teamMemberRequest struct {
	Email string `json:"email"`
}

func validInviteEmail(email string) bool {
	email = strings.TrimSpace(email)
	at := strings.IndexByte(email, '@')
	// Loose shape check only — the real verification is Google confirming
	// ownership of the address at sign-in time.
	return at > 0 && at < len(email)-3 && !strings.ContainsAny(email, " \t\r\n") &&
		strings.Contains(email[at:], ".")
}

func (h *teamHandlers) add(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req teamMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !validInviteEmail(req.Email) {
		writeError(w, http.StatusBadRequest, "email is not a valid address")
		return
	}
	if err := h.repo.Add(r.Context(), req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add team member")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "invited"})
}

// remove withdraws an invite. POST body (not DELETE path segment) because
// emails make hostile URL path elements.
func (h *teamHandlers) remove(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req teamMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	joined, err := h.repo.HasJoined(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove team member")
		return
	}
	if joined {
		writeError(w, http.StatusConflict, "this member already has an account; accounts cannot be removed from the app")
		return
	}
	removed, err := h.repo.Remove(r.Context(), req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove team member")
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no invite found for this email")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

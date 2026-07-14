package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tgz99/pantawin/server/internal/team"
)

// Team management (M6.3): any registered account can create a team and
// invite others into it; an account can belong to any number of teams.
// There is no admin role — every per-team action here is gated on the
// caller being a member of that team, not on having created it.
type teamHandlers struct {
	repo *team.Repository
}

type teamsResponse struct {
	Teams []team.Team `json:"teams"`
}

type teamMembersResponse struct {
	Members []team.Member `json:"members"`
}

type createTeamRequest struct {
	Name string `json:"name"`
}

func (h *teamHandlers) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}
	var req createTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	t, err := h.repo.Create(r.Context(), userID, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create team")
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// list returns every team the caller belongs to (spec: an account can join
// multiple teams).
func (h *teamHandlers) list(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}
	teams, err := h.repo.ListForUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list teams")
		return
	}
	if teams == nil {
		teams = []team.Team{}
	}
	writeJSON(w, http.StatusOK, teamsResponse{Teams: teams})
}

func (h *teamHandlers) teamIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid team id")
		return 0, false
	}
	return id, true
}

// requireMember checks the caller belongs to teamID — every per-team action
// is gated on membership, not on being the team's creator.
func (h *teamHandlers) requireMember(w http.ResponseWriter, r *http.Request, teamID int64) (int64, bool) {
	userID, ok := userIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return 0, false
	}
	member, err := h.repo.IsMember(r.Context(), teamID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify team membership")
		return 0, false
	}
	if !member {
		writeError(w, http.StatusForbidden, "you are not a member of this team")
		return 0, false
	}
	return userID, true
}

func (h *teamHandlers) listMembers(w http.ResponseWriter, r *http.Request) {
	teamID, ok := h.teamIDFromPath(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireMember(w, r, teamID); !ok {
		return
	}
	members, err := h.repo.ListMembers(r.Context(), teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list members")
		return
	}
	if members == nil {
		members = []team.Member{}
	}
	writeJSON(w, http.StatusOK, teamMembersResponse{Members: members})
}

type teamMemberRequest struct {
	Email string `json:"email"`
}

func validInviteEmail(email string) bool {
	email = strings.TrimSpace(email)
	at := strings.IndexByte(email, '@')
	// Loose shape check only — the real verification is Google (or the OTP
	// step) confirming ownership of the address at sign-in time.
	return at > 0 && at < len(email)-3 && !strings.ContainsAny(email, " \t\r\n") &&
		strings.Contains(email[at:], ".")
}

// invite adds a member to the team: any current member may invite, matching
// "every registered account can invite others as their team."
func (h *teamHandlers) invite(w http.ResponseWriter, r *http.Request) {
	teamID, ok := h.teamIDFromPath(w, r)
	if !ok {
		return
	}
	userID, ok := h.requireMember(w, r, teamID)
	if !ok {
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
	if err := h.repo.Invite(r.Context(), teamID, userID, req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to invite team member")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "invited"})
}

// removeInvite withdraws a pending (not-yet-joined) invite. POST body (not a
// DELETE path segment) because emails make hostile URL path elements.
func (h *teamHandlers) removeInvite(w http.ResponseWriter, r *http.Request) {
	teamID, ok := h.teamIDFromPath(w, r)
	if !ok {
		return
	}
	if _, ok := h.requireMember(w, r, teamID); !ok {
		return
	}
	var req teamMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	joined, err := h.repo.HasJoined(r.Context(), teamID, req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove invite")
		return
	}
	if joined {
		writeError(w, http.StatusConflict, "this email already joined the team and can't be removed here")
		return
	}
	removed, err := h.repo.RemoveInvite(r.Context(), teamID, req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove invite")
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "no pending invite found for this email")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

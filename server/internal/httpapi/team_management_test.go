//go:build integration

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// M6.3 exit criteria: signup is closed until someone invites an email into
// a team; any team member (not just its creator) can invite; invited
// emails join via the normal register/OTP flow (Google SSO also works, see
// TestGoogleLoginSkipsOTPAndVerifiesPendingAccount); non-members can't
// manage a team they don't belong to; a joined member can't be removed via
// this endpoint.
func TestTeamManagement(t *testing.T) {
	env := newGatedTestEnv(t)

	// Bootstrapped account logs in and creates a team.
	resp := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]any{
		"email": "admin@pantawin.test", "password": "Admin-Pass-42",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap login: expected 200, got %d", resp.StatusCode)
	}
	var owner tokens
	json.NewDecoder(resp.Body).Decode(&owner)
	resp.Body.Close()

	resp = env.do(t, http.MethodPost, "/v1/teams", owner.AccessToken, map[string]any{"name": "Ops"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create team: expected 201, got %d", resp.StatusCode)
	}
	var createdTeam struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&createdTeam)
	resp.Body.Close()
	if createdTeam.Name != "Ops" {
		t.Fatalf("created team name = %q, want Ops", createdTeam.Name)
	}

	// The creator is already listed as a member.
	resp = env.do(t, http.MethodGet, "/v1/teams", owner.AccessToken, nil)
	var teamsList struct {
		Teams []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"teams"`
	}
	json.NewDecoder(resp.Body).Decode(&teamsList)
	resp.Body.Close()
	if len(teamsList.Teams) != 1 || teamsList.Teams[0].ID != createdTeam.ID {
		t.Fatalf("owner's team list = %+v, want just %d", teamsList.Teams, createdTeam.ID)
	}

	// Signup is closed by default: a stranger is rejected.
	resp, err := http.Post(env.server.URL+"/v1/auth/register", "application/json",
		strings.NewReader(`{"email":"stranger@evil.com","password":"Correct-Horse-42"}`))
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("uninvited register: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Bad invite email rejected; good one accepted.
	membersPath := "/v1/teams/" + strconv.FormatInt(createdTeam.ID, 10) + "/members"
	resp = env.do(t, http.MethodPost, membersPath, owner.AccessToken, map[string]any{"email": "not-an-email"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad invite email: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, membersPath, owner.AccessToken, map[string]any{"email": "Mate@Corp.com"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	memberState := func() map[string]bool {
		resp := env.do(t, http.MethodGet, membersPath, owner.AccessToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list members: expected 200, got %d", resp.StatusCode)
		}
		var list struct {
			Members []struct {
				Email  string `json:"email"`
				Joined bool   `json:"joined"`
			} `json:"members"`
		}
		json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		state := make(map[string]bool, len(list.Members))
		for _, m := range list.Members {
			state[m.Email] = m.Joined
		}
		return state
	}
	state := memberState()
	if joined, ok := state["admin@pantawin.test"]; !ok || !joined {
		t.Errorf("owner should be listed as joined, got %v", state)
	}
	if joined, ok := state["mate@corp.com"]; !ok || joined {
		t.Errorf("invite should be listed as not joined, got %v", state)
	}

	// The invited email registers (email/password + OTP) and is immediately
	// a member — no separate "accept invite" step.
	mate := env.register(t, "mate@corp.com", "Correct-Horse-42-staple")
	if state := memberState(); !state["mate@corp.com"] {
		t.Errorf("mate should be joined after registering, got %v", state)
	}

	// Mate (a member, not the creator) can invite too — "any account can
	// invite others as their team," not just the team's creator.
	resp = env.do(t, http.MethodPost, membersPath, mate.AccessToken, map[string]any{"email": "third@corp.com"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("non-creator member invite: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// An outsider (registered, but not a member of THIS team) cannot manage
	// it. Give them an account via an unrelated invite — registration is
	// gated — without making them a member of the "Ops" team under test.
	resp = env.do(t, http.MethodPost, "/v1/teams", owner.AccessToken, map[string]any{"name": "Unrelated"})
	var unrelatedTeam struct {
		ID int64 `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&unrelatedTeam)
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/teams/"+strconv.FormatInt(unrelatedTeam.ID, 10)+"/members", owner.AccessToken,
		map[string]any{"email": "outsider@pantawin.test"})
	resp.Body.Close()
	outsider := env.register(t, "outsider@pantawin.test", "Correct-Horse-42-staple")
	for _, probe := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, membersPath, nil},
		{http.MethodPost, membersPath, map[string]any{"email": "x@y.com"}},
		{http.MethodPost, membersPath + "/remove", map[string]any{"email": "x@y.com"}},
	} {
		resp := env.do(t, probe.method, probe.path, outsider.AccessToken, probe.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as non-member: expected 403, got %d", probe.method, probe.path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// A joined member can't be removed via this endpoint; a pending invite
	// can be, and removal closes the door again.
	resp = env.do(t, http.MethodPost, membersPath+"/remove", owner.AccessToken, map[string]any{"email": "mate@corp.com"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("remove joined member: expected 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.do(t, http.MethodPost, membersPath, owner.AccessToken, map[string]any{"email": "temp@corp.com"})
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, membersPath+"/remove", owner.AccessToken, map[string]any{"email": "temp@corp.com"})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("remove pending invite: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp, err = http.Post(env.server.URL+"/v1/auth/register", "application/json",
		strings.NewReader(`{"email":"temp@corp.com","password":"Correct-Horse-42"}`))
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("register after invite removed: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// An account can belong to several teams at once, invited into each by
// different, unrelated members.
func TestAccountJoinsMultipleTeams(t *testing.T) {
	env := newGatedTestEnv(t)

	login := func(email, password string) tokens {
		resp := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]any{"email": email, "password": password})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login %s: expected 200, got %d", email, resp.StatusCode)
		}
		var tk tokens
		json.NewDecoder(resp.Body).Decode(&tk)
		resp.Body.Close()
		return tk
	}
	createTeam := func(owner tokens, name string) int64 {
		resp := env.do(t, http.MethodPost, "/v1/teams", owner.AccessToken, map[string]any{"name": name})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create team %s: expected 201, got %d", name, resp.StatusCode)
		}
		var created struct {
			ID int64 `json:"id"`
		}
		json.NewDecoder(resp.Body).Decode(&created)
		resp.Body.Close()
		return created.ID
	}

	owner := login("admin@pantawin.test", "Admin-Pass-42")
	teamAID := createTeam(owner, "Team A")

	// Registration is gated: other-owner needs an invite from SOMEWHERE to
	// get an account at all, but team creation itself isn't gated — once
	// registered, they create their own independent Team B.
	resp := env.do(t, http.MethodPost, "/v1/teams/"+strconv.FormatInt(teamAID, 10)+"/members", owner.AccessToken,
		map[string]any{"email": "other-owner@pantawin.test"})
	resp.Body.Close()
	otherOwner := env.register(t, "other-owner@pantawin.test", "Correct-Horse-42-staple")
	teamBID := createTeam(otherOwner, "Team B")

	// Both teams invite the SAME not-yet-existing email before it registers.
	resp = env.do(t, http.MethodPost, "/v1/teams/"+strconv.FormatInt(teamAID, 10)+"/members", owner.AccessToken,
		map[string]any{"email": "shared@corp.com"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite to team A: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/teams/"+strconv.FormatInt(teamBID, 10)+"/members", otherOwner.AccessToken,
		map[string]any{"email": "shared@corp.com"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite to team B: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Registering once joins BOTH teams.
	shared := env.register(t, "shared@corp.com", "Correct-Horse-42-staple")
	resp = env.do(t, http.MethodGet, "/v1/teams", shared.AccessToken, nil)
	var list struct {
		Teams []struct {
			ID int64 `json:"id"`
		} `json:"teams"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Teams) != 2 {
		t.Fatalf("expected shared@corp.com to be in 2 teams, got %d: %+v", len(list.Teams), list.Teams)
	}
	seen := map[int64]bool{}
	for _, tm := range list.Teams {
		seen[tm.ID] = true
	}
	if !seen[teamAID] || !seen[teamBID] {
		t.Fatalf("expected membership in both %d and %d, got %+v", teamAID, teamBID, list.Teams)
	}

	// Inviting an email that ALREADY has an account adds it immediately (no
	// pending-invite / registration step). other-owner is already in Team A
	// (from the invite that unlocked their registration) and Team B (which
	// they created); a fresh Team C proves the immediate-add path.
	teamCID := createTeam(owner, "Team C")
	resp = env.do(t, http.MethodPost, "/v1/teams/"+strconv.FormatInt(teamCID, 10)+"/members", owner.AccessToken,
		map[string]any{"email": "other-owner@pantawin.test"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite existing account: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodGet, "/v1/teams", otherOwner.AccessToken, nil)
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Teams) != 3 {
		t.Fatalf("expected other-owner to now be in 3 teams (A, B, C), got %d: %+v", len(list.Teams), list.Teams)
	}
	seen = map[int64]bool{}
	for _, tm := range list.Teams {
		seen[tm.ID] = true
	}
	if !seen[teamAID] || !seen[teamBID] || !seen[teamCID] {
		t.Fatalf("expected membership in A, B, and C, got %+v", list.Teams)
	}
}

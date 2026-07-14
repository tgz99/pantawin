//go:build integration

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// M6.1 exit criteria: signup is closed until the admin invites an email
// in-app; invited emails can join (Google SSO in production, register here);
// only the admin can manage the team; joined members can't be removed.
func TestTeamManagement(t *testing.T) {
	env := newGatedTestEnv(t)

	// Admin logs in (account bootstrapped by the gated env).
	resp := env.do(t, http.MethodPost, "/v1/auth/login", "", map[string]any{
		"email": "admin@pantawin.test", "password": "Admin-Pass-42",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin login: expected 200, got %d", resp.StatusCode)
	}
	var admin tokens
	json.NewDecoder(resp.Body).Decode(&admin)
	resp.Body.Close()

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
	resp = env.do(t, http.MethodPost, "/v1/team", admin.AccessToken, map[string]any{"email": "not-an-email"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad invite email: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/team", admin.AccessToken, map[string]any{"email": "Mate@Corp.com"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// List shows the admin (joined) and the invite (not joined yet).
	memberState := func() map[string]bool {
		resp := env.do(t, http.MethodGet, "/v1/team", admin.AccessToken, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list team: expected 200, got %d", resp.StatusCode)
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
		t.Errorf("admin should be listed as joined, got %v", state)
	}
	if joined, ok := state["mate@corp.com"]; !ok || joined {
		t.Errorf("invite should be listed as not joined, got %v", state)
	}

	// The invited email can create an account (case-insensitive).
	mate := env.register(t, "mate@corp.com", "Correct-Horse-42-staple")
	if state := memberState(); !state["mate@corp.com"] {
		t.Errorf("mate should be joined after registering, got %v", state)
	}

	// Non-admin members cannot manage the team.
	for _, probe := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/team", nil},
		{http.MethodPost, "/v1/team", map[string]any{"email": "x@y.com"}},
		{http.MethodPost, "/v1/team/remove", map[string]any{"email": "x@y.com"}},
	} {
		resp := env.do(t, probe.method, probe.path, mate.AccessToken, probe.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as non-admin: expected 403, got %d", probe.method, probe.path, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// A joined member can't be removed; a pending invite can, and removal
	// closes the door again.
	resp = env.do(t, http.MethodPost, "/v1/team/remove", admin.AccessToken, map[string]any{"email": "mate@corp.com"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("remove joined member: expected 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = env.do(t, http.MethodPost, "/v1/team", admin.AccessToken, map[string]any{"email": "temp@corp.com"})
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/team/remove", admin.AccessToken, map[string]any{"email": "temp@corp.com"})
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

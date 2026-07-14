//go:build integration

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

// M6 exit criteria: a team-scoped monitor is visible and manageable by every
// user, alerts fan out to every user's email, and personal monitors stay
// isolated (covered by TestMonitorOwnershipIsolation).
func TestTeamMonitorSharedAccess(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice@pantawin.test", "Correct-Horse-42-staple")
	bob := env.register(t, "bob@pantawin.test", "Correct-Horse-42-staple")

	// Scope is validated.
	resp := env.do(t, http.MethodPost, "/v1/monitors", alice.AccessToken, map[string]any{
		"url": "https://example.com", "scope": "everyone",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad scope, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Alice creates a team monitor.
	resp = env.do(t, http.MethodPost, "/v1/monitors", alice.AccessToken, map[string]any{
		"url": "https://example.com", "name": "shared-prod", "scope": "team",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating team monitor, got %d", resp.StatusCode)
	}
	var created struct {
		ID    int64  `json:"id"`
		Scope string `json:"scope"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.Scope != "team" {
		t.Fatalf("created monitor scope = %q, want team", created.Scope)
	}

	// Bob sees it in his list, tagged with its scope.
	resp = env.do(t, http.MethodGet, "/v1/monitors", bob.AccessToken, nil)
	var list []struct {
		ID    int64  `json:"id"`
		Scope string `json:"scope"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	idx := slices.IndexFunc(list, func(v struct {
		ID    int64  `json:"id"`
		Scope string `json:"scope"`
	}) bool {
		return v.ID == created.ID
	})
	if idx < 0 {
		t.Fatalf("bob's list is missing the team monitor (got %d monitors)", len(list))
	}
	if list[idx].Scope != "team" {
		t.Errorf("team monitor scope in list = %q, want team", list[idx].Scope)
	}

	// Bob can read, edit, pause, and see stats/incidents of the team monitor.
	for _, probe := range []struct {
		method, path string
		body         any
		wantStatus   int
	}{
		{http.MethodGet, fmt.Sprintf("/v1/monitors/%d", created.ID), nil, http.StatusOK},
		{http.MethodPatch, fmt.Sprintf("/v1/monitors/%d", created.ID), map[string]any{"name": "renamed-by-bob"}, http.StatusOK},
		{http.MethodPost, fmt.Sprintf("/v1/monitors/%d/pause", created.ID), nil, http.StatusOK},
		{http.MethodPost, fmt.Sprintf("/v1/monitors/%d/resume", created.ID), nil, http.StatusOK},
		{http.MethodGet, fmt.Sprintf("/v1/monitors/%d/stats", created.ID), nil, http.StatusOK},
		{http.MethodGet, fmt.Sprintf("/v1/monitors/%d/incidents", created.ID), nil, http.StatusOK},
	} {
		resp := env.do(t, probe.method, probe.path, bob.AccessToken, probe.body)
		if resp.StatusCode != probe.wantStatus {
			t.Errorf("%s %s as team member: expected %d, got %d", probe.method, probe.path, probe.wantStatus, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// Email fanout: the team monitor alerts both alice and bob.
	_, emails, err := env.monitorRepo.AlertConfig(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("AlertConfig: %v", err)
	}
	for _, want := range []string{"alice@pantawin.test", "bob@pantawin.test"} {
		if !slices.Contains(emails, want) {
			t.Errorf("team AlertConfig emails %v missing %s", emails, want)
		}
	}

	// A personal monitor still alerts only its owner.
	resp = env.do(t, http.MethodPost, "/v1/monitors", alice.AccessToken, map[string]any{
		"url": "https://personal.example.com",
	})
	var personal struct {
		ID    int64  `json:"id"`
		Scope string `json:"scope"`
	}
	json.NewDecoder(resp.Body).Decode(&personal)
	resp.Body.Close()
	if personal.Scope != "personal" {
		t.Errorf("default scope = %q, want personal", personal.Scope)
	}
	_, emails, err = env.monitorRepo.AlertConfig(context.Background(), personal.ID)
	if err != nil {
		t.Fatalf("AlertConfig(personal): %v", err)
	}
	if len(emails) != 1 || emails[0] != "alice@pantawin.test" {
		t.Errorf("personal AlertConfig emails = %v, want [alice@pantawin.test]", emails)
	}

	// Scope can be flipped via PATCH; bob loses access once it's personal.
	resp = env.do(t, http.MethodPatch, fmt.Sprintf("/v1/monitors/%d", created.ID), alice.AccessToken, map[string]any{"scope": "personal"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH scope->personal: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodGet, fmt.Sprintf("/v1/monitors/%d", created.ID), bob.AccessToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("after scope->personal, bob GET: expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

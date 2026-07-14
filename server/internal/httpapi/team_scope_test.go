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

// M6/M6.3 exit criteria: a team-scoped monitor is visible and manageable by
// every member of its specific team (and only that team), alerts fan out to
// every member's email, and personal monitors stay isolated (covered by
// TestMonitorOwnershipIsolation). Someone outside the team — even a
// registered account — can't see it.
func TestTeamMonitorSharedAccess(t *testing.T) {
	env := newTestEnv(t)
	alice := env.register(t, "alice@pantawin.test", "Correct-Horse-42-staple")
	bob := env.register(t, "bob@pantawin.test", "Correct-Horse-42-staple")
	outsider := env.register(t, "outsider@pantawin.test", "Correct-Horse-42-staple")

	resp := env.do(t, http.MethodPost, "/v1/teams", alice.AccessToken, map[string]any{"name": "Ops"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create team: expected 201, got %d", resp.StatusCode)
	}
	var teamID struct {
		ID int64 `json:"id"`
	}
	json.NewDecoder(resp.Body).Decode(&teamID)
	resp.Body.Close()

	resp = env.do(t, http.MethodPost, fmt.Sprintf("/v1/teams/%d/members", teamID.ID), alice.AccessToken,
		map[string]any{"email": "bob@pantawin.test"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite bob: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Scope requires a matching team_id, and membership is enforced.
	resp = env.do(t, http.MethodPost, "/v1/monitors", alice.AccessToken, map[string]any{
		"url": "https://example.com", "scope": "everyone",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad scope, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/monitors", alice.AccessToken, map[string]any{
		"url": "https://example.com", "scope": "team",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("team scope without team_id: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = env.do(t, http.MethodPost, "/v1/monitors", outsider.AccessToken, map[string]any{
		"url": "https://example.com", "scope": "team", "team_id": teamID.ID,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("team scope for a team you don't belong to: expected 403, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Alice creates a team monitor.
	resp = env.do(t, http.MethodPost, "/v1/monitors", alice.AccessToken, map[string]any{
		"url": "https://example.com", "name": "shared-prod", "scope": "team", "team_id": teamID.ID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating team monitor, got %d", resp.StatusCode)
	}
	var created struct {
		ID     int64  `json:"id"`
		Scope  string `json:"scope"`
		TeamID *int64 `json:"team_id"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if created.Scope != "team" || created.TeamID == nil || *created.TeamID != teamID.ID {
		t.Fatalf("created monitor scope/team_id = %q/%v, want team/%d", created.Scope, created.TeamID, teamID.ID)
	}

	// Bob (a team member) sees it in his list, tagged with its team.
	resp = env.do(t, http.MethodGet, "/v1/monitors", bob.AccessToken, nil)
	var list []struct {
		ID     int64  `json:"id"`
		Scope  string `json:"scope"`
		TeamID *int64 `json:"team_id"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	idx := slices.IndexFunc(list, func(v struct {
		ID     int64  `json:"id"`
		Scope  string `json:"scope"`
		TeamID *int64 `json:"team_id"`
	}) bool {
		return v.ID == created.ID
	})
	if idx < 0 {
		t.Fatalf("bob's list is missing the team monitor (got %d monitors)", len(list))
	}

	// Outsider (registered, but not on this team) does NOT see it.
	resp = env.do(t, http.MethodGet, "/v1/monitors", outsider.AccessToken, nil)
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if slices.ContainsFunc(list, func(v struct {
		ID     int64  `json:"id"`
		Scope  string `json:"scope"`
		TeamID *int64 `json:"team_id"`
	}) bool {
		return v.ID == created.ID
	}) {
		t.Fatal("outsider (not a team member) should not see the team monitor")
	}
	resp = env.do(t, http.MethodGet, fmt.Sprintf("/v1/monitors/%d", created.ID), outsider.AccessToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("outsider GET team monitor: expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()

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

	// Email fanout: the team monitor alerts alice and bob, not the outsider.
	_, emails, err := env.monitorRepo.AlertConfig(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("AlertConfig: %v", err)
	}
	for _, want := range []string{"alice@pantawin.test", "bob@pantawin.test"} {
		if !slices.Contains(emails, want) {
			t.Errorf("team AlertConfig emails %v missing %s", emails, want)
		}
	}
	if slices.Contains(emails, "outsider@pantawin.test") {
		t.Errorf("team AlertConfig emails %v should not include the outsider", emails)
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

	// Scope can be flipped via PATCH; bob loses access once it's personal,
	// and team_id can't be left dangling (it must clear alongside scope).
	resp = env.do(t, http.MethodPatch, fmt.Sprintf("/v1/monitors/%d", created.ID), alice.AccessToken, map[string]any{"scope": "personal"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH scope->personal: expected 200, got %d", resp.StatusCode)
	}
	var afterFlip struct {
		Scope  string `json:"scope"`
		TeamID *int64 `json:"team_id"`
	}
	json.NewDecoder(resp.Body).Decode(&afterFlip)
	resp.Body.Close()
	if afterFlip.Scope != "personal" || afterFlip.TeamID != nil {
		t.Errorf("after scope->personal: scope=%q team_id=%v, want personal/nil", afterFlip.Scope, afterFlip.TeamID)
	}
	resp = env.do(t, http.MethodGet, fmt.Sprintf("/v1/monitors/%d", created.ID), bob.AccessToken, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("after scope->personal, bob GET: expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// team_id sent without scope is rejected, not silently ignored.
	resp = env.do(t, http.MethodPatch, fmt.Sprintf("/v1/monitors/%d", personal.ID), alice.AccessToken,
		map[string]any{"team_id": teamID.ID})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("team_id without scope change: expected 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

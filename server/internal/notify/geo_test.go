package notify

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLookupTarget_LoopbackSkipsGeo(t *testing.T) {
	ip, location := lookupTarget(context.Background(), "http://127.0.0.1:8080/health")
	if ip != "127.0.0.1" {
		t.Errorf("ip = %q, want 127.0.0.1", ip)
	}
	if location != "Private network" {
		t.Errorf("location = %q, want Private network", location)
	}
}

func TestLookupTarget_BadURL(t *testing.T) {
	ip, location := lookupTarget(context.Background(), "not a url")
	if ip != "" || location != "" {
		t.Errorf("bad URL should yield empty enrichment, got %q / %q", ip, location)
	}
}

func TestGeoLocate_FormatsAndCaches(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"status":"success","country":"Indonesia","city":"Jakarta","isp":"Example ISP"}`)
	}))
	defer srv.Close()
	oldEndpoint := geoEndpoint
	geoEndpoint = srv.URL + "/"
	defer func() { geoEndpoint = oldEndpoint }()

	want := "Jakarta, Indonesia · Example ISP"
	if got := geoLocate(context.Background(), "203.0.113.10"); got != want {
		t.Errorf("geoLocate = %q, want %q", got, want)
	}
	// Second lookup must come from the cache, not the API.
	if got := geoLocate(context.Background(), "203.0.113.10"); got != want {
		t.Errorf("cached geoLocate = %q, want %q", got, want)
	}
	if calls != 1 {
		t.Errorf("geo API called %d times, want 1 (cache miss only)", calls)
	}
}

func TestGeoLocate_FailureIsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"fail"}`)
	}))
	defer srv.Close()
	oldEndpoint := geoEndpoint
	geoEndpoint = srv.URL + "/"
	defer func() { geoEndpoint = oldEndpoint }()

	if got := geoLocate(context.Background(), "203.0.113.99"); got != "" {
		t.Errorf("failed lookup should be empty, got %q", got)
	}
}

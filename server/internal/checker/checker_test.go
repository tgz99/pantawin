package checker

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheck_SuccessWithinExpectedRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	result := c.Check(context.Background(), http.MethodGet, srv.URL, 200, 399)

	if !result.OK {
		t.Errorf("expected OK=true, got false (error_type=%q)", result.ErrorType)
	}
	if result.HTTPCode != http.StatusOK {
		t.Errorf("expected HTTP 200, got %d", result.HTTPCode)
	}
	if result.ErrorType != "" {
		t.Errorf("expected empty error_type on success, got %q", result.ErrorType)
	}
	if result.ResponseTimeMS < 0 {
		t.Errorf("expected non-negative response time, got %d", result.ResponseTimeMS)
	}
}

func TestCheck_StatusOutsideExpectedRangeIsNotOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	result := c.Check(context.Background(), http.MethodGet, srv.URL, 200, 399)

	if result.OK {
		t.Error("expected OK=false for a 500 response outside the 200-399 range")
	}
	if result.HTTPCode != http.StatusInternalServerError {
		t.Errorf("expected HTTP 500, got %d", result.HTTPCode)
	}
	if result.ErrorType != ErrorTypeHTTP {
		t.Errorf("expected error_type=%q, got %q", ErrorTypeHTTP, result.ErrorType)
	}
}

func TestCheck_CustomExpectedStatusRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(2 * time.Second)
	// A monitor explicitly expecting 404 (e.g. checking a "page removed"
	// endpoint) should treat it as healthy.
	result := c.Check(context.Background(), http.MethodGet, srv.URL, 404, 404)

	if !result.OK {
		t.Errorf("expected OK=true when 404 is within the configured expected range, got error_type=%q", result.ErrorType)
	}
}

func TestCheck_TimeoutIsClassifiedAsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(10 * time.Millisecond)
	result := c.Check(context.Background(), http.MethodGet, srv.URL, 200, 399)

	if result.OK {
		t.Error("expected OK=false on timeout")
	}
	if result.ErrorType != ErrorTypeTimeout {
		t.Errorf("expected error_type=%q, got %q", ErrorTypeTimeout, result.ErrorType)
	}
}

func TestCheck_DNSFailureIsClassifiedAsDNS(t *testing.T) {
	c := New(2 * time.Second)
	// .invalid is reserved by RFC 2606 and guaranteed to never resolve.
	result := c.Check(context.Background(), http.MethodGet, "http://this-host-does-not-exist.invalid", 200, 399)

	if result.OK {
		t.Error("expected OK=false for an unresolvable host")
	}
	if result.ErrorType != ErrorTypeDNS {
		t.Errorf("expected error_type=%q, got %q", ErrorTypeDNS, result.ErrorType)
	}
}

func TestCheck_ConnectionRefusedIsClassifiedAsConn(t *testing.T) {
	// Bind a listener and immediately close it to get a port that will
	// refuse connections deterministically.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate a test port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	c := New(2 * time.Second)
	result := c.Check(context.Background(), http.MethodGet, "http://"+addr, 200, 399)

	if result.OK {
		t.Error("expected OK=false for a refused connection")
	}
	if result.ErrorType != ErrorTypeConn {
		t.Errorf("expected error_type=%q, got %q", ErrorTypeConn, result.ErrorType)
	}
}

func TestCheck_MalformedURLIsHTTPError(t *testing.T) {
	c := New(2 * time.Second)
	result := c.Check(context.Background(), http.MethodGet, "://not-a-valid-url", 200, 399)

	if result.OK {
		t.Error("expected OK=false for a malformed URL")
	}
	if result.ErrorType != ErrorTypeHTTP {
		t.Errorf("expected error_type=%q, got %q", ErrorTypeHTTP, result.ErrorType)
	}
}

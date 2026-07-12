// Package checker executes HTTP checks against monitored URLs and classifies
// the outcome (spec section 3.2: status, http_code, response_time_ms,
// error_type).
package checker

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"time"
)

const (
	ErrorTypeTimeout = "timeout"
	ErrorTypeDNS     = "dns"
	ErrorTypeTLS     = "tls"
	ErrorTypeConn    = "conn"
	ErrorTypeHTTP    = "http"
)

type Result struct {
	OK             bool
	HTTPCode       int
	ResponseTimeMS int64
	ErrorType      string // empty when OK
}

type Checker struct {
	client *http.Client
}

// New builds a Checker with the given per-request timeout. Redirects are
// followed by the default http.Client policy (up to 10 hops).
func New(timeout time.Duration) *Checker {
	return &Checker{
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Check performs a single HTTP request and classifies the result.
// expectedStatusMin/Max form an inclusive range (spec default 200-399).
func (c *Checker) Check(ctx context.Context, method, url string, expectedStatusMin, expectedStatusMax int) Result {
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		// Malformed method/URL — treat as an HTTP-layer failure, not a
		// network error, since it never left the process.
		return Result{OK: false, ErrorType: ErrorTypeHTTP}
	}

	resp, err := c.client.Do(req)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		return Result{OK: false, ResponseTimeMS: elapsed, ErrorType: classifyError(err)}
	}
	defer resp.Body.Close()

	ok := resp.StatusCode >= expectedStatusMin && resp.StatusCode <= expectedStatusMax
	res := Result{
		OK:             ok,
		HTTPCode:       resp.StatusCode,
		ResponseTimeMS: elapsed,
	}
	if !ok {
		res.ErrorType = ErrorTypeHTTP
	}
	return res
}

func classifyError(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrorTypeTimeout
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrorTypeDNS
	}

	var tlsErr *tls.CertificateVerificationError
	if errors.As(err, &tlsErr) {
		return ErrorTypeTLS
	}
	var recordHdrErr tls.RecordHeaderError
	if errors.As(err, &recordHdrErr) {
		return ErrorTypeTLS
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return ErrorTypeConn
	}

	return ErrorTypeConn
}

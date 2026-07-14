package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Best-effort target enrichment for alert emails: the monitored host's IP and
// a human-readable location ("City, Country · ISP"). Everything here is
// advisory — a DNS or geo failure must never block or fail an alert send, so
// errors degrade to empty strings and the email simply omits those rows.

const (
	dnsLookupTimeout = 3 * time.Second
	geoLookupTimeout = 4 * time.Second
)

// geoEndpoint is ip-api.com's free JSON API (HTTP-only on the free tier —
// fine, geo of a public server IP isn't sensitive). Var so tests can stub it.
var geoEndpoint = "http://ip-api.com/json/"

// geoCache memoizes location by IP for the life of the process. Monitor IPs
// are near-static and alert volume is tiny, so no TTL/eviction is needed;
// it also keeps the retrier from re-hitting the rate-limited free API.
var geoCache sync.Map // ip string -> location string

// lookupTarget resolves the monitor URL's host to an IP and geolocates it.
// Either value may be "" on failure.
func lookupTarget(ctx context.Context, monitorURL string) (ip, location string) {
	u, err := url.Parse(monitorURL)
	if err != nil || u.Hostname() == "" {
		return "", ""
	}
	host := u.Hostname()

	dnsCtx, cancel := context.WithTimeout(ctx, dnsLookupTimeout)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIP(dnsCtx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return "", ""
	}
	// Prefer IPv4 — it's what the checker dials and what users recognize.
	chosen := addrs[0]
	for _, a := range addrs {
		if a.To4() != nil {
			chosen = a
			break
		}
	}
	ip = chosen.String()

	if chosen.IsPrivate() || chosen.IsLoopback() {
		return ip, "Private network"
	}
	return ip, geoLocate(ctx, ip)
}

func geoLocate(ctx context.Context, ip string) string {
	if cached, ok := geoCache.Load(ip); ok {
		return cached.(string)
	}

	geoCtx, cancel := context.WithTimeout(ctx, geoLookupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(
		geoCtx, http.MethodGet,
		geoEndpoint+url.PathEscape(ip)+"?fields=status,country,city,isp", nil,
	)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var body struct {
		Status  string `json:"status"`
		Country string `json:"country"`
		City    string `json:"city"`
		ISP     string `json:"isp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != "success" {
		return ""
	}

	location := body.Country
	if body.City != "" && body.Country != "" {
		location = fmt.Sprintf("%s, %s", body.City, body.Country)
	} else if body.City != "" {
		location = body.City
	}
	if body.ISP != "" && location != "" {
		location = fmt.Sprintf("%s · %s", location, body.ISP)
	}
	// Cache only successful lookups; failures should retry next alert.
	if location != "" {
		geoCache.Store(ip, location)
	}
	return location
}

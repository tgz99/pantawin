package notify

import (
	"strings"
	"testing"
	"time"
)

func TestRenderEmail_Down(t *testing.T) {
	c, err := renderEmail(IncidentEvent{
		MonitorName: "gratisaja.com", MonitorURL: "https://gratisaja.com",
		EventType: EventDown, Cause: "timeout", At: time.Unix(0, 0).UTC(),
		StartedAt: time.Date(2026, 7, 14, 3, 2, 1, 0, time.UTC),
		DeepLink:  "pantawin://monitor/1",
	}, "103.181.182.61", "Jakarta, Indonesia · Example ISP")
	if err != nil {
		t.Fatalf("renderEmail: %v", err)
	}
	if !strings.Contains(c.Subject, "DOWN") || !strings.Contains(c.Subject, "gratisaja.com") {
		t.Errorf("unexpected subject: %q", c.Subject)
	}
	for _, want := range []string{
		"gratisaja.com", "https://gratisaja.com", "timeout", "pantawin://monitor/1",
		"2026-07-14 03:02:01 UTC", "103.181.182.61", "Jakarta, Indonesia",
	} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("DOWN email missing %q", want)
		}
	}
}

// Payloads claimed before the StartedAt/enrichment upgrade replay through the
// retrier with zero/empty values — every new row must drop out cleanly.
func TestRenderEmail_Down_OmitsEmptyEnrichment(t *testing.T) {
	c, err := renderEmail(IncidentEvent{
		MonitorName: "gratisaja.com", MonitorURL: "https://gratisaja.com",
		EventType: EventDown, Cause: "timeout", At: time.Unix(0, 0).UTC(),
		DeepLink: "pantawin://monitor/1",
	}, "", "")
	if err != nil {
		t.Fatalf("renderEmail: %v", err)
	}
	for _, absent := range []string{"Incident started", ">IP<", "Location"} {
		if strings.Contains(c.HTML, absent) {
			t.Errorf("DOWN email should omit %q when unknown, got:\n%s", absent, c.HTML)
		}
	}
}

func TestRenderEmail_Recovered(t *testing.T) {
	c, err := renderEmail(IncidentEvent{
		MonitorName: "gratisaja.com", MonitorURL: "https://gratisaja.com",
		EventType: EventRecovered, Cause: "timeout", At: time.Unix(0, 0).UTC(),
		StartedAt:    time.Date(2026, 7, 14, 3, 2, 1, 0, time.UTC),
		DownDuration: 90 * time.Second, DeepLink: "pantawin://monitor/1",
	}, "103.181.182.61", "Jakarta, Indonesia")
	if err != nil {
		t.Fatalf("renderEmail: %v", err)
	}
	if !strings.Contains(c.Subject, "recovered") {
		t.Errorf("unexpected subject: %q", c.Subject)
	}
	for _, want := range []string{"1m 30s", "timeout", "2026-07-14 03:02:01 UTC", "103.181.182.61", "Jakarta, Indonesia"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("RECOVERED email missing %q, got:\n%s", want, c.HTML)
		}
	}
}

func TestHumanizeDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                       "unknown",
		45 * time.Second:        "45s",
		90 * time.Second:        "1m 30s",
		2 * time.Hour + 5*time.Minute + 3*time.Second: "2h 5m 3s",
	}
	for d, want := range cases {
		if got := humanizeDuration(d); got != want {
			t.Errorf("humanizeDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestBuildMIME_HasHTMLContentType(t *testing.T) {
	msg := string(buildMIME("alerts@pantawin.gratisaja.com", []string{"user@example.com", "mate@example.com"}, "Subj", "<b>hi</b>"))
	for _, want := range []string{
		"From: Pantawin <alerts@pantawin.gratisaja.com>",
		"To: user@example.com, mate@example.com",
		"Subject: Subj",
		"Content-Type: text/html; charset=UTF-8",
		"<b>hi</b>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("MIME message missing %q", want)
		}
	}
}

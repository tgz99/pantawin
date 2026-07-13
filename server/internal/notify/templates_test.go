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
		DeepLink: "pantawin://monitor/1",
	})
	if err != nil {
		t.Fatalf("renderEmail: %v", err)
	}
	if !strings.Contains(c.Subject, "DOWN") || !strings.Contains(c.Subject, "gratisaja.com") {
		t.Errorf("unexpected subject: %q", c.Subject)
	}
	for _, want := range []string{"gratisaja.com", "https://gratisaja.com", "timeout", "pantawin://monitor/1"} {
		if !strings.Contains(c.HTML, want) {
			t.Errorf("DOWN email missing %q", want)
		}
	}
}

func TestRenderEmail_Recovered(t *testing.T) {
	c, err := renderEmail(IncidentEvent{
		MonitorName: "gratisaja.com", MonitorURL: "https://gratisaja.com",
		EventType: EventRecovered, At: time.Unix(0, 0).UTC(),
		DownDuration: 90 * time.Second, DeepLink: "pantawin://monitor/1",
	})
	if err != nil {
		t.Fatalf("renderEmail: %v", err)
	}
	if !strings.Contains(c.Subject, "recovered") {
		t.Errorf("unexpected subject: %q", c.Subject)
	}
	if !strings.Contains(c.HTML, "1m 30s") {
		t.Errorf("RECOVERED email should show humanized downtime 1m 30s, got:\n%s", c.HTML)
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
	msg := string(buildMIME("alerts@pantawin.gratisaja.com", "user@example.com", "Subj", "<b>hi</b>"))
	for _, want := range []string{
		"From: Pantawin <alerts@pantawin.gratisaja.com>",
		"To: user@example.com",
		"Subject: Subj",
		"Content-Type: text/html; charset=UTF-8",
		"<b>hi</b>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("MIME message missing %q", want)
		}
	}
}

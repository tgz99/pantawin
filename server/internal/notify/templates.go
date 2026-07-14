package notify

import (
	"bytes"
	"fmt"
	"html/template"
	"time"
)

// Email templates are inline table-based HTML with inline styles — the only
// layout approach that renders consistently across mail clients (Gmail,
// Outlook, Apple Mail all strip <style> blocks and flex/grid).

var downTemplate = template.Must(template.New("down").Parse(`
<div style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;max-width:520px;margin:0 auto">
  <table width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse">
    <tr><td style="background:#d32f2f;color:#fff;padding:16px 20px;font-size:18px;font-weight:600;border-radius:8px 8px 0 0">
      ⛔ {{.MonitorName}} is DOWN
    </td></tr>
    <tr><td style="border:1px solid #eee;border-top:0;padding:20px;border-radius:0 0 8px 8px">
      <p style="margin:0 0 12px;color:#333">Pantawin detected an outage:</p>
      <table cellpadding="6" cellspacing="0" style="font-size:14px;color:#444">
        <tr><td style="color:#888">Monitor</td><td><strong>{{.MonitorName}}</strong></td></tr>
        <tr><td style="color:#888">URL</td><td>{{.MonitorURL}}</td></tr>
        <tr><td style="color:#888">Reason</td><td>{{.Cause}}</td></tr>
        {{if .StartedAt}}<tr><td style="color:#888">Incident started</td><td>{{.StartedAt}}</td></tr>{{end}}
        <tr><td style="color:#888">Detected</td><td>{{.At}}</td></tr>
        {{if .IP}}<tr><td style="color:#888">IP</td><td>{{.IP}}</td></tr>{{end}}
        {{if .Location}}<tr><td style="color:#888">Location</td><td>{{.Location}}</td></tr>{{end}}
      </table>
      <p style="margin:16px 0 0">
        <a href="{{.DeepLink}}" style="background:#d32f2f;color:#fff;text-decoration:none;padding:10px 18px;border-radius:6px;display:inline-block;font-size:14px">Open in Pantawin</a>
      </p>
    </td></tr>
  </table>
</div>`))

var recoveredTemplate = template.Must(template.New("recovered").Parse(`
<div style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;max-width:520px;margin:0 auto">
  <table width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse">
    <tr><td style="background:#2e7d32;color:#fff;padding:16px 20px;font-size:18px;font-weight:600;border-radius:8px 8px 0 0">
      ✅ {{.MonitorName}} has RECOVERED
    </td></tr>
    <tr><td style="border:1px solid #eee;border-top:0;padding:20px;border-radius:0 0 8px 8px">
      <p style="margin:0 0 12px;color:#333">The monitor is back up.</p>
      <table cellpadding="6" cellspacing="0" style="font-size:14px;color:#444">
        <tr><td style="color:#888">Monitor</td><td><strong>{{.MonitorName}}</strong></td></tr>
        <tr><td style="color:#888">URL</td><td>{{.MonitorURL}}</td></tr>
        {{if .Cause}}<tr><td style="color:#888">Reason</td><td>{{.Cause}}</td></tr>{{end}}
        {{if .StartedAt}}<tr><td style="color:#888">Incident started</td><td>{{.StartedAt}}</td></tr>{{end}}
        <tr><td style="color:#888">Downtime</td><td>{{.Downtime}}</td></tr>
        <tr><td style="color:#888">Recovered</td><td>{{.At}}</td></tr>
        {{if .IP}}<tr><td style="color:#888">IP</td><td>{{.IP}}</td></tr>{{end}}
        {{if .Location}}<tr><td style="color:#888">Location</td><td>{{.Location}}</td></tr>{{end}}
      </table>
      <p style="margin:16px 0 0">
        <a href="{{.DeepLink}}" style="background:#2e7d32;color:#fff;text-decoration:none;padding:10px 18px;border-radius:6px;display:inline-block;font-size:14px">Open in Pantawin</a>
      </p>
    </td></tr>
  </table>
</div>`))

type emailContent struct {
	Subject string
	HTML    string
}

// renderEmail renders the alert body. ip and location are best-effort
// enrichment (see lookupTarget) — empty strings omit their rows.
func renderEmail(event IncidentEvent, ip, location string) (emailContent, error) {
	// StartedAt is zero on payloads queued before the field existed (retries
	// of pre-upgrade rows) — render "" so the template drops the row.
	startedAt := ""
	if !event.StartedAt.IsZero() {
		startedAt = event.StartedAt.UTC().Format("2006-01-02 15:04:05 UTC")
	}
	data := struct {
		MonitorName string
		MonitorURL  string
		Cause       string
		At          string
		StartedAt   string
		Downtime    string
		IP          string
		Location    string
		// template.URL: the deep link uses our own pantawin:// scheme, which
		// html/template would otherwise reject as unsafe and replace with
		// "#ZgotmplZ". This value is server-constructed from a monitor ID,
		// never user input, so marking it trusted is safe.
		DeepLink template.URL
	}{
		MonitorName: event.MonitorName,
		MonitorURL:  event.MonitorURL,
		Cause:       event.Cause,
		At:          event.At.UTC().Format("2006-01-02 15:04:05 UTC"),
		StartedAt:   startedAt,
		Downtime:    humanizeDuration(event.DownDuration),
		IP:          ip,
		Location:    location,
		DeepLink:    template.URL(event.DeepLink),
	}

	var buf bytes.Buffer
	var subject string
	switch event.EventType {
	case EventDown:
		subject = fmt.Sprintf("⛔ %s is DOWN", event.MonitorName)
		if err := downTemplate.Execute(&buf, data); err != nil {
			return emailContent{}, err
		}
	case EventRecovered:
		subject = fmt.Sprintf("✅ %s recovered", event.MonitorName)
		if err := recoveredTemplate.Execute(&buf, data); err != nil {
			return emailContent{}, err
		}
	default:
		return emailContent{}, fmt.Errorf("unknown event type %q", event.EventType)
	}
	return emailContent{Subject: subject, HTML: buf.String()}, nil
}

func humanizeDuration(d time.Duration) string {
	if d <= 0 {
		return "unknown"
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

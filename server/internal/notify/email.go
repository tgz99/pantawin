package notify

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"
)

// EmailChannel delivers via a local SMTP relay — the self-hosted Postfix
// listening on 127.0.0.1:25 (spec section 3.2b). Because the relay is
// loopback-only and trusts localhost (mynetworks = 127.0.0.0/8), no SASL
// auth is needed; that's why this is deliberately a plain net/smtp
// SendMail with nil auth.
type EmailChannel struct {
	smtpAddr string // "127.0.0.1:25"
	from     string // "alerts@pantawin.gratisaja.com"
}

func NewEmailChannel(smtpAddr, from string) *EmailChannel {
	return &EmailChannel{smtpAddr: smtpAddr, from: from}
}

func (c *EmailChannel) Name() string { return "email" }

func (c *EmailChannel) Send(ctx context.Context, event IncidentEvent, target ChannelTarget) error {
	if target.Email == "" {
		return fmt.Errorf("email channel: empty target address")
	}
	content, err := renderEmail(event)
	if err != nil {
		return fmt.Errorf("render email: %w", err)
	}

	msg := buildMIME(c.from, target.Email, content.Subject, content.HTML)

	// net/smtp has no context support; the caller bounds this with a
	// worker-level timeout. Postfix on loopback responds in milliseconds.
	if err := smtp.SendMail(c.smtpAddr, nil, c.from, []string{target.Email}, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

func buildMIME(from, to, subject, htmlBody string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: Pantawin <%s>\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	return []byte(b.String())
}

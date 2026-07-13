package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Postfix on loopback responds in milliseconds; these bounds only matter
// when the relay is down/missing, so a sibling channel (push) isn't held
// up behind a hanging TCP connect (observed: ~130s kernel connect timeout).
const (
	smtpDialTimeout  = 5 * time.Second
	smtpTotalTimeout = 30 * time.Second
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

	// net/smtp has no context support, so bound the dial and the whole
	// SMTP conversation with explicit deadlines instead of smtp.SendMail.
	conn, err := net.DialTimeout("tcp", c.smtpAddr, smtpDialTimeout)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(smtpTotalTimeout))

	host, _, err := net.SplitHostPort(c.smtpAddr)
	if err != nil {
		host = c.smtpAddr
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()

	if err := client.Mail(c.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(target.Email); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	return client.Quit()
}

func buildMIME(from, to, subject, htmlBody string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: Pantawin <%s>\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	// Date and Message-ID are required for acceptance at the majors —
	// Gmail hard-rejects mail without a Message-ID (550 5.7.1).
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", messageID(), domainOf(from))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(htmlBody)
	return []byte(b.String())
}

func messageID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Fall back to a timestamp; uniqueness per-second is enough here.
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return addr[i+1:]
	}
	return "localhost"
}

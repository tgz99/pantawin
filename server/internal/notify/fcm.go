package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2/google"
)

// FcmChannel delivers push notifications via the FCM HTTP v1 API (spec 3.2b).
//
// It is INACTIVE until a service-account credential is provided: NewFcmChannel
// returns (nil, nil) when credentialsJSON is empty, and the caller simply
// doesn't register the "push" channel — monitors requesting push are then
// skipped cleanly rather than erroring. This mirrors the M2 Postfix pattern:
// the code ships wired but dormant, and activates when you drop in Firebase
// credentials (FCM_CREDENTIALS_JSON + FCM_PROJECT_ID).
type FcmChannel struct {
	projectID  string
	httpClient *http.Client
	// resolveTokens maps a user to their registered device tokens; injected
	// so notify doesn't import device.
	resolveTokens func(ctx context.Context, monitorID int64) (userID int64, tokens []string, err error)
	// onStaleToken removes a token FCM reports as unregistered.
	onStaleToken func(ctx context.Context, token string)
}

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// NewFcmChannel builds an FCM channel from a service-account JSON. Returns
// (nil, nil) when credentialsJSON is empty (push not configured) so callers
// can conditionally register it.
func NewFcmChannel(
	ctx context.Context,
	credentialsJSON []byte,
	projectID string,
	resolveTokens func(ctx context.Context, monitorID int64) (int64, []string, error),
	onStaleToken func(ctx context.Context, token string),
) (*FcmChannel, error) {
	if len(credentialsJSON) == 0 || projectID == "" {
		return nil, nil // not configured — dormant
	}
	creds, err := google.CredentialsFromJSON(ctx, credentialsJSON, fcmScope)
	if err != nil {
		return nil, fmt.Errorf("fcm: parse service account: %w", err)
	}
	return &FcmChannel{
		projectID:     projectID,
		httpClient:    oauth2HTTPClient(ctx, creds),
		resolveTokens: resolveTokens,
		onStaleToken:  onStaleToken,
	}, nil
}

func (c *FcmChannel) Name() string { return "push" }

// Send fans the notification out to all of the owning user's device tokens.
// The ChannelTarget carries the email for the email channel and is unused
// here — push routing is by user, resolved from the monitor.
func (c *FcmChannel) Send(ctx context.Context, event IncidentEvent, _ ChannelTarget) error {
	_, tokens, err := c.resolveTokens(ctx, event.MonitorID)
	if err != nil {
		return fmt.Errorf("fcm: resolve device tokens: %w", err)
	}
	if len(tokens) == 0 {
		return nil // no devices registered — nothing to do, not an error
	}

	title, body := pushText(event)
	var firstErr error
	for _, token := range tokens {
		if err := c.sendOne(ctx, token, title, body, event); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func pushText(event IncidentEvent) (title, body string) {
	if event.EventType == EventDown {
		return fmt.Sprintf("%s is DOWN", event.MonitorName), fmt.Sprintf("Reason: %s", event.Cause)
	}
	return fmt.Sprintf("%s recovered", event.MonitorName), fmt.Sprintf("Down for %s", humanizeDuration(event.DownDuration))
}

// fcmMessage is the HTTP v1 request shape. A data-only high-priority message
// (spec 6.4) so the Android client renders its own notification with the
// right channel/sound and can deep-link.
func (c *FcmChannel) sendOne(ctx context.Context, token, title, body string, event IncidentEvent) error {
	payload := map[string]any{
		"message": map[string]any{
			"token": token,
			"data": map[string]string{
				"type":         "incident",
				"monitor_id":   fmt.Sprintf("%d", event.MonitorID),
				"monitor_name": event.MonitorName,
				"status":       string(event.EventType),
				"reason":       event.Cause,
				"title":        title,
				"body":         body,
				"deep_link":    event.DeepLink,
			},
			"android": map[string]any{"priority": "high"},
		},
	}
	raw, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", c.projectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fcm: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}
	respBody, _ := io.ReadAll(resp.Body)
	// 404 UNREGISTERED / 400 with that token means the token is stale — prune it.
	if resp.StatusCode == http.StatusNotFound && c.onStaleToken != nil {
		c.onStaleToken(ctx, token)
	}
	return fmt.Errorf("fcm: send returned %d: %s", resp.StatusCode, string(respBody))
}

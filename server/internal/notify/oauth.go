package notify

import (
	"context"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// oauth2HTTPClient returns an *http.Client that automatically attaches (and
// refreshes) an OAuth2 bearer token derived from the service-account
// credentials — this is how FCM HTTP v1 is authenticated.
func oauth2HTTPClient(ctx context.Context, creds *google.Credentials) *http.Client {
	return oauth2.NewClient(ctx, creds.TokenSource)
}

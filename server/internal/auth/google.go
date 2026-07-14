package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"google.golang.org/api/idtoken"
)

// ErrGoogleNotConfigured is returned when Google sign-in is attempted but no
// OAuth client ID is configured (GOOGLE_CLIENT_ID unset — dormant, like FCM).
var ErrGoogleNotConfigured = errors.New("google sign-in is not configured")

// ErrGoogleTokenInvalid is returned for an ID token that fails verification.
var ErrGoogleTokenInvalid = errors.New("invalid google id token")

// GoogleIdentity is the subset of Google ID-token claims we act on.
type GoogleIdentity struct {
	Email    string
	Verified bool
}

// GoogleVerifier validates a Google ID token and extracts the identity.
// Injected into Service so tests can fake it without Google's JWKS.
type GoogleVerifier func(ctx context.Context, rawIDToken string) (GoogleIdentity, error)

// NewGoogleVerifier verifies tokens against Google's public keys, requiring
// our OAuth client ID as the audience. Returns nil when clientID is empty so
// the caller can leave the feature dormant.
func NewGoogleVerifier(clientID string) GoogleVerifier {
	if clientID == "" {
		return nil
	}
	return func(ctx context.Context, rawIDToken string) (GoogleIdentity, error) {
		payload, err := idtoken.Validate(ctx, rawIDToken, clientID)
		if err != nil {
			return GoogleIdentity{}, fmt.Errorf("%w: %s", ErrGoogleTokenInvalid, err)
		}
		email, _ := payload.Claims["email"].(string)
		verified, _ := payload.Claims["email_verified"].(bool)
		return GoogleIdentity{Email: email, Verified: verified}, nil
	}
}

// WithGoogleVerifier installs the verifier (nil = feature dormant).
func (s *Service) WithGoogleVerifier(v GoogleVerifier) *Service {
	s.googleVerify = v
	return s
}

// GoogleLogin exchanges a verified Google ID token for a Pantawin session.
// The account is keyed by email: an existing account (e.g. the bootstrap
// admin) is simply logged into; an unknown email gets a new account with an
// unguessable random password (the user can set a real one in the app).
// Only verified Google emails are accepted — an unverified email could be
// claimed by anyone at signup time.
func (s *Service) GoogleLogin(ctx context.Context, rawIDToken string) (Tokens, error) {
	if s.googleVerify == nil {
		return Tokens{}, ErrGoogleNotConfigured
	}
	identity, err := s.googleVerify(ctx, rawIDToken)
	if err != nil {
		return Tokens{}, err
	}
	if identity.Email == "" || !identity.Verified {
		return Tokens{}, ErrGoogleTokenInvalid
	}

	user, err := s.repo.GetUserByEmail(ctx, identity.Email)
	if errors.Is(err, ErrUserNotFound) {
		// The allowlist gates only NEW accounts — existing users (however
		// they registered) can always sign in with Google.
		if !s.signupAllowed(identity.Email) {
			return Tokens{}, ErrSignupNotAllowed
		}
		hash, hashErr := HashPassword(randomPassword())
		if hashErr != nil {
			return Tokens{}, fmt.Errorf("hash placeholder password: %w", hashErr)
		}
		user, err = s.repo.CreateUser(ctx, identity.Email, hash)
	}
	if err != nil {
		return Tokens{}, err
	}
	return s.issueTokens(ctx, user.ID)
}

// randomPassword generates an unguessable placeholder for accounts created
// via Google — password login stays effectively disabled until the user
// sets one via change-password.
func randomPassword() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

package auth

import (
	"testing"
	"time"
)

func newTestIssuer() *TokenIssuer {
	return NewTokenIssuer("test-secret-do-not-use-in-prod", 15*time.Minute, 30*24*time.Hour)
}

func TestIssueAndParseAccessToken_RoundTrip(t *testing.T) {
	issuer := newTestIssuer()
	token, err := issuer.IssueAccessToken(42)
	if err != nil {
		t.Fatalf("IssueAccessToken returned error: %v", err)
	}

	userID, err := issuer.ParseAccessToken(token)
	if err != nil {
		t.Fatalf("ParseAccessToken returned error: %v", err)
	}
	if userID != 42 {
		t.Errorf("expected userID 42, got %d", userID)
	}
}

func TestIssueAndParseRefreshToken_RoundTrip(t *testing.T) {
	issuer := newTestIssuer()
	token, err := issuer.IssueRefreshToken(7)
	if err != nil {
		t.Fatalf("IssueRefreshToken returned error: %v", err)
	}

	userID, err := issuer.ParseRefreshToken(token)
	if err != nil {
		t.Fatalf("ParseRefreshToken returned error: %v", err)
	}
	if userID != 7 {
		t.Errorf("expected userID 7, got %d", userID)
	}
}

func TestParseAccessToken_RejectsRefreshToken(t *testing.T) {
	issuer := newTestIssuer()
	refreshToken, err := issuer.IssueRefreshToken(1)
	if err != nil {
		t.Fatalf("IssueRefreshToken returned error: %v", err)
	}

	if _, err := issuer.ParseAccessToken(refreshToken); err != ErrWrongTokenType {
		t.Errorf("expected ErrWrongTokenType when parsing a refresh token as an access token, got %v", err)
	}
}

func TestParseRefreshToken_RejectsAccessToken(t *testing.T) {
	issuer := newTestIssuer()
	accessToken, err := issuer.IssueAccessToken(1)
	if err != nil {
		t.Fatalf("IssueAccessToken returned error: %v", err)
	}

	if _, err := issuer.ParseRefreshToken(accessToken); err != ErrWrongTokenType {
		t.Errorf("expected ErrWrongTokenType when parsing an access token as a refresh token, got %v", err)
	}
}

func TestParseAccessToken_RejectsTokenSignedWithDifferentSecret(t *testing.T) {
	issuer := newTestIssuer()
	otherIssuer := NewTokenIssuer("a-different-secret", 15*time.Minute, 30*24*time.Hour)

	token, err := otherIssuer.IssueAccessToken(1)
	if err != nil {
		t.Fatalf("IssueAccessToken returned error: %v", err)
	}

	if _, err := issuer.ParseAccessToken(token); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for a token signed with a different secret, got %v", err)
	}
}

func TestParseAccessToken_RejectsExpiredToken(t *testing.T) {
	// Issue a token that already expired 1 second ago by using a negative TTL.
	issuer := NewTokenIssuer("test-secret-do-not-use-in-prod", -1*time.Second, 30*24*time.Hour)
	token, err := issuer.IssueAccessToken(1)
	if err != nil {
		t.Fatalf("IssueAccessToken returned error: %v", err)
	}

	if _, err := issuer.ParseAccessToken(token); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for an expired token, got %v", err)
	}
}

func TestParseAccessToken_RejectsGarbage(t *testing.T) {
	issuer := newTestIssuer()
	if _, err := issuer.ParseAccessToken("not-a-jwt-at-all"); err != ErrInvalidToken {
		t.Errorf("expected ErrInvalidToken for garbage input, got %v", err)
	}
}

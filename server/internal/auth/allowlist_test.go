package auth

import (
	"context"
	"errors"
	"testing"
)

// The allowlist gate runs before any repo access, so a nil-repo Service is
// enough: a blocked email returns ErrSignupNotAllowed immediately, and an
// allowed email proceeds to password validation (ErrWeakPassword proves the
// gate was passed without needing a database).
func TestSignupAllowlist(t *testing.T) {
	cases := []struct {
		name      string
		allowlist []string
		email     string
		allowed   bool
	}{
		{"empty list keeps signup open", nil, "anyone@anywhere.com", true},
		{"exact email allowed", []string{"boss@corp.com"}, "boss@corp.com", true},
		{"exact email case-insensitive", []string{"boss@corp.com"}, "Boss@Corp.com", true},
		{"unlisted email blocked", []string{"boss@corp.com"}, "intruder@evil.com", false},
		{"domain entry allows whole domain", []string{"@corp.com"}, "newhire@corp.com", true},
		{"domain entry blocks other domains", []string{"@corp.com"}, "x@corp.com.evil.com", false},
		{"mixed entries", []string{"@corp.com", "contractor@gmail.com"}, "contractor@gmail.com", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(nil, nil, nil, 0).WithSignupAllowlist(tc.allowlist)
			_, err := svc.Register(context.Background(), tc.email, "weak")
			if tc.allowed {
				// Passed the gate: the weak password is what stops it.
				if !errors.Is(err, ErrWeakPassword) {
					t.Fatalf("expected ErrWeakPassword after passing allowlist, got %v", err)
				}
			} else if !errors.Is(err, ErrSignupNotAllowed) {
				t.Fatalf("expected ErrSignupNotAllowed, got %v", err)
			}
		})
	}
}

// With the invite store wired (M6.1), signup is closed-by-default: only env
// entries or store-approved (invited) emails get through.
func TestSignupAllowlistStore(t *testing.T) {
	store := func(_ context.Context, email string) (bool, error) {
		return email == "invited@corp.com", nil
	}

	svc := NewService(nil, nil, nil, 0).WithSignupAllowlistStore(store)

	if _, err := svc.Register(context.Background(), "stranger@evil.com", "weak"); !errors.Is(err, ErrSignupNotAllowed) {
		t.Errorf("uninvited email with store wired: expected ErrSignupNotAllowed, got %v", err)
	}
	if _, err := svc.Register(context.Background(), "Invited@Corp.com", "weak"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("invited email should pass the gate (case-insensitive), got %v", err)
	}

	// Env entries still work alongside the store.
	svc = NewService(nil, nil, nil, 0).
		WithSignupAllowlist([]string{"@corp.com"}).
		WithSignupAllowlistStore(func(context.Context, string) (bool, error) { return false, nil })
	if _, err := svc.Register(context.Background(), "env@corp.com", "weak"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("env-allowlisted email should pass despite store denial, got %v", err)
	}
}

package auth

import "testing"

func TestHashPassword_VerifyPassword_RoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned empty hash")
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Error("VerifyPassword should accept the correct plaintext password")
	}
}

func TestVerifyPassword_RejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if VerifyPassword(hash, "wrong password") {
		t.Error("VerifyPassword should reject an incorrect plaintext password")
	}
}

func TestHashPassword_ProducesDifferentHashesForSameInput(t *testing.T) {
	// bcrypt salts internally — two hashes of the same password must differ,
	// otherwise a rainbow-table style attack becomes viable.
	h1, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	h2, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if h1 == h2 {
		t.Error("expected different salted hashes for the same plaintext password")
	}
}

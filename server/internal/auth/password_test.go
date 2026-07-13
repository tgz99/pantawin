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

func TestValidatePasswordPolicy(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantOK   bool
	}{
		{"valid", "Abcdefg1", true},
		{"valid long", "Sup3r-Secret-Passphrase", true},
		{"too short", "Abc1", false},
		{"exactly 7", "Abcdef1", false},
		{"no uppercase", "abcdefg1", false},
		{"no digit", "Abcdefgh", false},
		{"empty", "", false},
		{"digits and upper only", "PASSW0RD", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePasswordPolicy(tc.password)
			if tc.wantOK && err != nil {
				t.Errorf("expected %q to pass policy, got %v", tc.password, err)
			}
			if !tc.wantOK && err == nil {
				t.Errorf("expected %q to fail policy", tc.password)
			}
		})
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

package auth

import (
	"errors"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// ErrWeakPassword is returned when a password fails the policy below.
var ErrWeakPassword = errors.New("password must be at least 8 characters with at least one uppercase letter and one number")

// ValidatePasswordPolicy enforces the account password policy: minimum 8
// characters, at least one uppercase letter, at least one digit. Enforced
// server-side so no client can skip it.
func ValidatePasswordPolicy(plain string) error {
	if len(plain) < 8 {
		return ErrWeakPassword
	}
	var hasUpper, hasDigit bool
	for _, r := range plain {
		switch {
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	if !hasUpper || !hasDigit {
		return ErrWeakPassword
	}
	return nil
}

func HashPassword(plain string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func VerifyPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

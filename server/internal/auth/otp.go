package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// M6.2: email/password registration is gated behind a one-time code sent to
// the address, so a typo'd or someone-else's email can't be registered
// without also controlling that inbox. Google sign-in skips this — Google
// already vouches for the email.
const (
	defaultOTPTTL            = 10 * time.Minute
	defaultOTPResendCooldown = 60 * time.Second
	maxOTPAttempts           = 5
)

var (
	// ErrOTPInvalid covers both "no such code" and "wrong code" — never
	// reveal which, so a code can't be brute-forced-probed for existence.
	ErrOTPInvalid = errors.New("invalid verification code")
	// ErrOTPExpired covers expiry and attempt exhaustion alike: either way
	// the caller must request a new code.
	ErrOTPExpired = errors.New("verification code expired — request a new one")
	// ErrOTPResendTooSoon rate-limits resend-otp per email, independent of
	// the per-IP auth rate limiter.
	ErrOTPResendTooSoon = errors.New("please wait before requesting another code")
)

// WithOTPTuning overrides the OTP TTL and resend cooldown. Tests only —
// production uses the package defaults.
func (r *Repository) WithOTPTuning(ttl, resendCooldown time.Duration) *Repository {
	r.otpTTL = ttl
	r.otpResendCooldown = resendCooldown
	return r
}

// IssueOTP generates a fresh 6-digit code for email, stores its hash, and
// returns the plaintext for the caller to send. A request within the cooldown
// window of the previous code yields ErrOTPResendTooSoon — used by the
// explicit "resend" action, which is what the cooldown is meant to throttle.
func (r *Repository) IssueOTP(ctx context.Context, email string) (string, error) {
	return r.issueOTP(ctx, email, false)
}

// IssueOTPBypassingCooldown always issues a fresh code, ignoring the resend
// cooldown. Used by Register: re-registering an email still pending
// verification (typo'd password, lost the first email) must always send a
// working code — the cooldown exists to throttle the explicit resend button,
// not to trap a legitimate re-registration attempt. Register-spam against one
// address is instead bounded by the per-IP auth rate limiter.
func (r *Repository) IssueOTPBypassingCooldown(ctx context.Context, email string) (string, error) {
	return r.issueOTP(ctx, email, true)
}

func (r *Repository) issueOTP(ctx context.Context, email string, bypassCooldown bool) (string, error) {
	code, err := generateOTPCode()
	if err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}
	// Durations ride as seconds + make_interval() — pgx has no built-in
	// time.Duration->interval mapping (same pattern as notify's retrier).
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO email_otps (email, code_hash, expires_at, attempts, created_at)
		VALUES ($1, $2, now() + make_interval(secs => $3), 0, now())
		ON CONFLICT (email) DO UPDATE SET
			code_hash  = EXCLUDED.code_hash,
			expires_at = EXCLUDED.expires_at,
			attempts   = 0,
			created_at = now()
		WHERE $5 OR email_otps.created_at < now() - make_interval(secs => $4)
	`, email, hashOTP(code), r.otpTTL.Seconds(), r.otpResendCooldown.Seconds(), bypassCooldown)
	if err != nil {
		return "", fmt.Errorf("issue otp: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// A row already existed and the cooldown WHERE excluded the update —
		// distinct from "brand new row", which always affects exactly 1.
		return "", ErrOTPResendTooSoon
	}
	return code, nil
}

// VerifyOTP checks code against the stored hash for email. On success the
// row is consumed (deleted) so the code can't be replayed.
func (r *Repository) VerifyOTP(ctx context.Context, email, code string) error {
	var hash string
	var expiresAt time.Time
	var attempts int
	err := r.pool.QueryRow(ctx,
		`SELECT code_hash, expires_at, attempts FROM email_otps WHERE email = $1`, email,
	).Scan(&hash, &expiresAt, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOTPInvalid
	}
	if err != nil {
		return fmt.Errorf("query otp: %w", err)
	}
	if attempts >= maxOTPAttempts || time.Now().After(expiresAt) {
		_, _ = r.pool.Exec(ctx, `DELETE FROM email_otps WHERE email = $1`, email)
		return ErrOTPExpired
	}
	if hashOTP(code) != hash {
		_, _ = r.pool.Exec(ctx, `UPDATE email_otps SET attempts = attempts + 1 WHERE email = $1`, email)
		return ErrOTPInvalid
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM email_otps WHERE email = $1`, email); err != nil {
		return fmt.Errorf("consume otp: %w", err)
	}
	return nil
}

// generateOTPCode produces a 6-digit numeric code. The modulo-1e6 bias from
// 2^32 not dividing evenly is negligible for a rate-limited, attempt-capped,
// short-lived code — not a cryptographic key.
func generateOTPCode() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint32(buf[:]) % 1000000
	return fmt.Sprintf("%06d", n), nil
}

func hashOTP(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

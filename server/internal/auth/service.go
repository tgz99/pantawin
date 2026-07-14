package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrSignupNotAllowed rejects account creation for emails outside the
// configured allowlist (M6 — team monitors are visible to every account, so
// account creation must be gated once a team uses them).
var ErrSignupNotAllowed = errors.New("signup is not allowed for this email")

// ErrEmailNotVerified blocks login until the OTP step completes (M6.2).
// Google-created accounts are verified at creation, so this only ever
// applies to the email/password path.
var ErrEmailNotVerified = errors.New("email not verified")

type Tokens struct {
	AccessToken  string
	RefreshToken string
}

type Service struct {
	repo            *Repository
	issuer          *TokenIssuer
	refreshStore    *RefreshStore
	refreshTokenTTL time.Duration
	googleVerify    GoogleVerifier // nil = Google sign-in dormant
	signupAllowlist []string
	// allowlistStore consults the DB-backed team invite list (M6.1). When
	// wired, signup is closed-by-default: only env entries or invited emails
	// may create accounts.
	allowlistStore func(ctx context.Context, email string) (bool, error)
	// otpMailer delivers the verification code (M6.2). nil = email/password
	// registration can't complete — an operator misconfiguration, not a
	// normal dormant-feature state, since email/password signup has no
	// fallback path.
	otpMailer func(ctx context.Context, to, code string) error
}

// WithOTPMailer wires verification-code delivery for email/password signup.
func (s *Service) WithOTPMailer(mailer func(ctx context.Context, to, code string) error) *Service {
	s.otpMailer = mailer
	return s
}

// WithSignupAllowlist restricts NEW account creation (register + Google
// find-or-create) to the given entries: exact emails ("a@b.com") or whole
// domains ("@b.com"). Existing accounts always keep working.
func (s *Service) WithSignupAllowlist(entries []string) *Service {
	s.signupAllowlist = entries
	return s
}

// WithSignupAllowlistStore wires the in-app team invite list (M6.1). Once a
// store is set, signup is closed to anyone who is neither in the env
// allowlist nor invited — even when both lists are empty.
func (s *Service) WithSignupAllowlistStore(store func(ctx context.Context, email string) (bool, error)) *Service {
	s.allowlistStore = store
	return s
}

func (s *Service) signupAllowed(ctx context.Context, email string) (bool, error) {
	lower := strings.ToLower(strings.TrimSpace(email))
	for _, entry := range s.signupAllowlist {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, "@") {
			if strings.HasSuffix(lower, entry) {
				return true, nil
			}
		} else if lower == entry {
			return true, nil
		}
	}
	if s.allowlistStore != nil {
		return s.allowlistStore(ctx, lower)
	}
	// No store wired (pre-M6.1 shape): empty env list means open signup.
	return len(s.signupAllowlist) == 0, nil
}

func NewService(repo *Repository, issuer *TokenIssuer, refreshStore *RefreshStore, refreshTTL time.Duration) *Service {
	return &Service{repo: repo, issuer: issuer, refreshStore: refreshStore, refreshTokenTTL: refreshTTL}
}

// Register creates an unverified account and emails it a one-time code; no
// session is issued yet (M6.2 — email/password signup requires VerifyOTP to
// complete, unlike Google sign-in which self-verifies). Registering again
// for an email that's still pending verification just resets the password
// and sends a fresh code, so a lost email or a mistyped password isn't a
// dead end.
func (s *Service) Register(ctx context.Context, email, password string) error {
	allowed, err := s.signupAllowed(ctx, email)
	if err != nil {
		return fmt.Errorf("check signup allowlist: %w", err)
	}
	if !allowed {
		return ErrSignupNotAllowed
	}
	if err := ValidatePasswordPolicy(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	user, err := s.repo.CreateOrReplaceUnverifiedUser(ctx, email, hash)
	if err != nil {
		return err // may be ErrEmailAlreadyRegistered — caller maps to HTTP status
	}
	return s.sendOTP(ctx, user.Email, true) // bypass cooldown: see IssueOTPBypassingCooldown
}

// ResendOTP re-sends a verification code for an account still pending
// verification. Rate-limited per-email (independent of the per-IP auth rate
// limiter) via the repository's resend cooldown.
func (s *Service) ResendOTP(ctx context.Context, email string) error {
	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		return err // ErrUserNotFound if no account exists for this email
	}
	if user.EmailVerified {
		return ErrEmailAlreadyRegistered // nothing pending — already verified
	}
	return s.sendOTP(ctx, user.Email, false) // respects cooldown: this IS the resend button
}

func (s *Service) sendOTP(ctx context.Context, email string, bypassCooldown bool) error {
	if s.otpMailer == nil {
		return fmt.Errorf("otp email delivery is not configured")
	}
	issue := s.repo.IssueOTP
	if bypassCooldown {
		issue = s.repo.IssueOTPBypassingCooldown
	}
	code, err := issue(ctx, email)
	if err != nil {
		return err // may be ErrOTPResendTooSoon
	}
	if err := s.otpMailer(ctx, email, code); err != nil {
		return fmt.Errorf("send otp email: %w", err)
	}
	return nil
}

// VerifyOTP completes email/password registration: a correct, unexpired code
// marks the account verified and issues a session, same as a normal login.
func (s *Service) VerifyOTP(ctx context.Context, email, code string) (Tokens, error) {
	if err := s.repo.VerifyOTP(ctx, email, code); err != nil {
		return Tokens{}, err // ErrOTPInvalid / ErrOTPExpired
	}
	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		return Tokens{}, err
	}
	if err := s.repo.MarkEmailVerified(ctx, email); err != nil {
		return Tokens{}, err
	}
	return s.issueTokens(ctx, user.ID)
}

func (s *Service) Login(ctx context.Context, email, password string) (Tokens, error) {
	user, err := s.repo.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return Tokens{}, ErrInvalidCredentials
		}
		return Tokens{}, err
	}
	if !VerifyPassword(user.PasswordHash, password) {
		return Tokens{}, ErrInvalidCredentials
	}
	if !user.EmailVerified {
		return Tokens{}, ErrEmailNotVerified
	}
	return s.issueTokens(ctx, user.ID)
}

// Refresh validates the JWT signature/expiry AND consumes the stored row —
// refresh tokens are single-use (rotation, spec section 8). Replaying a
// rotated token fails even if its JWT hasn't expired yet.
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	jwtUserID, err := s.issuer.ParseRefreshToken(refreshToken)
	if err != nil {
		return Tokens{}, ErrInvalidCredentials
	}
	storedUserID, err := s.refreshStore.Consume(ctx, refreshToken)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenInvalid) {
			return Tokens{}, ErrInvalidCredentials
		}
		return Tokens{}, err
	}
	if jwtUserID != storedUserID {
		return Tokens{}, ErrInvalidCredentials
	}
	return s.issueTokens(ctx, storedUserID)
}

// ChangePassword verifies the current password, enforces the password
// policy on the new one, then rotates credentials: the hash is replaced,
// ALL existing refresh tokens are revoked (any other device must log in
// again), and a fresh token pair is returned so the calling session
// continues seamlessly.
func (s *Service) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string) (Tokens, error) {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return Tokens{}, err
	}
	if !VerifyPassword(user.PasswordHash, currentPassword) {
		return Tokens{}, ErrInvalidCredentials
	}
	if err := ValidatePasswordPolicy(newPassword); err != nil {
		return Tokens{}, err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return Tokens{}, fmt.Errorf("hash password: %w", err)
	}
	if err := s.repo.UpdatePassword(ctx, userID, hash); err != nil {
		return Tokens{}, err
	}
	if err := s.refreshStore.RevokeAllForUser(ctx, userID); err != nil {
		return Tokens{}, err
	}
	return s.issueTokens(ctx, userID)
}

func (s *Service) issueTokens(ctx context.Context, userID int64) (Tokens, error) {
	access, err := s.issuer.IssueAccessToken(userID)
	if err != nil {
		return Tokens{}, fmt.Errorf("issue access token: %w", err)
	}
	refresh, err := s.issuer.IssueRefreshToken(userID)
	if err != nil {
		return Tokens{}, fmt.Errorf("issue refresh token: %w", err)
	}
	if err := s.refreshStore.Save(ctx, userID, refresh, time.Now().Add(s.refreshTokenTTL)); err != nil {
		return Tokens{}, err
	}
	return Tokens{AccessToken: access, RefreshToken: refresh}, nil
}

// Bootstrap ensures at least one account exists — the operator-provided
// ADMIN_EMAIL/ADMIN_PASSWORD — so the API isn't unusable on a fresh database
// with no registration UI wired up yet at M0.
func Bootstrap(ctx context.Context, repo *Repository, email, password string) error {
	count, err := repo.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}
	if _, err := repo.CreateUser(ctx, email, hash, true); err != nil {
		return fmt.Errorf("create bootstrap admin user: %w", err)
	}
	return nil
}

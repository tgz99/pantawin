package auth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrInvalidCredentials = errors.New("invalid email or password")

type Tokens struct {
	AccessToken  string
	RefreshToken string
}

type Service struct {
	repo            *Repository
	issuer          *TokenIssuer
	refreshStore    *RefreshStore
	refreshTokenTTL time.Duration
}

func NewService(repo *Repository, issuer *TokenIssuer, refreshStore *RefreshStore, refreshTTL time.Duration) *Service {
	return &Service{repo: repo, issuer: issuer, refreshStore: refreshStore, refreshTokenTTL: refreshTTL}
}

func (s *Service) Register(ctx context.Context, email, password string) (Tokens, error) {
	if err := ValidatePasswordPolicy(password); err != nil {
		return Tokens{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return Tokens{}, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.repo.CreateUser(ctx, email, hash)
	if err != nil {
		return Tokens{}, err // may be ErrEmailAlreadyRegistered — caller maps to HTTP status
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
	if _, err := repo.CreateUser(ctx, email, hash); err != nil {
		return fmt.Errorf("create bootstrap admin user: %w", err)
	}
	return nil
}

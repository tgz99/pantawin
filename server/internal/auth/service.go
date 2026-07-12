package auth

import (
	"context"
	"errors"
	"fmt"
)

var ErrInvalidCredentials = errors.New("invalid email or password")

type Tokens struct {
	AccessToken  string
	RefreshToken string
}

type Service struct {
	repo   *Repository
	issuer *TokenIssuer
}

func NewService(repo *Repository, issuer *TokenIssuer) *Service {
	return &Service{repo: repo, issuer: issuer}
}

func (s *Service) Register(ctx context.Context, email, password string) (Tokens, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return Tokens{}, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.repo.CreateUser(ctx, email, hash)
	if err != nil {
		return Tokens{}, err // may be ErrEmailAlreadyRegistered — caller maps to HTTP status
	}
	return s.issueTokens(user.ID)
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
	return s.issueTokens(user.ID)
}

// Refresh is stateless at M0: any refresh token that verifies against the
// signing secret and hasn't expired yields a new access token. Server-side
// revocation/rotation (a refresh_tokens table) lands in M1 alongside full
// monitor auth — see spec section 8 "JWT with rotation".
func (s *Service) Refresh(ctx context.Context, refreshToken string) (Tokens, error) {
	userID, err := s.issuer.ParseRefreshToken(refreshToken)
	if err != nil {
		return Tokens{}, ErrInvalidCredentials
	}
	if _, err := s.repo.GetUserByID(ctx, userID); err != nil {
		return Tokens{}, ErrInvalidCredentials
	}
	return s.issueTokens(userID)
}

func (s *Service) issueTokens(userID int64) (Tokens, error) {
	access, err := s.issuer.IssueAccessToken(userID)
	if err != nil {
		return Tokens{}, fmt.Errorf("issue access token: %w", err)
	}
	refresh, err := s.issuer.IssueRefreshToken(userID)
	if err != nil {
		return Tokens{}, fmt.Errorf("issue refresh token: %w", err)
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

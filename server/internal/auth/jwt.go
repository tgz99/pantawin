package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	tokenTypeAccess  = "access"
	tokenTypeRefresh = "refresh"
)

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrWrongTokenType = errors.New("wrong token type")
)

type TokenIssuer struct {
	secret          []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

func NewTokenIssuer(secret string, accessTTL, refreshTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret), accessTokenTTL: accessTTL, refreshTokenTTL: refreshTTL}
}

type claims struct {
	jwt.RegisteredClaims
	Type string `json:"type"`
}

// IssueAccessToken and IssueRefreshToken both encode the user id as the
// subject; "type" distinguishes them so a refresh token can never be used
// where an access token is expected, and vice versa.
func (t *TokenIssuer) IssueAccessToken(userID int64) (string, error) {
	return t.issue(userID, tokenTypeAccess, t.accessTokenTTL)
}

func (t *TokenIssuer) IssueRefreshToken(userID int64) (string, error) {
	return t.issue(userID, tokenTypeRefresh, t.refreshTokenTTL)
}

func (t *TokenIssuer) issue(userID int64, typ string, ttl time.Duration) (string, error) {
	now := time.Now()
	// A random jti guarantees every token string is unique even when two
	// are minted in the same second with otherwise identical claims — the
	// refresh-token hash is a UNIQUE column, so without this a rapid
	// register->refresh would collide.
	jti, err := randomID()
	if err != nil {
		return "", err
	}
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        jti,
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Type: typ,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, c)
	return token.SignedString(t.secret)
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ParseAccessToken validates signature, expiry, and that the token is
// specifically an access token (not a refresh token being reused).
func (t *TokenIssuer) ParseAccessToken(raw string) (userID int64, err error) {
	return t.parse(raw, tokenTypeAccess)
}

func (t *TokenIssuer) ParseRefreshToken(raw string) (userID int64, err error) {
	return t.parse(raw, tokenTypeRefresh)
}

func (t *TokenIssuer) parse(raw, wantType string) (int64, error) {
	var c claims
	token, err := jwt.ParseWithClaims(raw, &c, func(tok *jwt.Token) (interface{}, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", tok.Header["alg"])
		}
		return t.secret, nil
	})
	if err != nil || !token.Valid {
		return 0, ErrInvalidToken
	}
	if c.Type != wantType {
		return 0, ErrWrongTokenType
	}
	var userID int64
	if _, err := fmt.Sscanf(c.Subject, "%d", &userID); err != nil {
		return 0, ErrInvalidToken
	}
	return userID, nil
}

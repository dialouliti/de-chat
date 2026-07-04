// Package auth handles password hashing and JWT issuance / verification.
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Claims is the JWT payload. It carries the user id, username, and role so
// that middleware can authorize requests without an extra DB lookup.
type Claims struct {
	UserID   uuid.UUID `json:"uid"`
	Username string    `json:"usr"`
	Role     string    `json:"rol"`
	jwt.RegisteredClaims
}

// Service issues and verifies JWTs and hashes passwords.
type Service struct {
	secret []byte
	ttl    time.Duration

	// checker reports whether a user is disabled, so blocked users are rejected
	// on every request. Defaults to "nobody disabled" until SetUserChecker.
	checker UserChecker
}

// New creates an auth Service. The secret must be non-empty.
func New(secret string, ttl time.Duration) *Service {
	return &Service{secret: []byte(secret), ttl: ttl, checker: noOpChecker}
}

// SetUserChecker injects the store-backed disabled check. Must be called once
// during startup, before serving requests, for blocking to take effect.
func (s *Service) SetUserChecker(c UserChecker) {
	if c == nil {
		s.checker = noOpChecker
		return
	}
	s.checker = c
}

// Authenticate validates a token and ensures the user is not blocked. It is
// the shared entry point for "is this request allowed": the REST middleware
// and the WebSocket handshake both go through it, so blocking cannot be
// bypassed by switching transports.
func (s *Service) Authenticate(ctx context.Context, tokenStr string) (*Claims, error) {
	claims, err := s.ParseToken(tokenStr)
	if err != nil {
		return nil, err
	}
	disabled, err := s.checker(ctx, claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("check user: %w", err)
	}
	if disabled {
		return nil, ErrBlocked
	}
	return claims, nil
}

// ErrBlocked is returned by Authenticate when the user has been disabled.
var ErrBlocked = errors.New("account is blocked")

// HashPassword returns a bcrypt hash of the password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

// ComparePassword reports whether the plaintext matches the stored hash.
func ComparePassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// IssueToken creates a signed JWT for the given user that expires after ttl.
func (s *Service) IssueToken(userID uuid.UUID, username, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
			Subject:   userID.String(),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

// ParseToken verifies the signature and returns the claims. It returns an
// error for malformed, tampered, or expired tokens.
func (s *Service) ParseToken(tokenStr string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}

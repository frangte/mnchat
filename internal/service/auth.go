// Package service holds business rules and authorization; it depends on stores
// (repos) through small interfaces.
package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/frangte/mnchat/internal/domain"
)

type UserStore interface {
	List(ctx context.Context) ([]domain.User, error)
	Get(ctx context.Context, id int64) (domain.User, error)
}

type AuthService struct {
	users    UserStore
	secret   []byte
	tokenTTL time.Duration
}

func NewAuthService(users UserStore, secret string) *AuthService {
	return &AuthService{users: users, secret: []byte(secret), tokenTTL: 24 * time.Hour}
}

// DemoUsers lists every seeded user for the passwordless demo login screen.
func (s *AuthService) DemoUsers(ctx context.Context) ([]domain.User, error) {
	return s.users.List(ctx)
}

// DemoLogin issues a token for any existing user without a password (demo only).
func (s *AuthService) DemoLogin(ctx context.Context, userID int64) (string, domain.User, error) {
	u, err := s.users.Get(ctx, userID)
	if err != nil {
		return "", u, err
	}
	now := time.Now()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(u.ID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.tokenTTL)),
	}).SignedString(s.secret)
	if err != nil {
		return "", u, fmt.Errorf("sign token: %w", err)
	}
	return token, u, nil
}

// Authenticate validates a bearer token and returns the user id it was issued to.
func (s *AuthService) Authenticate(token string) (int64, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) {
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return 0, domain.ErrUnauthorized
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return 0, domain.ErrUnauthorized
	}
	return id, nil
}

func (s *AuthService) Me(ctx context.Context, userID int64) (domain.User, error) {
	return s.users.Get(ctx, userID)
}

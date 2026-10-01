package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestNewServiceRejectsInvalidRefreshTTL(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Second} {
		if _, err := NewService(nil, nil, nil, ttl); err == nil {
			t.Fatalf("NewService() with TTL %v returned nil error", ttl)
		}
	}
}

func TestIssueTokenPairValidation(t *testing.T) {
	tokens := newSessionTestTokenManager(t)
	service, err := NewService(nil, sqlc.New(nil), tokens, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	for _, userID := range []int64{0, -1} {
		pair, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
		if !errors.Is(err, ErrInvalidUserID) {
			t.Fatalf("IssueTokenPair(%d) error = %v, want ErrInvalidUserID", userID, err)
		}
		if pair != (TokenPair{}) {
			t.Errorf("IssueTokenPair(%d) pair = %+v, want empty", userID, pair)
		}
	}

	pair, err := service.IssueTokenPair(context.Background(), 1, "owner")
	if err == nil {
		t.Fatal("IssueTokenPair() with invalid role returned nil error")
	}
	if pair != (TokenPair{}) {
		t.Errorf("IssueTokenPair() pair = %+v, want empty", pair)
	}
}

func TestRotateTokenPairRejectsBlankToken(t *testing.T) {
	service, err := NewService(nil, nil, nil, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	for _, rawToken := range []string{"", "   "} {
		pair, err := service.RotateTokenPair(context.Background(), rawToken)
		if !errors.Is(err, ErrInvalidRefreshToken) {
			t.Fatalf("RotateTokenPair(%q) error = %v, want ErrInvalidRefreshToken", rawToken, err)
		}
		if pair != (TokenPair{}) {
			t.Errorf("RotateTokenPair(%q) pair = %+v, want empty", rawToken, pair)
		}
	}
}

func TestRevokeRefreshTokenRejectsBlankToken(t *testing.T) {
	service, err := NewService(nil, nil, nil, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	for _, rawToken := range []string{"", "   "} {
		if err := service.RevokeRefreshToken(context.Background(), rawToken); !errors.Is(err, ErrInvalidRefreshToken) {
			t.Fatalf("RevokeRefreshToken(%q) error = %v, want ErrInvalidRefreshToken", rawToken, err)
		}
	}
}

func newSessionTestTokenManager(t *testing.T) *auth.TokenManager {
	t.Helper()
	tokens, err := auth.NewTokenManager(
		"test-secret-that-is-at-least-32-bytes-long",
		"ryoko-test",
		"ryoko-test-api",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	return tokens
}

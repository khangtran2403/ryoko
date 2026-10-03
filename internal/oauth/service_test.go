package oauth

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/session"
)

type stubTokenIssuer struct {
	pair   session.TokenPair
	err    error
	calls  int
	userID int64
	role   string
}

func (s *stubTokenIssuer) IssueTokenPair(_ context.Context, userID int64, role string) (session.TokenPair, error) {
	s.calls++
	s.userID = userID
	s.role = role
	return s.pair, s.err
}

func TestNewServiceRejectsMissingDependencies(t *testing.T) {
	pool := &pgxpool.Pool{}
	queries := sqlc.New(pool)
	issuer := &stubTokenIssuer{}

	if _, err := NewService(nil, queries, issuer); err == nil {
		t.Fatal("NewService() with nil pool returned nil error")
	}
	if _, err := NewService(pool, nil, issuer); err == nil {
		t.Fatal("NewService() with nil queries returned nil error")
	}
	if _, err := NewService(pool, queries, nil); err == nil {
		t.Fatal("NewService() with nil session service returned nil error")
	}
}

func TestAuthenticateGoogleRejectsInvalidIdentityBeforeDatabaseAccess(t *testing.T) {
	service := &Service{sessions: &stubTokenIssuer{}}
	tests := []GoogleIdentity{
		{Email: "user@example.com", Name: "User"},
		{Subject: "subject", Name: "User"},
		{Subject: "subject", Email: "not-an-email", Name: "User"},
		{Subject: "subject", Email: "user@example.com"},
	}
	for _, identity := range tests {
		if _, err := service.AuthenticateGoogle(context.Background(), identity); !errors.Is(err, ErrInvalidGoogleIdentity) {
			t.Errorf("AuthenticateGoogle(%+v) error = %v, want ErrInvalidGoogleIdentity", identity, err)
		}
	}
}

func TestNormalizeGoogleIdentity(t *testing.T) {
	got, err := normalizeGoogleIdentity(GoogleIdentity{
		Subject: "  google-subject  ",
		Email:   "  USER@Example.COM ",
		Name:    "  Example User  ",
	})
	if err != nil {
		t.Fatalf("normalizeGoogleIdentity() error = %v", err)
	}
	want := GoogleIdentity{Subject: "google-subject", Email: "user@example.com", Name: "Example User"}
	if got != want {
		t.Errorf("normalizeGoogleIdentity() = %+v, want %+v", got, want)
	}
}

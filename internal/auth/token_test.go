package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef"

func TestNewTokenManager(t *testing.T) {
	tests := []struct {
		name     string
		secret   string
		issuer   string
		audience string
		ttl      time.Duration
		wantErr  bool
	}{
		{
			name:     "valid configuration",
			secret:   testJWTSecret,
			issuer:   "ryoko",
			audience: "ryoko-api",
			ttl:      15 * time.Minute,
		},
		{
			name:     "short secret",
			secret:   "too-short",
			issuer:   "ryoko",
			audience: "ryoko-api",
			ttl:      15 * time.Minute,
			wantErr:  true,
		},
		{
			name:     "whitespace secret",
			secret:   strings.Repeat(" ", 32),
			issuer:   "ryoko",
			audience: "ryoko-api",
			ttl:      15 * time.Minute,
			wantErr:  true,
		},
		{
			name:     "empty issuer",
			secret:   testJWTSecret,
			issuer:   " ",
			audience: "ryoko-api",
			ttl:      15 * time.Minute,
			wantErr:  true,
		},
		{
			name:     "empty audience",
			secret:   testJWTSecret,
			issuer:   "ryoko",
			audience: " ",
			ttl:      15 * time.Minute,
			wantErr:  true,
		},
		{
			name:     "nonpositive TTL",
			secret:   testJWTSecret,
			issuer:   "ryoko",
			audience: "ryoko-api",
			ttl:      0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewTokenManager(tt.secret, tt.issuer, tt.audience, tt.ttl)
			if tt.wantErr {
				if err == nil {
					t.Fatal("NewTokenManager() error = nil, want an error")
				}
				if manager != nil {
					t.Fatal("NewTokenManager() returned a manager with invalid configuration")
				}
				return
			}

			if err != nil {
				t.Fatalf("NewTokenManager() error = %v, want nil", err)
			}
			if manager == nil {
				t.Fatal("NewTokenManager() returned nil manager")
			}
		})
	}
}

func TestTokenRoundTrip(t *testing.T) {
	manager := newTestTokenManager(t)

	tokenString, err := manager.GenerateToken(42, RoleCustomer)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	principal, err := manager.ParseToken(tokenString)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if principal.UserID != 42 {
		t.Errorf("ParseToken() UserID = %d, want 42", principal.UserID)
	}
	if principal.Role != RoleCustomer {
		t.Errorf("ParseToken() Role = %q, want %q", principal.Role, RoleCustomer)
	}
}

func TestGenerateTokenRejectsInvalidPrincipal(t *testing.T) {
	manager := newTestTokenManager(t)

	tests := []struct {
		name   string
		userID int64
		role   string
	}{
		{name: "zero user ID", userID: 0, role: RoleCustomer},
		{name: "negative user ID", userID: -1, role: RoleCustomer},
		{name: "unknown role", userID: 1, role: "staff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := manager.GenerateToken(tt.userID, tt.role); err == nil {
				t.Fatal("GenerateToken() error = nil, want an error")
			}
		})
	}
}

func TestParseTokenRejectsExpiredToken(t *testing.T) {
	manager := newTestTokenManager(t)
	tokenString, err := manager.GenerateToken(42, RoleCustomer)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	manager.now = func() time.Time {
		return time.Date(2030, time.January, 1, 0, 16, 0, 0, time.UTC)
	}

	if _, err := manager.ParseToken(tokenString); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
	}
}

func TestParseTokenRejectsWrongSecret(t *testing.T) {
	issuer := newTestTokenManager(t)
	tokenString, err := issuer.GenerateToken(42, RoleAdmin)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	parser, err := NewTokenManager(
		"abcdef0123456789abcdef0123456789",
		"ryoko",
		"ryoko-api",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	parser.now = issuer.now

	if _, err := parser.ParseToken(tokenString); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
	}
}

func TestParseTokenRejectsInvalidClaims(t *testing.T) {
	manager := newTestTokenManager(t)
	now := manager.now().UTC()

	tests := []struct {
		name   string
		claims Claims
	}{
		{
			name: "nonnumeric subject",
			claims: validTestClaims(now, func(claims *Claims) {
				claims.Subject = "not-a-number"
			}),
		},
		{
			name: "nonpositive subject",
			claims: validTestClaims(now, func(claims *Claims) {
				claims.Subject = "0"
			}),
		},
		{
			name: "unknown role",
			claims: validTestClaims(now, func(claims *Claims) {
				claims.Role = "staff"
			}),
		},
		{
			name: "missing expiration",
			claims: validTestClaims(now, func(claims *Claims) {
				claims.ExpiresAt = nil
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokenString := signTestToken(t, manager, jwt.SigningMethodHS256, tt.claims)
			if _, err := manager.ParseToken(tokenString); !errors.Is(err, ErrInvalidToken) {
				t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestParseTokenRejectsWrongSigningMethod(t *testing.T) {
	manager := newTestTokenManager(t)
	claims := validTestClaims(manager.now().UTC(), nil)
	tokenString := signTestToken(t, manager, jwt.SigningMethodHS384, claims)

	if _, err := manager.ParseToken(tokenString); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("ParseToken() error = %v, want ErrInvalidToken", err)
	}
}

func newTestTokenManager(t *testing.T) *TokenManager {
	t.Helper()

	manager, err := NewTokenManager(testJWTSecret, "ryoko", "ryoko-api", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	manager.now = func() time.Time {
		return time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)
	}
	return manager
}

func validTestClaims(now time.Time, mutate func(*Claims)) Claims {
	claims := Claims{
		Role: RoleCustomer,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "ryoko",
			Audience:  jwt.ClaimStrings{"ryoko-api"},
			Subject:   "42",
			ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	if mutate != nil {
		mutate(&claims)
	}
	return claims
}

func signTestToken(
	t *testing.T,
	manager *TokenManager,
	method jwt.SigningMethod,
	claims Claims,
) string {
	t.Helper()

	tokenString, err := jwt.NewWithClaims(method, claims).SignedString(manager.secret)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return tokenString
}

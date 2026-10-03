package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestGoogleIDTokenValidatorValidatesClaimsAndCachesKeys(t *testing.T) {
	privateKey, keyID, server, requests := newGoogleJWKSTestServer(t)
	fixedNow := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	validator := NewGoogleIDTokenValidator(server.Client())
	validator.jwksURL = server.URL
	validator.now = func() time.Time { return fixedNow }

	rawToken := signGoogleIDToken(t, privateKey, keyID, googleIDTokenClaims{
		Email:         " USER@Example.com ",
		EmailVerified: true,
		Name:          "  Example User  ",
		Nonce:         "expected-nonce",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    googleModernIssuer,
			Subject:   "google-subject",
			Audience:  jwt.ClaimStrings{"client-id"},
			ExpiresAt: jwt.NewNumericDate(fixedNow.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(fixedNow.Add(-time.Minute)),
		},
	})

	want := GoogleIdentity{Subject: "google-subject", Email: "user@example.com", Name: "Example User"}
	for attempt := 1; attempt <= 2; attempt++ {
		got, err := validator.Validate(context.Background(), rawToken, "client-id", "expected-nonce")
		if err != nil {
			t.Fatalf("Validate() attempt %d error = %v", attempt, err)
		}
		if got != want {
			t.Errorf("Validate() = %+v, want %+v", got, want)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("JWKS requests = %d, want 1", got)
	}
}

func TestGoogleIDTokenValidatorRejectsInvalidIdentityClaims(t *testing.T) {
	privateKey, keyID, server, _ := newGoogleJWKSTestServer(t)
	fixedNow := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	validator := NewGoogleIDTokenValidator(server.Client())
	validator.jwksURL = server.URL
	validator.now = func() time.Time { return fixedNow }

	validClaims := googleIDTokenClaims{
		Email:         "user@example.com",
		EmailVerified: true,
		Name:          "User",
		Nonce:         "expected-nonce",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    googleModernIssuer,
			Subject:   "google-subject",
			Audience:  jwt.ClaimStrings{"client-id"},
			ExpiresAt: jwt.NewNumericDate(fixedNow.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(fixedNow.Add(-time.Minute)),
		},
	}

	tests := []struct {
		name   string
		mutate func(*googleIDTokenClaims)
		nonce  string
	}{
		{name: "wrong nonce", nonce: "wrong-nonce"},
		{name: "unverified email", nonce: "expected-nonce", mutate: func(claims *googleIDTokenClaims) { claims.EmailVerified = false }},
		{name: "invalid email", nonce: "expected-nonce", mutate: func(claims *googleIDTokenClaims) { claims.Email = "invalid" }},
		{name: "missing subject", nonce: "expected-nonce", mutate: func(claims *googleIDTokenClaims) { claims.Subject = "" }},
		{name: "invalid issuer", nonce: "expected-nonce", mutate: func(claims *googleIDTokenClaims) { claims.Issuer = "https://issuer.example.com" }},
		{name: "expired", nonce: "expected-nonce", mutate: func(claims *googleIDTokenClaims) { claims.ExpiresAt = jwt.NewNumericDate(fixedNow.Add(-time.Minute)) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := validClaims
			if tt.mutate != nil {
				tt.mutate(&claims)
			}
			rawToken := signGoogleIDToken(t, privateKey, keyID, claims)
			if _, err := validator.Validate(context.Background(), rawToken, "client-id", tt.nonce); err == nil {
				t.Fatal("Validate() returned nil error")
			}
		})
	}
}

func TestGoogleIDTokenValidatorUsesEmailLocalPartWhenNameMissing(t *testing.T) {
	privateKey, keyID, server, _ := newGoogleJWKSTestServer(t)
	fixedNow := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	validator := NewGoogleIDTokenValidator(server.Client())
	validator.jwksURL = server.URL
	validator.now = func() time.Time { return fixedNow }
	rawToken := signGoogleIDToken(t, privateKey, keyID, googleIDTokenClaims{
		Email:         "user@example.com",
		EmailVerified: true,
		Nonce:         "nonce",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: googleLegacyIssuer, Subject: "subject",
			Audience:  jwt.ClaimStrings{"client-id"},
			ExpiresAt: jwt.NewNumericDate(fixedNow.Add(time.Hour)),
		},
	})

	identity, err := validator.Validate(context.Background(), rawToken, "client-id", "nonce")
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if identity.Name != "user" {
		t.Errorf("identity name = %q, want %q", identity.Name, "user")
	}
}

func TestCacheMaxAge(t *testing.T) {
	tests := []struct {
		header string
		want   time.Duration
	}{
		{header: "public, max-age=3600", want: time.Hour},
		{header: `max-age="120"`, want: 2 * time.Minute},
		{header: "public", want: time.Hour},
		{header: "max-age=invalid", want: time.Hour},
	}
	for _, tt := range tests {
		if got := cacheMaxAge(tt.header); got != tt.want {
			t.Errorf("cacheMaxAge(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func newGoogleJWKSTestServer(
	t *testing.T,
) (*rsa.PrivateKey, string, *httptest.Server, *atomic.Int32) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	const keyID = "test-key"
	exponent := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
	document := googleJWKS{Keys: []googleJWK{{
		KeyType: "RSA", KeyID: keyID, Use: "sig", Algorithm: "RS256",
		Modulus:  base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
		Exponent: base64.RawURLEncoding.EncodeToString(exponent),
	}}}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(document)
	}))
	t.Cleanup(server.Close)
	return privateKey, keyID, server, &requests
}

func signGoogleIDToken(
	t *testing.T,
	privateKey *rsa.PrivateKey,
	keyID string,
	claims googleIDTokenClaims,
) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = keyID
	rawToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign ID token: %v", err)
	}
	return rawToken
}

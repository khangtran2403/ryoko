package oauth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestGoogleProviderAuthorizationURL(t *testing.T) {
	provider := newTestGoogleProvider(t)
	authorizationURL, err := provider.AuthorizationURL("state-value", "verifier-value", "nonce-value")
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if parsed.Scheme+"://"+parsed.Host+parsed.Path != googleAuthorizationURL {
		t.Errorf("authorization endpoint = %q, want %q", parsed.Scheme+"://"+parsed.Host+parsed.Path, googleAuthorizationURL)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-id" ||
		query.Get("redirect_uri") != "http://localhost:8080/auth/google/callback" ||
		query.Get("response_type") != "code" ||
		query.Get("state") != "state-value" ||
		query.Get("nonce") != "nonce-value" ||
		query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") != oauth2.S256ChallengeFromVerifier("verifier-value") {
		t.Errorf("authorization query = %v", query)
	}
	for _, scope := range []string{googleOpenIDScope, googleEmailScope, googleProfileScope} {
		if !strings.Contains(" "+query.Get("scope")+" ", " "+scope+" ") {
			t.Errorf("scope %q missing from %q", scope, query.Get("scope"))
		}
	}
}

func TestGoogleProviderAuthorizationURLRejectsMissingSecurityValues(t *testing.T) {
	provider := newTestGoogleProvider(t)
	for _, values := range [][3]string{
		{"", "verifier", "nonce"},
		{"state", "", "nonce"},
		{"state", "verifier", ""},
	} {
		if _, err := provider.AuthorizationURL(values[0], values[1], values[2]); err == nil {
			t.Errorf("AuthorizationURL(%q, %q, %q) returned nil error", values[0], values[1], values[2])
		}
	}
}

func TestGoogleProviderExchangeIdentity(t *testing.T) {
	provider := newTestGoogleProvider(t)
	var gotCode, gotVerifier, gotRawToken, gotAudience, gotNonce string
	provider.exchangeToken = func(_ context.Context, code, verifier string) (*oauth2.Token, error) {
		gotCode = code
		gotVerifier = verifier
		return (&oauth2.Token{}).WithExtra(map[string]any{"id_token": "raw-id-token"}), nil
	}
	want := GoogleIdentity{Subject: "google-subject", Email: "user@example.com", Name: "User"}
	provider.validateToken = func(_ context.Context, rawToken, audience, nonce string) (GoogleIdentity, error) {
		gotRawToken = rawToken
		gotAudience = audience
		gotNonce = nonce
		return want, nil
	}

	got, err := provider.ExchangeIdentity(context.Background(), " auth-code ", "verifier", "nonce")
	if err != nil {
		t.Fatalf("ExchangeIdentity() error = %v", err)
	}
	if got != want {
		t.Errorf("identity = %+v, want %+v", got, want)
	}
	if gotCode != "auth-code" || gotVerifier != "verifier" || gotRawToken != "raw-id-token" || gotAudience != "client-id" || gotNonce != "nonce" {
		t.Errorf("exchange inputs = {code:%q verifier:%q token:%q audience:%q nonce:%q}", gotCode, gotVerifier, gotRawToken, gotAudience, gotNonce)
	}
}

func TestGoogleProviderExchangeIdentityRejectsFailures(t *testing.T) {
	tests := []struct {
		name     string
		exchange func(context.Context, string, string) (*oauth2.Token, error)
		validate func(context.Context, string, string, string) (GoogleIdentity, error)
	}{
		{
			name: "exchange failure",
			exchange: func(context.Context, string, string) (*oauth2.Token, error) {
				return nil, errors.New("token endpoint unavailable")
			},
		},
		{
			name: "missing ID token",
			exchange: func(context.Context, string, string) (*oauth2.Token, error) {
				return &oauth2.Token{}, nil
			},
		},
		{
			name: "validation failure",
			exchange: func(context.Context, string, string) (*oauth2.Token, error) {
				return (&oauth2.Token{}).WithExtra(map[string]any{"id_token": "raw-id-token"}), nil
			},
			validate: func(context.Context, string, string, string) (GoogleIdentity, error) {
				return GoogleIdentity{}, errors.New("invalid signature")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := newTestGoogleProvider(t)
			provider.exchangeToken = tt.exchange
			if tt.validate != nil {
				provider.validateToken = tt.validate
			}
			identity, err := provider.ExchangeIdentity(context.Background(), "code", "verifier", "nonce")
			if err == nil {
				t.Fatal("ExchangeIdentity() returned nil error")
			}
			if identity != (GoogleIdentity{}) {
				t.Errorf("identity = %+v, want empty", identity)
			}
		})
	}
}

func TestGoogleProviderExchangeIdentityRejectsMissingInput(t *testing.T) {
	provider := newTestGoogleProvider(t)
	for _, values := range [][3]string{
		{"", "verifier", "nonce"},
		{"code", "", "nonce"},
		{"code", "verifier", ""},
	} {
		if _, err := provider.ExchangeIdentity(context.Background(), values[0], values[1], values[2]); !errors.Is(err, ErrInvalidGoogleIdentity) {
			t.Errorf("ExchangeIdentity(%q, %q, %q) error = %v", values[0], values[1], values[2], err)
		}
	}
}

func newTestGoogleProvider(t *testing.T) *GoogleProvider {
	t.Helper()
	provider, err := NewGoogleProvider(
		"client-id",
		"client-secret",
		"http://localhost:8080/auth/google/callback",
		NewGoogleIDTokenValidator(nil),
	)
	if err != nil {
		t.Fatalf("NewGoogleProvider() error = %v", err)
	}
	return provider
}

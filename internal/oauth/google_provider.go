package oauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"
)

const (
	ProviderGoogle         = "google"
	googleAuthorizationURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL         = "https://oauth2.googleapis.com/token"
	googleOpenIDScope      = "openid"
	googleEmailScope       = "email"
	googleProfileScope     = "profile"
)

var ErrInvalidGoogleIdentity = errors.New("invalid Google identity")

type GoogleIdentity struct {
	Subject string
	Email   string
	Name    string
}

type GoogleProvider struct {
	clientID      string
	config        oauth2.Config
	exchangeToken func(ctx context.Context, code, verifier string) (*oauth2.Token, error)
	validateToken func(ctx context.Context, rawToken, audience, nonce string) (GoogleIdentity, error)
}

func NewGoogleProvider(
	clientID string,
	clientSecret string,
	redirectURL string,
	validator *GoogleIDTokenValidator,
) (*GoogleProvider, error) {
	clientID = strings.TrimSpace(clientID)
	redirectURL = strings.TrimSpace(redirectURL)
	if clientID == "" {
		return nil, errors.New("Google OAuth client ID is required")
	}
	if clientSecret == "" {
		return nil, errors.New("Google OAuth client secret is required")
	}
	if redirectURL == "" {
		return nil, errors.New("Google OAuth redirect URL is required")
	}
	if validator == nil {
		return nil, errors.New("Google ID-token validator is required")
	}

	provider := &GoogleProvider{
		clientID: clientID,
		config: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes: []string{
				googleOpenIDScope,
				googleEmailScope,
				googleProfileScope,
			},
			Endpoint: oauth2.Endpoint{
				AuthURL:   googleAuthorizationURL,
				TokenURL:  googleTokenURL,
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
		validateToken: validator.Validate,
	}
	provider.exchangeToken = func(ctx context.Context, code, verifier string) (*oauth2.Token, error) {
		return provider.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	}
	return provider, nil
}

func (p *GoogleProvider) AuthorizationURL(state, verifier, nonce string) (string, error) {
	if state == "" || verifier == "" || nonce == "" {
		return "", errors.New("OAuth state, PKCE verifier, and nonce are required")
	}
	return p.config.AuthCodeURL(
		state,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	), nil
}

func (p *GoogleProvider) ExchangeIdentity(
	ctx context.Context,
	code string,
	verifier string,
	nonce string,
) (GoogleIdentity, error) {
	code = strings.TrimSpace(code)
	if code == "" || verifier == "" || nonce == "" {
		return GoogleIdentity{}, ErrInvalidGoogleIdentity
	}

	token, err := p.exchangeToken(ctx, code, verifier)
	if err != nil {
		return GoogleIdentity{}, fmt.Errorf("exchange Google authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return GoogleIdentity{}, fmt.Errorf("%w: token response did not contain an ID token", ErrInvalidGoogleIdentity)
	}

	identity, err := p.validateToken(ctx, rawIDToken, p.clientID, nonce)
	if err != nil {
		return GoogleIdentity{}, fmt.Errorf("%w: %v", ErrInvalidGoogleIdentity, err)
	}
	return identity, nil
}

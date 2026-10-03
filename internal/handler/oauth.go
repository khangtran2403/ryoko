package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	appoauth "github.com/khangtran2403/ryoko/internal/oauth"
	"github.com/khangtran2403/ryoko/internal/session"
)

type googleOAuthProvider interface {
	AuthorizationURL(state, verifier, nonce string) (string, error)
	ExchangeIdentity(ctx context.Context, code, verifier, nonce string) (appoauth.GoogleIdentity, error)
}

type oauthFlowCookieManager interface {
	Set(w http.ResponseWriter, values appoauth.FlowValues) error
	ReadAndClear(w http.ResponseWriter, r *http.Request) (appoauth.FlowValues, error)
}

type oauthAccountService interface {
	AuthenticateGoogle(ctx context.Context, identity appoauth.GoogleIdentity) (session.TokenPair, error)
}

type OAuthHandler struct {
	provider googleOAuthProvider
	cookies  oauthFlowCookieManager
	service  oauthAccountService
}

func NewOAuthHandler(
	provider googleOAuthProvider,
	cookies oauthFlowCookieManager,
	service oauthAccountService,
) *OAuthHandler {
	return &OAuthHandler{
		provider: provider,
		cookies:  cookies,
		service:  service,
	}
}

// StartGoogle creates a short-lived browser flow protected by state, PKCE,
// and an OpenID nonce, then redirects the browser to Google.
func (h *OAuthHandler) StartGoogle(w http.ResponseWriter, r *http.Request) {
	state, err := appoauth.GenerateState()
	if err != nil {
		http.Error(w, "Failed to start Google authentication", http.StatusInternalServerError)
		return
	}
	verifier, err := appoauth.GeneratePKCEVerifier()
	if err != nil {
		http.Error(w, "Failed to start Google authentication", http.StatusInternalServerError)
		return
	}
	nonce, err := appoauth.GenerateNonce()
	if err != nil {
		http.Error(w, "Failed to start Google authentication", http.StatusInternalServerError)
		return
	}

	flow := appoauth.FlowValues{
		State:        state,
		PKCEVerifier: verifier,
		Nonce:        nonce,
	}
	authorizationURL, err := h.provider.AuthorizationURL(state, verifier, nonce)
	if err != nil {
		http.Error(w, "Failed to start Google authentication", http.StatusInternalServerError)
		return
	}
	if err := h.cookies.Set(w, flow); err != nil {
		http.Error(w, "Failed to start Google authentication", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

// GoogleCallback consumes the one-time browser flow, exchanges Google's code,
// links the verified identity, and returns Ryoko's normal token response.
func (h *OAuthHandler) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	flow, err := h.cookies.ReadAndClear(w, r)
	if err != nil {
		http.Error(w, "Invalid or expired Google authentication flow", http.StatusBadRequest)
		return
	}
	if !appoauth.SecureEqual(r.URL.Query().Get("state"), flow.State) {
		http.Error(w, "Invalid or expired Google authentication flow", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Error(w, "Google authentication was not completed", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Google authorization code is required", http.StatusBadRequest)
		return
	}
	identity, err := h.provider.ExchangeIdentity(r.Context(), code, flow.PKCEVerifier, flow.Nonce)
	if err != nil {
		http.Error(w, "Google authentication failed", http.StatusUnauthorized)
		return
	}

	pair, err := h.service.AuthenticateGoogle(r.Context(), identity)
	if errors.Is(err, appoauth.ErrOAuthAccountConflict) {
		http.Error(w, "This account is already linked to a different Google identity", http.StatusConflict)
		return
	}
	if errors.Is(err, appoauth.ErrInvalidGoogleIdentity) {
		http.Error(w, "Google authentication failed", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "Failed to complete Google authentication", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if err := json.NewEncoder(w).Encode(LoginResponse{
		AccessToken:      pair.AccessToken,
		RefreshToken:     pair.RefreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        pair.AccessExpiresIn,
		RefreshExpiresAt: pair.RefreshExpiresAt,
	}); err != nil {
		return
	}
}

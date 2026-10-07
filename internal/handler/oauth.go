package handler

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

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
	CreateGoogleLoginCode(ctx context.Context, identity appoauth.GoogleIdentity) (appoauth.LoginCode, error)
	ExchangeLoginCode(ctx context.Context, rawCode string) (session.TokenPair, error)
}

type OAuthHandler struct {
	provider           googleOAuthProvider
	cookies            oauthFlowCookieManager
	service            oauthAccountService
	refreshCookies     refreshCookieManager
	successRedirectURL *url.URL
}

type ExchangeOAuthCodeRequest struct {
	Code string `json:"code"`
}

func NewOAuthHandler(
	provider googleOAuthProvider,
	cookies oauthFlowCookieManager,
	service oauthAccountService,
	refreshCookies refreshCookieManager,
	successRedirectURL string,
) (*OAuthHandler, error) {
	redirectURL, err := url.Parse(strings.TrimSpace(successRedirectURL))
	if err != nil || redirectURL.Scheme == "" || redirectURL.Host == "" {
		return nil, errors.New("OAuth success redirect URL must be absolute")
	}
	if redirectURL.Scheme != "http" && redirectURL.Scheme != "https" {
		return nil, errors.New("OAuth success redirect URL must use http or https")
	}
	return &OAuthHandler{
		provider:           provider,
		cookies:            cookies,
		service:            service,
		refreshCookies:     refreshCookies,
		successRedirectURL: redirectURL,
	}, nil
}

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

	authorizationURL, err := h.provider.AuthorizationURL(state, verifier, nonce)
	if err != nil {
		http.Error(w, "Failed to start Google authentication", http.StatusInternalServerError)
		return
	}
	if err := h.cookies.Set(w, appoauth.FlowValues{
		State: state, PKCEVerifier: verifier, Nonce: nonce,
	}); err != nil {
		http.Error(w, "Failed to start Google authentication", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

// GoogleCallback consumes Google's response and redirects the browser with a
// short-lived one-time Ryoko code. Access and refresh tokens never enter the URL.
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

	loginCode, err := h.service.CreateGoogleLoginCode(r.Context(), identity)
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

	redirectURL := *h.successRedirectURL
	query := redirectURL.Query()
	query.Set("code", loginCode.Code)
	redirectURL.RawQuery = query.Encode()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

// ExchangeCode consumes the one-time code and returns the standard Ryoko token
// pair. The service makes code consumption and refresh-token storage atomic.
func (h *OAuthHandler) ExchangeCode(w http.ResponseWriter, r *http.Request) {
	var req ExchangeOAuthCodeRequest
	if !decodeJSONRequest(w, r, &req, false) {
		return
	}
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		http.Error(w, "OAuth login code is required", http.StatusBadRequest)
		return
	}

	pair, err := h.service.ExchangeLoginCode(r.Context(), req.Code)
	if errors.Is(err, appoauth.ErrInvalidLoginCode) ||
		errors.Is(err, appoauth.ErrExpiredLoginCode) ||
		errors.Is(err, appoauth.ErrConsumedLoginCode) {
		http.Error(w, "Invalid or expired OAuth login code", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "Failed to exchange OAuth login code", http.StatusInternalServerError)
		return
	}

	if err := writeSessionResponse(w, pair, h.refreshCookies); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
	}
}

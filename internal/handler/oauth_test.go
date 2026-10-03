package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	appoauth "github.com/khangtran2403/ryoko/internal/oauth"
	"github.com/khangtran2403/ryoko/internal/session"
)

type fakeGoogleOAuthProvider struct {
	authorizationErr error
	exchangeIdentity appoauth.GoogleIdentity
	exchangeErr      error

	state            string
	startVerifier    string
	startNonce       string
	exchangeCalled   bool
	code             string
	exchangeVerifier string
	exchangeNonce    string
}

func (p *fakeGoogleOAuthProvider) AuthorizationURL(state, verifier, nonce string) (string, error) {
	p.state = state
	p.startVerifier = verifier
	p.startNonce = nonce
	if p.authorizationErr != nil {
		return "", p.authorizationErr
	}
	return "https://accounts.example.test/authorize?state=" + url.QueryEscape(state), nil
}

func (p *fakeGoogleOAuthProvider) ExchangeIdentity(
	_ context.Context,
	code string,
	verifier string,
	nonce string,
) (appoauth.GoogleIdentity, error) {
	p.exchangeCalled = true
	p.code = code
	p.exchangeVerifier = verifier
	p.exchangeNonce = nonce
	return p.exchangeIdentity, p.exchangeErr
}

type fakeOAuthAccountService struct {
	called   bool
	identity appoauth.GoogleIdentity
	pair     session.TokenPair
	err      error
}

func (s *fakeOAuthAccountService) AuthenticateGoogle(
	_ context.Context,
	identity appoauth.GoogleIdentity,
) (session.TokenPair, error) {
	s.called = true
	s.identity = identity
	return s.pair, s.err
}

func TestOAuthHandlerGoogleFlowSuccess(t *testing.T) {
	provider := &fakeGoogleOAuthProvider{
		exchangeIdentity: appoauth.GoogleIdentity{
			Subject: "google-subject",
			Email:   "user@example.com",
			Name:    "Example User",
		},
	}
	expiresAt := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	service := &fakeOAuthAccountService{pair: session.TokenPair{
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		AccessExpiresIn:  900,
		RefreshExpiresAt: expiresAt,
	}}
	handler := newTestOAuthHandler(t, provider, service)

	startRequest := httptest.NewRequest(http.MethodGet, "/auth/google", nil)
	startRecorder := httptest.NewRecorder()
	handler.StartGoogle(startRecorder, startRequest)

	if startRecorder.Code != http.StatusFound {
		t.Fatalf("start status = %d, want %d; body = %s", startRecorder.Code, http.StatusFound, startRecorder.Body.String())
	}
	if provider.state == "" || provider.startVerifier == "" || provider.startNonce == "" {
		t.Fatalf("flow values = {state:%q verifier:%q nonce:%q}", provider.state, provider.startVerifier, provider.startNonce)
	}
	if location := startRecorder.Header().Get("Location"); !strings.Contains(location, url.QueryEscape(provider.state)) {
		t.Errorf("redirect location = %q, want generated state", location)
	}
	flowCookies := startRecorder.Result().Cookies()
	if len(flowCookies) != 3 {
		t.Fatalf("flow cookie count = %d, want 3", len(flowCookies))
	}
	for _, cookie := range flowCookies {
		if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/auth/google" {
			t.Errorf("flow cookie has unsafe attributes: %+v", cookie)
		}
	}

	callbackRequest := httptest.NewRequest(
		http.MethodGet,
		"/auth/google/callback?state="+url.QueryEscape(provider.state)+"&code=authorization-code",
		nil,
	)
	for _, cookie := range flowCookies {
		callbackRequest.AddCookie(cookie)
	}
	callbackRecorder := httptest.NewRecorder()
	handler.GoogleCallback(callbackRecorder, callbackRequest)

	if callbackRecorder.Code != http.StatusOK {
		t.Fatalf("callback status = %d, want %d; body = %s", callbackRecorder.Code, http.StatusOK, callbackRecorder.Body.String())
	}
	if !provider.exchangeCalled || provider.code != "authorization-code" {
		t.Errorf("exchange call = {called:%v code:%q}", provider.exchangeCalled, provider.code)
	}
	if provider.exchangeVerifier != provider.startVerifier || provider.exchangeNonce != provider.startNonce {
		t.Errorf("exchange flow values do not match start values")
	}
	if !service.called || service.identity != provider.exchangeIdentity {
		t.Errorf("service call = {called:%v identity:%+v}", service.called, service.identity)
	}
	if got := callbackRecorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	var response LoginResponse
	if err := json.NewDecoder(callbackRecorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode callback response: %v", err)
	}
	if response.AccessToken != "access-token" || response.RefreshToken != "refresh-token" ||
		response.TokenType != "Bearer" || response.ExpiresIn != 900 ||
		!response.RefreshExpiresAt.Equal(expiresAt) {
		t.Errorf("callback response = %+v", response)
	}

	clearedCookies := callbackRecorder.Result().Cookies()
	if len(clearedCookies) != 3 {
		t.Fatalf("cleared cookie count = %d, want 3", len(clearedCookies))
	}
	for _, cookie := range clearedCookies {
		if cookie.MaxAge != -1 {
			t.Errorf("cleared cookie MaxAge = %d, want -1", cookie.MaxAge)
		}
	}

}

func TestOAuthHandlerGoogleCallbackRejectsInvalidState(t *testing.T) {
	provider := &fakeGoogleOAuthProvider{}
	service := &fakeOAuthAccountService{}
	handler := newTestOAuthHandler(t, provider, service)
	flowCookies := startOAuthFlow(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=tampered&code=code", nil)
	for _, cookie := range flowCookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.GoogleCallback(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if provider.exchangeCalled || service.called {
		t.Error("invalid state reached the provider or account service")
	}
	if len(recorder.Result().Cookies()) != 3 {
		t.Error("invalid callback did not clear all flow cookies")
	}
}

func TestOAuthHandlerGoogleCallbackRejectsMissingCookies(t *testing.T) {
	provider := &fakeGoogleOAuthProvider{}
	service := &fakeOAuthAccountService{}
	handler := newTestOAuthHandler(t, provider, service)
	request := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=state&code=code", nil)
	recorder := httptest.NewRecorder()

	handler.GoogleCallback(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if provider.exchangeCalled || service.called {
		t.Error("missing cookies reached the provider or account service")
	}
}

func TestOAuthHandlerGoogleCallbackHandlesProviderDenial(t *testing.T) {
	provider := &fakeGoogleOAuthProvider{}
	service := &fakeOAuthAccountService{}
	handler := newTestOAuthHandler(t, provider, service)
	flowCookies := startOAuthFlow(t, handler)

	request := httptest.NewRequest(
		http.MethodGet,
		"/auth/google/callback?state="+url.QueryEscape(provider.state)+"&error=access_denied",
		nil,
	)
	for _, cookie := range flowCookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.GoogleCallback(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if provider.exchangeCalled || service.called {
		t.Error("provider denial reached token exchange or account service")
	}
}

func TestOAuthHandlerGoogleCallbackMapsAuthenticationErrors(t *testing.T) {
	tests := []struct {
		name        string
		exchangeErr error
		serviceErr  error
		wantStatus  int
	}{
		{name: "token exchange failure", exchangeErr: errors.New("exchange failed"), wantStatus: http.StatusUnauthorized},
		{name: "invalid identity", serviceErr: appoauth.ErrInvalidGoogleIdentity, wantStatus: http.StatusUnauthorized},
		{name: "link conflict", serviceErr: appoauth.ErrOAuthAccountConflict, wantStatus: http.StatusConflict},
		{name: "database failure", serviceErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeGoogleOAuthProvider{
				exchangeIdentity: appoauth.GoogleIdentity{Subject: "subject", Email: "user@example.com", Name: "User"},
				exchangeErr:      tt.exchangeErr,
			}
			service := &fakeOAuthAccountService{err: tt.serviceErr}
			handler := newTestOAuthHandler(t, provider, service)
			flowCookies := startOAuthFlow(t, handler)
			request := httptest.NewRequest(
				http.MethodGet,
				"/auth/google/callback?state="+url.QueryEscape(provider.state)+"&code=code",
				nil,
			)
			for _, cookie := range flowCookies {
				request.AddCookie(cookie)
			}
			recorder := httptest.NewRecorder()

			handler.GoogleCallback(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.exchangeErr != nil && service.called {
				t.Error("exchange failure reached account service")
			}
		})
	}
}

func TestOAuthHandlerStartGoogleHandlesProviderFailureWithoutCookies(t *testing.T) {
	provider := &fakeGoogleOAuthProvider{authorizationErr: errors.New("configuration failure")}
	handler := newTestOAuthHandler(t, provider, &fakeOAuthAccountService{})
	request := httptest.NewRequest(http.MethodGet, "/auth/google", nil)
	recorder := httptest.NewRecorder()

	handler.StartGoogle(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Error("provider failure set flow cookies")
	}
}

func newTestOAuthHandler(
	t *testing.T,
	provider *fakeGoogleOAuthProvider,
	service *fakeOAuthAccountService,
) *OAuthHandler {
	t.Helper()
	cookies, err := appoauth.NewFlowCookieManager(strings.Repeat("c", 32), false, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewFlowCookieManager() error = %v", err)
	}
	return NewOAuthHandler(provider, cookies, service)
}

func startOAuthFlow(t *testing.T, handler *OAuthHandler) []*http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/auth/google", nil)
	recorder := httptest.NewRecorder()
	handler.StartGoogle(recorder, request)
	if recorder.Code != http.StatusFound {
		t.Fatalf("start status = %d, want %d", recorder.Code, http.StatusFound)
	}
	return recorder.Result().Cookies()
}

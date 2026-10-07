package handler

import (
	"context"
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
	p.state, p.startVerifier, p.startNonce = state, verifier, nonce
	if p.authorizationErr != nil {
		return "", p.authorizationErr
	}
	return "https://accounts.example.test/authorize?state=" + url.QueryEscape(state), nil
}

func (p *fakeGoogleOAuthProvider) ExchangeIdentity(
	_ context.Context, code, verifier, nonce string,
) (appoauth.GoogleIdentity, error) {
	p.exchangeCalled = true
	p.code, p.exchangeVerifier, p.exchangeNonce = code, verifier, nonce
	return p.exchangeIdentity, p.exchangeErr
}

type fakeOAuthAccountService struct {
	createCalled   bool
	identity       appoauth.GoogleIdentity
	loginCode      appoauth.LoginCode
	createErr      error
	exchangeCalled bool
	exchangeCode   string
	pair           session.TokenPair
	exchangeErr    error
}

func (s *fakeOAuthAccountService) CreateGoogleLoginCode(
	_ context.Context, identity appoauth.GoogleIdentity,
) (appoauth.LoginCode, error) {
	s.createCalled = true
	s.identity = identity
	return s.loginCode, s.createErr
}

func (s *fakeOAuthAccountService) ExchangeLoginCode(
	_ context.Context, code string,
) (session.TokenPair, error) {
	s.exchangeCalled = true
	s.exchangeCode = code
	return s.pair, s.exchangeErr
}

func TestOAuthHandlerGoogleFlowRedirectsWithOneTimeCode(t *testing.T) {
	identity := appoauth.GoogleIdentity{Subject: "google-subject", Email: "user@example.com", Name: "Example User"}
	provider := &fakeGoogleOAuthProvider{exchangeIdentity: identity}
	service := &fakeOAuthAccountService{loginCode: appoauth.LoginCode{Code: "one-time-code"}}
	handler := newTestOAuthHandler(t, provider, service)

	startRecorder := httptest.NewRecorder()
	handler.StartGoogle(startRecorder, httptest.NewRequest(http.MethodGet, "/auth/google", nil))
	if startRecorder.Code != http.StatusFound {
		t.Fatalf("start status = %d, want %d", startRecorder.Code, http.StatusFound)
	}
	if provider.state == "" || provider.startVerifier == "" || provider.startNonce == "" {
		t.Fatal("start did not generate all OAuth flow values")
	}
	flowCookies := startRecorder.Result().Cookies()
	if len(flowCookies) != 3 {
		t.Fatalf("flow cookie count = %d, want 3", len(flowCookies))
	}

	callbackRequest := httptest.NewRequest(
		http.MethodGet,
		"/auth/google/callback?state="+url.QueryEscape(provider.state)+"&code=google-code",
		nil,
	)
	for _, cookie := range flowCookies {
		callbackRequest.AddCookie(cookie)
	}
	callbackRecorder := httptest.NewRecorder()
	handler.GoogleCallback(callbackRecorder, callbackRequest)

	if callbackRecorder.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want %d; body = %s", callbackRecorder.Code, http.StatusFound, callbackRecorder.Body.String())
	}
	redirect, err := url.Parse(callbackRecorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse success redirect: %v", err)
	}
	if redirect.String() != "https://app.example.test/auth/callback?code=one-time-code&source=google" {
		t.Errorf("success redirect = %q", redirect.String())
	}
	if strings.Contains(redirect.String(), "access") || strings.Contains(redirect.String(), "refresh") {
		t.Errorf("success redirect contains session token material: %q", redirect.String())
	}
	if !provider.exchangeCalled || provider.code != "google-code" ||
		provider.exchangeVerifier != provider.startVerifier || provider.exchangeNonce != provider.startNonce {
		t.Errorf("provider exchange did not receive the original flow values")
	}
	if !service.createCalled || service.identity != identity {
		t.Errorf("login-code call = {called:%v identity:%+v}", service.createCalled, service.identity)
	}
	if callbackRecorder.Header().Get("Cache-Control") != "no-store" {
		t.Error("callback redirect is missing Cache-Control: no-store")
	}
	if len(callbackRecorder.Result().Cookies()) != 3 {
		t.Error("callback did not clear all flow cookies")
	}
}

func TestOAuthHandlerExchangeCodeReturnsTokenPair(t *testing.T) {
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	service := &fakeOAuthAccountService{pair: session.TokenPair{
		AccessToken: "access-token", RefreshToken: "refresh-token",
		AccessExpiresIn: 900, RefreshExpiresAt: expiresAt,
	}}
	handler := newTestOAuthHandler(t, &fakeGoogleOAuthProvider{}, service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/auth/oauth/exchange", strings.NewReader(`{"code":"  one-time-code  "}`))
	request.Header.Set("Content-Type", "application/json")

	handler.ExchangeCode(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.exchangeCalled || service.exchangeCode != "one-time-code" {
		t.Errorf("exchange call = {called:%v code:%q}", service.exchangeCalled, service.exchangeCode)
	}
	assertAccessTokenResponseAndCookie(t, recorder, service.pair)
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Error("token response is missing Cache-Control: no-store")
	}
}

func TestOAuthHandlerExchangeCodeRejectsInvalidCodes(t *testing.T) {
	for _, serviceErr := range []error{
		appoauth.ErrInvalidLoginCode,
		appoauth.ErrExpiredLoginCode,
		appoauth.ErrConsumedLoginCode,
	} {
		service := &fakeOAuthAccountService{exchangeErr: serviceErr}
		handler := newTestOAuthHandler(t, &fakeGoogleOAuthProvider{}, service)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodPost, "/auth/oauth/exchange", strings.NewReader(`{"code":"bad-code"}`),
		)
		request.Header.Set("Content-Type", "application/json")
		handler.ExchangeCode(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("error %v: status = %d, want %d", serviceErr, recorder.Code, http.StatusUnauthorized)
		}
	}
}

func TestOAuthHandlerExchangeCodeRejectsBadRequest(t *testing.T) {
	for _, body := range []string{`{"code":`, `{}`, `{"code":"  "}`} {
		service := &fakeOAuthAccountService{}
		handler := newTestOAuthHandler(t, &fakeGoogleOAuthProvider{}, service)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/auth/oauth/exchange", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		handler.ExchangeCode(recorder, request)
		if recorder.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want %d", body, recorder.Code, http.StatusBadRequest)
		}
		if service.exchangeCalled {
			t.Errorf("body %q reached service", body)
		}
	}
}

func TestOAuthHandlerGoogleCallbackRejectsInvalidFlow(t *testing.T) {
	tests := []struct {
		name       string
		callback   func(state string) string
		addCookies bool
	}{
		{name: "tampered state", callback: func(string) string { return "/auth/google/callback?state=tampered&code=code" }, addCookies: true},
		{name: "missing cookies", callback: func(state string) string { return "/auth/google/callback?state=" + state + "&code=code" }},
		{name: "provider denial", callback: func(state string) string { return "/auth/google/callback?state=" + state + "&error=access_denied" }, addCookies: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeGoogleOAuthProvider{}
			service := &fakeOAuthAccountService{}
			handler := newTestOAuthHandler(t, provider, service)
			cookies := startOAuthFlow(t, handler)
			request := httptest.NewRequest(http.MethodGet, tt.callback(url.QueryEscape(provider.state)), nil)
			if tt.addCookies {
				for _, cookie := range cookies {
					request.AddCookie(cookie)
				}
			}
			recorder := httptest.NewRecorder()
			handler.GoogleCallback(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if provider.exchangeCalled || service.createCalled {
				t.Error("invalid flow reached provider exchange or account service")
			}
		})
	}
}

func TestOAuthHandlerGoogleCallbackMapsAuthenticationErrors(t *testing.T) {
	tests := []struct {
		name, kind string
		err        error
		want       int
	}{
		{name: "provider exchange", kind: "provider", err: errors.New("exchange failed"), want: http.StatusUnauthorized},
		{name: "invalid identity", kind: "service", err: appoauth.ErrInvalidGoogleIdentity, want: http.StatusUnauthorized},
		{name: "link conflict", kind: "service", err: appoauth.ErrOAuthAccountConflict, want: http.StatusConflict},
		{name: "database failure", kind: "service", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeGoogleOAuthProvider{exchangeIdentity: appoauth.GoogleIdentity{Subject: "s", Email: "u@example.com", Name: "U"}}
			service := &fakeOAuthAccountService{loginCode: appoauth.LoginCode{Code: "code"}}
			if tt.kind == "provider" {
				provider.exchangeErr = tt.err
			} else {
				service.createErr = tt.err
			}
			handler := newTestOAuthHandler(t, provider, service)
			cookies := startOAuthFlow(t, handler)
			request := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(provider.state)+"&code=code", nil)
			for _, cookie := range cookies {
				request.AddCookie(cookie)
			}
			recorder := httptest.NewRecorder()
			handler.GoogleCallback(recorder, request)
			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}
}

func TestNewOAuthHandlerRejectsInvalidSuccessRedirect(t *testing.T) {
	provider := &fakeGoogleOAuthProvider{}
	service := &fakeOAuthAccountService{}
	cookies, err := appoauth.NewFlowCookieManager(strings.Repeat("c", 32), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, redirectURL := range []string{"", "/auth/callback", "ftp://example.com/callback"} {
		if _, err := NewOAuthHandler(provider, cookies, service, session.NewRefreshCookieManager(false), redirectURL); err == nil {
			t.Errorf("NewOAuthHandler(%q) returned nil error", redirectURL)
		}
	}
}

func newTestOAuthHandler(t *testing.T, provider *fakeGoogleOAuthProvider, service *fakeOAuthAccountService) *OAuthHandler {
	t.Helper()
	cookies, err := appoauth.NewFlowCookieManager(strings.Repeat("c", 32), false, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewFlowCookieManager() error = %v", err)
	}
	handler, err := NewOAuthHandler(
		provider,
		cookies,
		service,
		session.NewRefreshCookieManager(false),
		"https://app.example.test/auth/callback?source=google",
	)
	if err != nil {
		t.Fatalf("NewOAuthHandler() error = %v", err)
	}
	return handler
}

func startOAuthFlow(t *testing.T, handler *OAuthHandler) []*http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.StartGoogle(recorder, httptest.NewRequest(http.MethodGet, "/auth/google", nil))
	if recorder.Code != http.StatusFound {
		t.Fatalf("start status = %d, want %d", recorder.Code, http.StatusFound)
	}
	return recorder.Result().Cookies()
}

package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testFrontendOrigin = "http://localhost:3000"

func TestCORSAllowsConfiguredOrigin(t *testing.T) {
	called := false
	handler := newTestCORSHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))
	request := httptest.NewRequest(http.MethodPost, "/auth/oauth/exchange", nil)
	request.Header.Set("Origin", testFrontendOrigin)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted || !called {
		t.Fatalf("status = %d, called = %v; want 202 and true", recorder.Code, called)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != testFrontendOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}
	if got := recorder.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want Origin", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want true", got)
	}
}

func TestCORSRequireAllowedOrigin(t *testing.T) {
	middleware, err := NewCORSMiddleware(testFrontendOrigin)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, origin string
		wantStatus   int
		wantCalled   bool
	}{
		{name: "allowed", origin: testFrontendOrigin, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "missing", wantStatus: http.StatusForbidden},
		{name: "different", origin: "https://evil.example", wantStatus: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := middleware.RequireAllowedOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tt.wantStatus || called != tt.wantCalled {
				t.Errorf("status = %d, called = %v; want %d, %v", recorder.Code, called, tt.wantStatus, tt.wantCalled)
			}
		})
	}
}

func TestCORSAllowsRequestsWithoutOrigin(t *testing.T) {
	called := false
	handler := newTestCORSHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	if recorder.Code != http.StatusNoContent || !called {
		t.Fatalf("status = %d, called = %v", recorder.Code, called)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want absent", got)
	}
}

func TestCORSRejectsUnconfiguredOrMalformedOrigin(t *testing.T) {
	for _, origin := range []string{"https://evil.example", "null", "http://localhost:3000/path"} {
		t.Run(origin, func(t *testing.T) {
			called := false
			handler := newTestCORSHandler(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			request := httptest.NewRequest(http.MethodPost, "/auth/oauth/exchange", nil)
			request.Header.Set("Origin", origin)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden || called {
				t.Errorf("status = %d, called = %v; want 403 and false", recorder.Code, called)
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q, want absent", got)
			}
		})
	}
}

func TestCORSHandlesValidPreflight(t *testing.T) {
	called := false
	handler := newTestCORSHandler(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	request := httptest.NewRequest(http.MethodOptions, "/auth/oauth/exchange", nil)
	request.Header.Set("Origin", testFrontendOrigin)
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type, authorization, idempotency-key")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || called {
		t.Fatalf("status = %d, called = %v; want 204 and false", recorder.Code, called)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != testFrontendOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); got != corsAllowedMethods {
		t.Errorf("Access-Control-Allow-Methods = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); got != corsAllowedHeaders {
		t.Errorf("Access-Control-Allow-Headers = %q", got)
	}
	if got := recorder.Header().Get("Access-Control-Max-Age"); got != corsMaxAge {
		t.Errorf("Access-Control-Max-Age = %q", got)
	}
}

func TestCORSRejectsInvalidPreflightMethodOrHeader(t *testing.T) {
	tests := []struct {
		name, method, headers string
	}{
		{name: "method", method: http.MethodPatch, headers: "Content-Type"},
		{name: "header", method: http.MethodPost, headers: "X-Not-Allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := newTestCORSHandler(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
			request.Header.Set("Origin", testFrontendOrigin)
			request.Header.Set("Access-Control-Request-Method", tt.method)
			request.Header.Set("Access-Control-Request-Headers", tt.headers)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusForbidden || called {
				t.Errorf("status = %d, called = %v; want 403 and false", recorder.Code, called)
			}
		})
	}
}

func TestNewCORSMiddlewareValidatesOrigin(t *testing.T) {
	for _, origin := range []string{"", "/relative", "ftp://example.com", "https://example.com/path", "https://user@example.com"} {
		if _, err := NewCORSMiddleware(origin); err == nil {
			t.Errorf("NewCORSMiddleware(%q) returned nil error", origin)
		}
	}
	middleware, err := NewCORSMiddleware("HTTPS://EXAMPLE.COM/")
	if err != nil {
		t.Fatalf("NewCORSMiddleware() error = %v", err)
	}
	if middleware.allowedOrigin != "https://example.com" {
		t.Errorf("allowed origin = %q", middleware.allowedOrigin)
	}
}

func newTestCORSHandler(t *testing.T, next http.Handler) http.Handler {
	t.Helper()
	middleware, err := NewCORSMiddleware(testFrontendOrigin)
	if err != nil {
		t.Fatalf("NewCORSMiddleware() error = %v", err)
	}
	return middleware.Allow(next)
}

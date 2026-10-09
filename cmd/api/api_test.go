package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/handler"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type healthyDatabase struct{}

func (healthyDatabase) Ping(context.Context) error { return nil }

func TestNewAPIHandlerServesHealthThroughGlobalMiddleware(t *testing.T) {
	apiHandler := newTestAPIHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()

	apiHandler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != "alive\n" {
		t.Errorf("body = %q, want %q", response.Body.String(), "alive\n")
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header is missing")
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", response.Header().Get("X-Content-Type-Options"), "nosniff")
	}
	if response.Header().Get("Content-Security-Policy") == "" {
		t.Error("Content-Security-Policy header is missing")
	}
	if response.Header().Get("Vary") != "Origin" {
		t.Errorf("Vary = %q, want %q", response.Header().Get("Vary"), "Origin")
	}
}

func TestNewAPIHandlerProtectsAuthenticatedRoutes(t *testing.T) {
	apiHandler := newTestAPIHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/me", nil)
	response := httptest.NewRecorder()

	apiHandler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want %q", response.Header().Get("WWW-Authenticate"), "Bearer")
	}
}

func TestAPIHandlerCORSBoundary(t *testing.T) {
	t.Run("allowed preflight", func(t *testing.T) {
		apiHandler := newTestAPIHandler(t)
		request := httptest.NewRequest(http.MethodOptions, "/auth/register", nil)
		request.Header.Set("Origin", "http://localhost:3000")
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "Content-Type")
		response := httptest.NewRecorder()

		apiHandler.ServeHTTP(response, request)

		if response.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusNoContent, response.Body.String())
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
			t.Errorf("Access-Control-Allow-Origin = %q", response.Header().Get("Access-Control-Allow-Origin"))
		}
		if response.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Errorf("Access-Control-Allow-Credentials = %q", response.Header().Get("Access-Control-Allow-Credentials"))
		}
	})

	t.Run("disallowed origin", func(t *testing.T) {
		apiHandler := newTestAPIHandler(t)
		request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
		request.Header.Set("Origin", "https://evil.example")
		response := httptest.NewRecorder()

		apiHandler.ServeHTTP(response, request)

		if response.Code != http.StatusForbidden || response.Body.String() != "CORS origin is not allowed\n" {
			t.Fatalf("response = {%d %q}, want CORS 403", response.Code, response.Body.String())
		}
	})

	t.Run("cookie endpoints require origin", func(t *testing.T) {
		for _, path := range []string{"/auth/refresh", "/auth/logout"} {
			t.Run(path, func(t *testing.T) {
				apiHandler := newTestAPIHandler(t)
				request := httptest.NewRequest(http.MethodPost, path, nil)
				response := httptest.NewRecorder()

				apiHandler.ServeHTTP(response, request)

				if response.Code != http.StatusForbidden || response.Body.String() != "Request origin is not allowed\n" {
					t.Fatalf("response = {%d %q}, want origin-required 403", response.Code, response.Body.String())
				}
			})
		}
	})
}

func TestAPIHandlerAllowsBearerClientsWithoutOrigin(t *testing.T) {
	apiHandler, tokenManager := newTestAPIHandlerWithTokenManager(t)
	token, err := tokenManager.GenerateToken(42, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("generate customer token: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/hotels", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	apiHandler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || response.Body.String() != "Forbidden\n" {
		t.Fatalf("originless bearer response = {%d %q}, want role-based 403", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/hotels", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Origin", "https://evil.example")
	response = httptest.NewRecorder()
	apiHandler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || response.Body.String() != "CORS origin is not allowed\n" {
		t.Fatalf("cross-origin bearer response = {%d %q}, want CORS 403", response.Code, response.Body.String())
	}
}

func TestAPIHandlerEnforcesJSONAndMethodBoundaries(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		body        string
		contentType string
		wantStatus  int
	}{
		{
			name: "missing content type", method: http.MethodPost, path: "/auth/register",
			body: `{}`, wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name: "wrong content type", method: http.MethodPost, path: "/auth/register",
			body: `{}`, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name: "oversized JSON", method: http.MethodPost, path: "/auth/register",
			body:        `{"padding":"` + strings.Repeat("a", 70<<10) + `"}`,
			contentType: "application/json", wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "unsupported method", method: http.MethodPost, path: "/health/live",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiHandler := newTestAPIHandler(t)
			request := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.contentType != "" {
				request.Header.Set("Content-Type", tt.contentType)
			}
			response := httptest.NewRecorder()

			apiHandler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, tt.wantStatus, response.Body.String())
			}
			assertGlobalSecurityHeaders(t, response.Header())
		})
	}
}

func TestAPIHandlerRequestIDsSecurityHeadersAndRecovery(t *testing.T) {
	validRequestID := "client-request_123"
	tests := []struct {
		name          string
		path          string
		requestID     string
		wantStatus    int
		wantRequestID string
	}{
		{name: "success", path: "/health/live", requestID: validRequestID, wantStatus: http.StatusOK, wantRequestID: validRequestID},
		{name: "authentication error", path: "/me", wantStatus: http.StatusUnauthorized},
		{name: "unknown route", path: "/does-not-exist", wantStatus: http.StatusNotFound},
		{name: "unsafe request ID", path: "/health/live", requestID: "unsafe request id", wantStatus: http.StatusOK},
		{name: "recovered panic", path: "/reviews/1", wantStatus: http.StatusInternalServerError},
	}
	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiHandler := newTestAPIHandler(t)
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.requestID != "" {
				request.Header.Set("X-Request-ID", tt.requestID)
			}
			response := httptest.NewRecorder()

			apiHandler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, tt.wantStatus, response.Body.String())
			}
			requestID := response.Header().Get("X-Request-ID")
			if tt.wantRequestID != "" {
				if requestID != tt.wantRequestID {
					t.Errorf("X-Request-ID = %q, want %q", requestID, tt.wantRequestID)
				}
			} else if !uuidPattern.MatchString(requestID) {
				t.Errorf("generated X-Request-ID = %q, want UUIDv4", requestID)
			}
			assertGlobalSecurityHeaders(t, response.Header())
			if tt.name == "recovered panic" && response.Body.String() != "Internal Server Error\n" {
				t.Errorf("panic body = %q, want generic error", response.Body.String())
			}
		})
	}
}

func assertGlobalSecurityHeaders(t *testing.T, header http.Header) {
	t.Helper()
	want := map[string]string{
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
	}
	for name, value := range want {
		if header.Get(name) != value {
			t.Errorf("%s = %q, want %q", name, header.Get(name), value)
		}
	}
}

func newTestAPIHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, _ := newTestAPIHandlerWithTokenManager(t)
	return handler
}

func newTestAPIHandlerWithTokenManager(t *testing.T) (http.Handler, *auth.TokenManager) {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tokenManager, err := auth.NewTokenManager(
		"api-boundary-test-secret-at-least-32-bytes",
		"ryoko-api-boundary-test",
		"ryoko-api-boundary-test",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}
	corsMiddleware, err := middleware.NewCORSMiddleware("http://localhost:3000")
	if err != nil {
		t.Fatalf("create CORS middleware: %v", err)
	}
	authRateLimiter, err := middleware.NewIPRateLimiter(
		map[string]middleware.RateLimitPolicy{
			"register":               {Requests: 5, Window: time.Minute},
			"login":                  {Requests: 5, Window: time.Minute},
			"password-reset-request": {Requests: 3, Window: time.Minute},
			"password-reset-verify":  {Requests: 5, Window: time.Minute},
			"password-reset-confirm": {Requests: 5, Window: time.Minute},
			"oauth-exchange":         {Requests: 5, Window: time.Minute},
			"refresh":                {Requests: 5, Window: time.Minute},
		},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("create rate limiter: %v", err)
	}

	return newAPIHandler(apiDependencies{
		healthHandler:        handler.NewHealthHandler(healthyDatabase{}, time.Second, logger),
		hotelHandler:         new(handler.HotelHandler),
		roomTypeHandler:      new(handler.RoomTypeHandler),
		amenityHandler:       new(handler.AmenityHandler),
		userHandler:          new(handler.UserHandler),
		authHandler:          new(handler.AuthHandler),
		oauthHandler:         new(handler.OAuthHandler),
		passwordResetHandler: new(handler.PasswordResetHandler),
		bookingHandler:       new(handler.BookingHandler),
		adminBookingHandler:  new(handler.AdminBookingHandler),
		reviewHandler:        new(handler.ReviewHandler),
		hotelImageHandler:    new(handler.HotelImageHandler),
		inventoryHandler:     new(handler.InventoryHandler),
		authMiddleware:       middleware.NewAuthMiddleware(tokenManager),
		corsMiddleware:       corsMiddleware,
		authRateLimiter:      authRateLimiter,
		logger:               logger,
	}), tokenManager
}

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/auth"
)

func TestRequireRole(t *testing.T) {
	tests := []struct {
		name       string
		principal  *auth.Principal
		wantStatus int
		wantCalled bool
		wantAuth   string
	}{
		{
			name:       "missing principal",
			wantStatus: http.StatusUnauthorized,
			wantAuth:   "Bearer",
		},
		{
			name: "customer is forbidden",
			principal: &auth.Principal{
				UserID: 42,
				Role:   auth.RoleCustomer,
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "admin reaches handler",
			principal: &auth.Principal{
				UserID: 7,
				Role:   auth.RoleAdmin,
			},
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})
			handler := RequireRole(auth.RoleAdmin, next)

			request := httptest.NewRequest(http.MethodPost, "/hotels", nil)
			if tt.principal != nil {
				ctx := context.WithValue(
					request.Context(),
					principalContextKey{},
					*tt.principal,
				)
				request = request.WithContext(ctx)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if called != tt.wantCalled {
				t.Errorf("handler called = %t, want %t", called, tt.wantCalled)
			}
			if got := recorder.Header().Get("WWW-Authenticate"); got != tt.wantAuth {
				t.Errorf("WWW-Authenticate = %q, want %q", got, tt.wantAuth)
			}
		})
	}
}

func TestAuthenticateThenRequireAdminRole(t *testing.T) {
	tokenManager, err := auth.NewTokenManager(
		"test-secret-that-is-at-least-32-bytes-long",
		"ryoko-test",
		"ryoko-test-api",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}
	adminToken, err := tokenManager.GenerateToken(7, auth.RoleAdmin)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	customerToken, err := tokenManager.GenerateToken(42, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("generate customer token: %v", err)
	}

	tests := []struct {
		name       string
		token      string
		wantStatus int
		wantUserID int64
	}{
		{name: "missing token", wantStatus: http.StatusUnauthorized},
		{name: "customer token", token: customerToken, wantStatus: http.StatusForbidden},
		{name: "admin token", token: adminToken, wantStatus: http.StatusNoContent, wantUserID: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reachedUserID int64
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				principal, ok := PrincipalFromContext(r.Context())
				if !ok {
					t.Fatal("principal missing after authentication")
				}
				reachedUserID = principal.UserID
				w.WriteHeader(http.StatusNoContent)
			})
			authMiddleware := NewAuthMiddleware(tokenManager)
			handler := authMiddleware.Authenticate(
				RequireRole(auth.RoleAdmin, next),
			)

			request := httptest.NewRequest(http.MethodPost, "/hotels", nil)
			if tt.token != "" {
				request.Header.Set("Authorization", "Bearer "+tt.token)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if reachedUserID != tt.wantUserID {
				t.Errorf("handler user ID = %d, want %d", reachedUserID, tt.wantUserID)
			}
		})
	}
}

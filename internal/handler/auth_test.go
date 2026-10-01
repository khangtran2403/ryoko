package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/session"
)

type fakeAuthQueries struct {
	loginCalled bool
	loginEmail  string
	loginResult sqlc.GetUserForLoginRow
	loginErr    error
}

func (f *fakeAuthQueries) RegisterUser(
	context.Context,
	sqlc.RegisterUserParams,
) (sqlc.RegisterUserRow, error) {
	return sqlc.RegisterUserRow{}, errors.New("unexpected RegisterUser call")
}

func (f *fakeAuthQueries) GetUserForLogin(
	_ context.Context,
	email string,
) (sqlc.GetUserForLoginRow, error) {
	f.loginCalled = true
	f.loginEmail = email
	return f.loginResult, f.loginErr
}

type fakeAuthSessionService struct {
	issueCalled bool
	issueUserID int64
	issueRole   string
	issueResult session.TokenPair
	issueErr    error

	rotateCalled bool
	rotateToken  string
	rotateResult session.TokenPair
	rotateErr    error
	revokeCalled bool
	revokeToken  string
	revokeErr    error
}

func (f *fakeAuthSessionService) IssueTokenPair(
	_ context.Context,
	userID int64,
	role string,
) (session.TokenPair, error) {
	f.issueCalled = true
	f.issueUserID = userID
	f.issueRole = role
	return f.issueResult, f.issueErr
}

func (f *fakeAuthSessionService) RotateTokenPair(
	_ context.Context,
	rawRefreshToken string,
) (session.TokenPair, error) {
	f.rotateCalled = true
	f.rotateToken = rawRefreshToken
	return f.rotateResult, f.rotateErr
}

func (f *fakeAuthSessionService) RevokeRefreshToken(
	_ context.Context,
	rawRefreshToken string,
) error {
	f.revokeCalled = true
	f.revokeToken = rawRefreshToken
	return f.revokeErr
}

func TestAuthHandlerLoginIssuesTokenPair(t *testing.T) {
	passwordHash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	refreshExpiry := time.Date(2030, 1, 31, 12, 0, 0, 0, time.UTC)
	queries := &fakeAuthQueries{loginResult: sqlc.GetUserForLoginRow{
		ID:           42,
		Email:        "user@example.com",
		PasswordHash: pgtype.Text{String: passwordHash, Valid: true},
		Role:         auth.RoleCustomer,
	}}
	sessions := &fakeAuthSessionService{issueResult: session.TokenPair{
		AccessToken:      "access-token",
		RefreshToken:     "refresh-token",
		AccessExpiresIn:  900,
		RefreshExpiresAt: refreshExpiry,
	}}
	handler := NewAuthHandler(queries, sessions)
	recorder := performAuthRequest(
		handler.LoginUser,
		`{"email":"  USER@EXAMPLE.COM ","password":"correct-password"}`,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !queries.loginCalled || queries.loginEmail != "user@example.com" {
		t.Errorf("login query = {called:%v email:%q}", queries.loginCalled, queries.loginEmail)
	}
	if !sessions.issueCalled || sessions.issueUserID != 42 || sessions.issueRole != auth.RoleCustomer {
		t.Errorf("issue call = {called:%v user:%d role:%q}", sessions.issueCalled, sessions.issueUserID, sessions.issueRole)
	}
	assertAuthTokenResponse(t, recorder, sessions.issueResult)
}

func TestAuthHandlerLoginRejectsInvalidRequestsAndCredentials(t *testing.T) {
	passwordHash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	tests := []struct {
		name       string
		body       string
		login      sqlc.GetUserForLoginRow
		loginErr   error
		wantStatus int
		wantQuery  bool
	}{
		{name: "malformed JSON", body: `{"email":`, wantStatus: http.StatusBadRequest},
		{name: "missing email", body: `{"password":"password"}`, wantStatus: http.StatusBadRequest},
		{name: "missing password", body: `{"email":"user@example.com"}`, wantStatus: http.StatusBadRequest},
		{name: "unknown email", body: `{"email":"user@example.com","password":"password"}`, loginErr: pgx.ErrNoRows, wantStatus: http.StatusUnauthorized, wantQuery: true},
		{
			name: "wrong password", body: `{"email":"user@example.com","password":"wrong"}`,
			login:      sqlc.GetUserForLoginRow{ID: 1, PasswordHash: pgtype.Text{String: passwordHash, Valid: true}, Role: auth.RoleCustomer},
			wantStatus: http.StatusUnauthorized, wantQuery: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries := &fakeAuthQueries{loginResult: tt.login, loginErr: tt.loginErr}
			sessions := &fakeAuthSessionService{}
			handler := NewAuthHandler(queries, sessions)
			recorder := performAuthRequest(handler.LoginUser, tt.body)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if queries.loginCalled != tt.wantQuery {
				t.Errorf("login query called = %v, want %v", queries.loginCalled, tt.wantQuery)
			}
			if sessions.issueCalled {
				t.Fatal("session service called for rejected login")
			}
		})
	}
}

func TestAuthHandlerLoginMapsSessionFailure(t *testing.T) {
	passwordHash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	queries := &fakeAuthQueries{loginResult: sqlc.GetUserForLoginRow{
		ID: 1, PasswordHash: pgtype.Text{String: passwordHash, Valid: true}, Role: auth.RoleCustomer,
	}}
	sessions := &fakeAuthSessionService{issueErr: errors.New("database unavailable")}
	handler := NewAuthHandler(queries, sessions)
	recorder := performAuthRequest(
		handler.LoginUser,
		`{"email":"user@example.com","password":"correct-password"}`,
	)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestAuthHandlerRefreshRotatesTokenPair(t *testing.T) {
	refreshExpiry := time.Date(2030, 2, 1, 12, 0, 0, 0, time.UTC)
	sessions := &fakeAuthSessionService{rotateResult: session.TokenPair{
		AccessToken:      "new-access-token",
		RefreshToken:     "new-refresh-token",
		AccessExpiresIn:  900,
		RefreshExpiresAt: refreshExpiry,
	}}
	handler := NewAuthHandler(&fakeAuthQueries{}, sessions)
	recorder := performAuthRequest(handler.RefreshToken, `{"refresh_token":"  old-refresh-token  "}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !sessions.rotateCalled || sessions.rotateToken != "old-refresh-token" {
		t.Errorf("rotate call = {called:%v token:%q}", sessions.rotateCalled, sessions.rotateToken)
	}
	assertAuthTokenResponse(t, recorder, sessions.rotateResult)
}

func TestAuthHandlerRefreshRejectsInvalidRequests(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"refresh_token":`},
		{name: "missing token", body: `{}`},
		{name: "blank token", body: `{"refresh_token":"  "}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sessions := &fakeAuthSessionService{}
			handler := NewAuthHandler(&fakeAuthQueries{}, sessions)
			recorder := performAuthRequest(handler.RefreshToken, tt.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if sessions.rotateCalled {
				t.Fatal("session service called for invalid request")
			}
		})
	}
}

func TestAuthHandlerRefreshUsesUniformUnauthorizedResponse(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "invalid", err: session.ErrInvalidRefreshToken},
		{name: "expired", err: session.ErrRefreshTokenExpired},
		{name: "revoked", err: session.ErrRefreshTokenRevoked},
		{name: "reused", err: session.ErrRefreshTokenReused},
		{name: "wrapped reused", err: errors.Join(errors.New("rotation failed"), session.ErrRefreshTokenReused)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions := &fakeAuthSessionService{rotateErr: tt.err}
			handler := NewAuthHandler(&fakeAuthQueries{}, sessions)
			recorder := performAuthRequest(handler.RefreshToken, `{"refresh_token":"refresh-token"}`)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
			if recorder.Body.String() != "Invalid refresh token\n" {
				t.Errorf("body = %q, want uniform invalid-token message", recorder.Body.String())
			}
		})
	}
}

func TestAuthHandlerRefreshMapsUnexpectedError(t *testing.T) {
	sessions := &fakeAuthSessionService{rotateErr: errors.New("database unavailable")}
	handler := NewAuthHandler(&fakeAuthQueries{}, sessions)
	recorder := performAuthRequest(handler.RefreshToken, `{"refresh_token":"refresh-token"}`)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestAuthHandlerLogout(t *testing.T) {
	sessions := &fakeAuthSessionService{}
	handler := NewAuthHandler(&fakeAuthQueries{}, sessions)
	recorder := performAuthRequest(handler.Logout, `{"refresh_token":"  refresh-token  "}`)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", recorder.Body.String())
	}
	if !sessions.revokeCalled || sessions.revokeToken != "refresh-token" {
		t.Errorf("revoke call = {called:%v token:%q}", sessions.revokeCalled, sessions.revokeToken)
	}
}

func TestAuthHandlerLogoutRejectsInvalidRequests(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"refresh_token":`},
		{name: "missing token", body: `{}`},
		{name: "blank token", body: `{"refresh_token":"  "}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sessions := &fakeAuthSessionService{}
			handler := NewAuthHandler(&fakeAuthQueries{}, sessions)
			recorder := performAuthRequest(handler.Logout, tt.body)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if sessions.revokeCalled {
				t.Fatal("session service called for invalid request")
			}
		})
	}
}

func TestAuthHandlerLogoutMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid token", err: session.ErrInvalidRefreshToken, wantStatus: http.StatusBadRequest},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions := &fakeAuthSessionService{revokeErr: tt.err}
			handler := NewAuthHandler(&fakeAuthQueries{}, sessions)
			recorder := performAuthRequest(handler.Logout, `{"refresh_token":"refresh-token"}`)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}

func assertAuthTokenResponse(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	want session.TokenPair,
) {
	t.Helper()
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	var response LoginResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.AccessToken != want.AccessToken ||
		response.RefreshToken != want.RefreshToken ||
		response.TokenType != "Bearer" ||
		response.ExpiresIn != want.AccessExpiresIn ||
		!response.RefreshExpiresAt.Equal(want.RefreshExpiresAt) {
		t.Errorf("response = %+v, want pair %+v", response, want)
	}
}

func performAuthRequest(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

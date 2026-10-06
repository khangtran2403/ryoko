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

func (f *fakeAuthQueries) RegisterUser(context.Context, sqlc.RegisterUserParams) (sqlc.RegisterUserRow, error) {
	return sqlc.RegisterUserRow{}, errors.New("unexpected RegisterUser call")
}

func (f *fakeAuthQueries) GetUserForLogin(_ context.Context, email string) (sqlc.GetUserForLoginRow, error) {
	f.loginCalled, f.loginEmail = true, email
	return f.loginResult, f.loginErr
}

type fakeAuthSessionService struct {
	issueCalled  bool
	issueUserID  int64
	issueRole    string
	issueResult  session.TokenPair
	issueErr     error
	rotateCalled bool
	rotateToken  string
	rotateResult session.TokenPair
	rotateErr    error
	revokeCalled bool
	revokeToken  string
	revokeErr    error
}

func (f *fakeAuthSessionService) IssueTokenPair(_ context.Context, userID int64, role string) (session.TokenPair, error) {
	f.issueCalled, f.issueUserID, f.issueRole = true, userID, role
	return f.issueResult, f.issueErr
}

func (f *fakeAuthSessionService) RotateTokenPair(_ context.Context, token string) (session.TokenPair, error) {
	f.rotateCalled, f.rotateToken = true, token
	return f.rotateResult, f.rotateErr
}

func (f *fakeAuthSessionService) RevokeRefreshToken(_ context.Context, token string) error {
	f.revokeCalled, f.revokeToken = true, token
	return f.revokeErr
}

func TestAuthHandlerLoginSetsRefreshCookieAndReturnsAccessToken(t *testing.T) {
	passwordHash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	queries := &fakeAuthQueries{loginResult: sqlc.GetUserForLoginRow{
		ID: 42, Email: "user@example.com",
		PasswordHash: pgtype.Text{String: passwordHash, Valid: true}, Role: auth.RoleCustomer,
	}}
	sessions := &fakeAuthSessionService{issueResult: session.TokenPair{
		AccessToken: "access-token", RefreshToken: "refresh-token",
		AccessExpiresIn: 900, RefreshExpiresAt: expiry,
	}}
	handler, _ := newTestAuthHandler(queries, sessions)
	recorder := performAuthRequest(handler.LoginUser, `{"email":"  USER@EXAMPLE.COM ","password":"correct-password"}`, "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if !queries.loginCalled || queries.loginEmail != "user@example.com" {
		t.Errorf("login query = {called:%v email:%q}", queries.loginCalled, queries.loginEmail)
	}
	if !sessions.issueCalled || sessions.issueUserID != 42 || sessions.issueRole != auth.RoleCustomer {
		t.Errorf("issue call = {called:%v user:%d role:%q}", sessions.issueCalled, sessions.issueUserID, sessions.issueRole)
	}
	assertAccessTokenResponseAndCookie(t, recorder, sessions.issueResult)
}

func TestAuthHandlerLoginRejectsInvalidRequestsAndCredentials(t *testing.T) {
	passwordHash, err := auth.HashPassword("correct-password")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, body string
		login      sqlc.GetUserForLoginRow
		loginErr   error
		want       int
		wantQuery  bool
	}{
		{name: "malformed", body: `{"email":`, want: http.StatusBadRequest},
		{name: "missing email", body: `{"password":"password"}`, want: http.StatusBadRequest},
		{name: "missing password", body: `{"email":"user@example.com"}`, want: http.StatusBadRequest},
		{name: "unknown", body: `{"email":"user@example.com","password":"password"}`, loginErr: pgx.ErrNoRows, want: http.StatusUnauthorized, wantQuery: true},
		{name: "wrong password", body: `{"email":"user@example.com","password":"wrong"}`, login: sqlc.GetUserForLoginRow{
			ID: 1, PasswordHash: pgtype.Text{String: passwordHash, Valid: true}, Role: auth.RoleCustomer,
		}, want: http.StatusUnauthorized, wantQuery: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries := &fakeAuthQueries{loginResult: tt.login, loginErr: tt.loginErr}
			sessions := &fakeAuthSessionService{}
			handler, _ := newTestAuthHandler(queries, sessions)
			recorder := performAuthRequest(handler.LoginUser, tt.body, "")
			if recorder.Code != tt.want || queries.loginCalled != tt.wantQuery || sessions.issueCalled {
				t.Errorf("status=%d query=%v issue=%v", recorder.Code, queries.loginCalled, sessions.issueCalled)
			}
		})
	}
}

func TestAuthHandlerRefreshReadsAndRotatesCookie(t *testing.T) {
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	sessions := &fakeAuthSessionService{rotateResult: session.TokenPair{
		AccessToken: "new-access", RefreshToken: "new-refresh",
		AccessExpiresIn: 900, RefreshExpiresAt: expiry,
	}}
	handler, cookies := newTestAuthHandler(&fakeAuthQueries{}, sessions)
	recorder := performCookieAuthRequest(t, handler.RefreshToken, cookies, "old-refresh")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if !sessions.rotateCalled || sessions.rotateToken != "old-refresh" {
		t.Errorf("rotate call = {called:%v token:%q}", sessions.rotateCalled, sessions.rotateToken)
	}
	assertAccessTokenResponseAndCookie(t, recorder, sessions.rotateResult)
}

func TestAuthHandlerRefreshRejectsMissingOrInvalidCookie(t *testing.T) {
	tests := []struct {
		name, token string
		err         error
	}{
		{name: "missing cookie"},
		{name: "invalid", token: "token", err: session.ErrInvalidRefreshToken},
		{name: "expired", token: "token", err: session.ErrRefreshTokenExpired},
		{name: "revoked", token: "token", err: session.ErrRefreshTokenRevoked},
		{name: "reused", token: "token", err: session.ErrRefreshTokenReused},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions := &fakeAuthSessionService{rotateErr: tt.err}
			handler, cookies := newTestAuthHandler(&fakeAuthQueries{}, sessions)
			var recorder *httptest.ResponseRecorder
			if tt.token == "" {
				recorder = performAuthRequest(handler.RefreshToken, "", "")
			} else {
				recorder = performCookieAuthRequest(t, handler.RefreshToken, cookies, tt.token)
			}
			if recorder.Code != http.StatusUnauthorized || recorder.Body.String() != "Invalid refresh token\n" {
				t.Errorf("status=%d body=%q", recorder.Code, recorder.Body.String())
			}
			assertClearedRefreshCookie(t, recorder)
		})
	}
}

func TestAuthHandlerRefreshMapsUnexpectedErrorWithoutClearingCookie(t *testing.T) {
	sessions := &fakeAuthSessionService{rotateErr: errors.New("database unavailable")}
	handler, cookies := newTestAuthHandler(&fakeAuthQueries{}, sessions)
	recorder := performCookieAuthRequest(t, handler.RefreshToken, cookies, "refresh-token")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Error("transient refresh failure unexpectedly changed the cookie")
	}
}

func TestAuthHandlerLogoutRevokesAndClearsCookie(t *testing.T) {
	sessions := &fakeAuthSessionService{}
	handler, cookies := newTestAuthHandler(&fakeAuthQueries{}, sessions)
	recorder := performCookieAuthRequest(t, handler.Logout, cookies, "refresh-token")
	if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if !sessions.revokeCalled || sessions.revokeToken != "refresh-token" {
		t.Errorf("revoke call = {called:%v token:%q}", sessions.revokeCalled, sessions.revokeToken)
	}
	assertClearedRefreshCookie(t, recorder)
}

func TestAuthHandlerLogoutWithoutCookieIsIdempotent(t *testing.T) {
	sessions := &fakeAuthSessionService{}
	handler, _ := newTestAuthHandler(&fakeAuthQueries{}, sessions)
	recorder := performAuthRequest(handler.Logout, "", "")
	if recorder.Code != http.StatusNoContent || sessions.revokeCalled {
		t.Errorf("status=%d revokeCalled=%v", recorder.Code, sessions.revokeCalled)
	}
	assertClearedRefreshCookie(t, recorder)
}

func TestAuthHandlerLogoutMapsUnexpectedErrorAndStillClearsCookie(t *testing.T) {
	sessions := &fakeAuthSessionService{revokeErr: errors.New("database unavailable")}
	handler, cookies := newTestAuthHandler(&fakeAuthQueries{}, sessions)
	recorder := performCookieAuthRequest(t, handler.Logout, cookies, "refresh-token")
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	assertClearedRefreshCookie(t, recorder)
}

func newTestAuthHandler(queries authQueries, sessions authSessionService) (*AuthHandler, *session.RefreshCookieManager) {
	cookies := session.NewRefreshCookieManager(false)
	return NewAuthHandler(queries, sessions, cookies), cookies
}

func performCookieAuthRequest(
	t *testing.T,
	handler http.HandlerFunc,
	cookies *session.RefreshCookieManager,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	cookieRecorder := httptest.NewRecorder()
	if err := cookies.Set(cookieRecorder, token, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("set request refresh cookie: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/auth", nil)
	request.AddCookie(cookieRecorder.Result().Cookies()[0])
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func performAuthRequest(handler http.HandlerFunc, body, refreshToken string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/auth", strings.NewReader(body))
	if refreshToken != "" {
		request.AddCookie(&http.Cookie{Name: "ryoko_refresh_token", Value: refreshToken, Path: "/auth"})
	}
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func assertAccessTokenResponseAndCookie(t *testing.T, recorder *httptest.ResponseRecorder, want session.TokenPair) {
	t.Helper()
	var raw map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if raw["access_token"] != want.AccessToken || raw["token_type"] != "Bearer" {
		t.Errorf("response = %+v", raw)
	}
	if _, exists := raw["refresh_token"]; exists {
		t.Error("response exposed refresh_token")
	}
	responseCookies := recorder.Result().Cookies()
	if len(responseCookies) != 1 {
		t.Fatalf("response cookie count = %d, want 1", len(responseCookies))
	}
	cookie := responseCookies[0]
	if cookie.Value != want.RefreshToken || !cookie.HttpOnly || cookie.Path != "/auth" || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("refresh cookie = %+v", cookie)
	}
}

func assertClearedRefreshCookie(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Value != "" {
		t.Errorf("cleared cookies = %+v", cookies)
	}
}

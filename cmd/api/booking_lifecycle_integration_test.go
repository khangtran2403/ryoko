package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	adminbooking "github.com/khangtran2403/ryoko/internal/admin_booking"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/handler"
	"github.com/khangtran2403/ryoko/internal/middleware"
	appoauth "github.com/khangtran2403/ryoko/internal/oauth"
	"github.com/khangtran2403/ryoko/internal/passwordreset"
	"github.com/khangtran2403/ryoko/internal/review"
	"github.com/khangtran2403/ryoko/internal/session"
)

const (
	lifecycleJWTSecret   = "booking-lifecycle-test-secret-at-least-32-bytes"
	lifecycleJWTIssuer   = "ryoko-test"
	lifecycleJWTAudience = "ryoko-test-api"
	lifecycleOrigin      = "http://localhost:3000"
	lifecycleResetPepper = "password-reset-lifecycle-pepper-at-least-32-bytes"
	lifecycleOAuthSecret = "oauth-lifecycle-cookie-secret-at-least-32-bytes"
)

func TestBookingHTTPLifecycleIntegration(t *testing.T) {
	pool := newAPIIntegrationPool(t)
	server := newBookingLifecycleServer(t, pool)
	defer server.Close()

	hotelID, roomTypeID := insertHTTPBookingFixtures(t, pool)
	client := server.Client()

	doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/register", "", "", map[string]any{
		"email":     "http-booking@example.com",
		"password":  "correct horse battery staple",
		"full_name": "HTTP Booking User",
		"phone":     "+84900000000",
	}, http.StatusCreated)

	loginBody := doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/login", "", "", map[string]any{
		"email":    "http-booking@example.com",
		"password": "correct horse battery staple",
	}, http.StatusOK)
	var login handler.LoginResponse
	decodeAPIResponse(t, loginBody, &login)
	if login.AccessToken == "" {
		t.Fatal("login access token is empty")
	}

	checkIn := utcDateOnly(time.Now()).AddDate(1, 0, 0)
	checkOut := checkIn.AddDate(0, 0, 3)
	searchURL := fmt.Sprintf(
		"%s/hotels/search?city=Test%%20City&check_in=%s&check_out=%s&rooms_count=1&guest_count=2",
		server.URL,
		checkIn.Format(time.DateOnly),
		checkOut.Format(time.DateOnly),
	)
	assertSearchReturnsHotel(t, client, searchURL, hotelID, true)

	createURL := server.URL + "/room-types/" + strconv.FormatInt(roomTypeID, 10) + "/bookings"
	createRequest := map[string]any{
		"check_in":    checkIn.Format(time.DateOnly),
		"check_out":   checkOut.Format(time.DateOnly),
		"rooms_count": 1,
		"guest_count": 2,
	}
	createBody := doAPIRequest(
		t, client, http.MethodPost, createURL, login.AccessToken,
		"booking-lifecycle-key", createRequest, http.StatusCreated,
	)
	var created handler.BookingResponse
	decodeAPIResponse(t, createBody, &created)
	if created.ID <= 0 || created.RoomTypeID != roomTypeID || created.Status != "confirmed" {
		t.Fatalf("created booking = %+v, want confirmed booking for room type %d", created, roomTypeID)
	}
	if created.TotalPrice != "300.00" {
		t.Errorf("created total price = %q, want %q", created.TotalPrice, "300.00")
	}

	duplicateBody := doAPIRequest(
		t, client, http.MethodPost, createURL, login.AccessToken,
		"booking-lifecycle-key", createRequest, http.StatusCreated,
	)
	var duplicate handler.BookingResponse
	decodeAPIResponse(t, duplicateBody, &duplicate)
	if duplicate.ID != created.ID {
		t.Errorf("idempotent booking ID = %d, want %d", duplicate.ID, created.ID)
	}
	assertBookingInventory(t, pool, roomTypeID, 1, 3)
	assertSingleBooking(t, pool)
	assertSearchReturnsHotel(t, client, searchURL, hotelID, false)

	bookingURL := server.URL + "/me/bookings/" + strconv.FormatInt(created.ID, 10)
	fetchedBody := doAPIRequest(t, client, http.MethodGet, bookingURL, login.AccessToken, "", nil, http.StatusOK)
	var fetched handler.BookingResponse
	decodeAPIResponse(t, fetchedBody, &fetched)
	if fetched.ID != created.ID || fetched.Status != "confirmed" {
		t.Errorf("fetched booking = %+v, want confirmed booking %d", fetched, created.ID)
	}

	listBody := doAPIRequest(t, client, http.MethodGet, server.URL+"/me/bookings", login.AccessToken, "", nil, http.StatusOK)
	var list handler.ListBookingsResponse
	decodeAPIResponse(t, listBody, &list)
	if len(list.Bookings) != 1 || list.Bookings[0].ID != created.ID {
		t.Errorf("listed bookings = %+v, want only booking %d", list.Bookings, created.ID)
	}

	cancelBody := doAPIRequest(
		t, client, http.MethodPost, bookingURL+"/cancel",
		login.AccessToken, "", nil, http.StatusOK,
	)
	var cancelled handler.BookingResponse
	decodeAPIResponse(t, cancelBody, &cancelled)
	if cancelled.ID != created.ID || cancelled.Status != "cancelled" {
		t.Errorf("cancelled booking = %+v, want cancelled booking %d", cancelled, created.ID)
	}

	historyBody := doAPIRequest(
		t, client, http.MethodGet, bookingURL+"/history",
		login.AccessToken, "", nil, http.StatusOK,
	)
	var history []sqlc.BookingStatusHistory
	decodeAPIResponse(t, historyBody, &history)
	if len(history) != 2 || history[0].ToStatus != "confirmed" || history[1].ToStatus != "cancelled" {
		t.Errorf("booking history = %+v, want confirmed then cancelled", history)
	}

	assertBookingInventory(t, pool, roomTypeID, 0, 3)
	assertSearchReturnsHotel(t, client, searchURL, hotelID, true)
}

func TestAuthSessionHTTPLifecycleIntegration(t *testing.T) {
	pool := newAPIIntegrationPool(t)
	server := newBookingLifecycleServer(t, pool)
	defer server.Close()
	client := server.Client()

	registerBody := doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/register", "", "", map[string]any{
		"email":     "http-session@example.com",
		"password":  "correct horse battery staple",
		"full_name": "HTTP Session User",
	}, http.StatusCreated)
	var registered sqlc.RegisterUserRow
	decodeAPIResponse(t, registerBody, &registered)

	loginBody, loginCookies := doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/login", "", nil,
		map[string]any{
			"email":    "http-session@example.com",
			"password": "correct horse battery staple",
		},
		http.StatusOK,
	)
	var login handler.LoginResponse
	decodeAPIResponse(t, loginBody, &login)
	originalRefreshCookie := requireRefreshCookie(t, loginCookies)
	if !originalRefreshCookie.HttpOnly || originalRefreshCookie.Path != "/auth" || originalRefreshCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("refresh cookie security attributes = %+v", originalRefreshCookie)
	}

	doAPIRequest(t, client, http.MethodGet, server.URL+"/me", login.AccessToken, "", nil, http.StatusOK)
	doAPIRequest(t, client, http.MethodGet, server.URL+"/me", "not-a-valid-token", "", nil, http.StatusUnauthorized)

	refreshBody, rotatedCookies := doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/refresh",
		lifecycleOrigin, originalRefreshCookie, nil, http.StatusOK,
	)
	var refreshed handler.LoginResponse
	decodeAPIResponse(t, refreshBody, &refreshed)
	if refreshed.AccessToken == "" {
		t.Error("refresh access token is empty")
	}
	rotatedRefreshCookie := requireRefreshCookie(t, rotatedCookies)
	if rotatedRefreshCookie.Value == originalRefreshCookie.Value {
		t.Error("refresh token was not rotated")
	}
	doAPIRequest(t, client, http.MethodGet, server.URL+"/me", refreshed.AccessToken, "", nil, http.StatusOK)

	// Reusing the replaced token is treated as credential theft. The service
	// revokes the entire token family, including the freshly rotated token.
	doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/refresh",
		lifecycleOrigin, originalRefreshCookie, nil, http.StatusUnauthorized,
	)
	doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/refresh",
		lifecycleOrigin, rotatedRefreshCookie, nil, http.StatusUnauthorized,
	)
	assertNoActiveRefreshTokens(t, pool, registered.ID)

	// Start a fresh session to verify the ordinary logout path independently
	// from reuse detection.
	_, secondLoginCookies := doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/login", "", nil,
		map[string]any{
			"email":    "http-session@example.com",
			"password": "correct horse battery staple",
		},
		http.StatusOK,
	)
	secondRefreshCookie := requireRefreshCookie(t, secondLoginCookies)
	_, clearedCookies := doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/logout",
		lifecycleOrigin, secondRefreshCookie, nil, http.StatusNoContent,
	)
	clearedCookie := requireRefreshCookie(t, clearedCookies)
	if clearedCookie.MaxAge >= 0 {
		t.Errorf("cleared refresh cookie MaxAge = %d, want a negative value", clearedCookie.MaxAge)
	}
	doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/refresh",
		lifecycleOrigin, secondRefreshCookie, nil, http.StatusUnauthorized,
	)
	assertNoActiveRefreshTokens(t, pool, registered.ID)

	expiringTokenManager, err := auth.NewTokenManager(
		lifecycleJWTSecret,
		lifecycleJWTIssuer,
		lifecycleJWTAudience,
		time.Second,
	)
	if err != nil {
		t.Fatalf("create expiring token manager: %v", err)
	}
	expiredAccessToken, err := expiringTokenManager.GenerateToken(registered.ID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("generate expiring access token: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	doAPIRequest(t, client, http.MethodGet, server.URL+"/me", expiredAccessToken, "", nil, http.StatusUnauthorized)
}

func TestAdminBookingHTTPLifecycleIntegration(t *testing.T) {
	pool := newAPIIntegrationPool(t)
	server := newBookingLifecycleServer(t, pool)
	defer server.Close()
	client := server.Client()

	doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/register", "", "", map[string]any{
		"email":     "admin-flow-customer@example.com",
		"password":  "correct horse battery staple",
		"full_name": "Admin Flow Customer",
	}, http.StatusCreated)
	customerLoginBody := doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/login", "", "", map[string]any{
		"email":    "admin-flow-customer@example.com",
		"password": "correct horse battery staple",
	}, http.StatusOK)
	var customerLogin handler.LoginResponse
	decodeAPIResponse(t, customerLoginBody, &customerLogin)

	hotelID, roomTypeID := insertHTTPBookingFixtures(t, pool)
	checkIn := utcDateOnly(time.Now()).AddDate(1, 0, 0)
	checkOut := checkIn.AddDate(0, 0, 3)
	createdBody := doAPIRequest(
		t,
		client,
		http.MethodPost,
		server.URL+"/room-types/"+strconv.FormatInt(roomTypeID, 10)+"/bookings",
		customerLogin.AccessToken,
		"admin-lifecycle-booking-key",
		map[string]any{
			"check_in":    checkIn.Format(time.DateOnly),
			"check_out":   checkOut.Format(time.DateOnly),
			"rooms_count": 1,
			"guest_count": 2,
		},
		http.StatusCreated,
	)
	var created handler.BookingResponse
	decodeAPIResponse(t, createdBody, &created)
	assertBookingInventory(t, pool, roomTypeID, 1, 3)

	adminBookingsURL := server.URL + "/admin/bookings"
	doAPIRequest(
		t, client, http.MethodGet, adminBookingsURL,
		customerLogin.AccessToken, "", nil, http.StatusForbidden,
	)
	adminCancelURL := adminBookingsURL + "/" + strconv.FormatInt(created.ID, 10) + "/cancel"
	doAPIRequest(
		t, client, http.MethodPost, adminCancelURL,
		customerLogin.AccessToken, "", map[string]any{"reason": "not permitted"},
		http.StatusForbidden,
	)

	adminID := insertHTTPAdminUser(t, pool)
	adminLoginBody := doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/login", "", "", map[string]any{
		"email":    "booking-admin@example.com",
		"password": "admin correct horse battery staple",
	}, http.StatusOK)
	var adminLogin handler.LoginResponse
	decodeAPIResponse(t, adminLoginBody, &adminLogin)

	filteredListURL := fmt.Sprintf(
		"%s?hotel_id=%d&status=confirmed&check_in_from=%s&check_in_to=%s",
		adminBookingsURL,
		hotelID,
		checkIn.Format(time.DateOnly),
		checkOut.Format(time.DateOnly),
	)
	listBody := doAPIRequest(
		t, client, http.MethodGet, filteredListURL,
		adminLogin.AccessToken, "", nil, http.StatusOK,
	)
	var list handler.ListBookingsAdminResponse
	decodeAPIResponse(t, listBody, &list)
	if len(list.Bookings) != 1 || list.Bookings[0].BookingID != created.ID || list.Bookings[0].Status != "confirmed" {
		t.Errorf("admin booking list = %+v, want confirmed booking %d", list.Bookings, created.ID)
	}

	adminHistoryURL := adminBookingsURL + "/" + strconv.FormatInt(created.ID, 10) + "/history"
	historyBeforeBody := doAPIRequest(
		t, client, http.MethodGet, adminHistoryURL,
		adminLogin.AccessToken, "", nil, http.StatusOK,
	)
	var historyBefore []sqlc.BookingStatusHistory
	decodeAPIResponse(t, historyBeforeBody, &historyBefore)
	if len(historyBefore) != 1 || historyBefore[0].ToStatus != "confirmed" {
		t.Errorf("history before admin cancellation = %+v, want initial confirmed transition", historyBefore)
	}

	const cancellationReason = "Hotel closed for emergency maintenance"
	cancelledBody := doAPIRequest(
		t, client, http.MethodPost, adminCancelURL,
		adminLogin.AccessToken, "", map[string]any{"reason": cancellationReason},
		http.StatusOK,
	)
	var cancelled handler.BookingResponse
	decodeAPIResponse(t, cancelledBody, &cancelled)
	if cancelled.ID != created.ID || cancelled.Status != "cancelled" {
		t.Errorf("admin-cancelled booking = %+v, want cancelled booking %d", cancelled, created.ID)
	}
	assertBookingInventory(t, pool, roomTypeID, 0, 3)

	historyAfterBody := doAPIRequest(
		t, client, http.MethodGet, adminHistoryURL,
		adminLogin.AccessToken, "", nil, http.StatusOK,
	)
	var historyAfter []sqlc.BookingStatusHistory
	decodeAPIResponse(t, historyAfterBody, &historyAfter)
	if len(historyAfter) != 2 {
		t.Fatalf("history entry count = %d, want 2; history = %+v", len(historyAfter), historyAfter)
	}
	var cancellation sqlc.BookingStatusHistory
	for _, transition := range historyAfter {
		if transition.ToStatus == "cancelled" {
			cancellation = transition
			break
		}
	}
	if cancellation.ID == 0 {
		t.Fatalf("admin cancellation transition is missing from history: %+v", historyAfter)
	}
	if cancellation.FromStatus.String != "confirmed" || !cancellation.FromStatus.Valid || cancellation.ToStatus != "cancelled" {
		t.Errorf("admin cancellation transition = %+v, want confirmed to cancelled", cancellation)
	}
	if !cancellation.ChangedByUserID.Valid || cancellation.ChangedByUserID.Int64 != adminID {
		t.Errorf("admin cancellation actor = %+v, want admin %d", cancellation.ChangedByUserID, adminID)
	}
	if !cancellation.Reason.Valid || cancellation.Reason.String != cancellationReason {
		t.Errorf("admin cancellation reason = %+v, want %q", cancellation.Reason, cancellationReason)
	}

	doAPIRequest(
		t, client, http.MethodPost, adminCancelURL,
		adminLogin.AccessToken, "", map[string]any{"reason": "second cancellation"},
		http.StatusConflict,
	)
}

func TestPasswordResetHTTPLifecycleIntegration(t *testing.T) {
	pool := newAPIIntegrationPool(t)
	emailSender := &recordingOTPEmailSender{}
	server := newBookingLifecycleServer(t, pool, lifecycleServerOptions{
		emailSender: emailSender,
	})
	defer server.Close()
	client := server.Client()

	registerBody := doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/register", "", "", map[string]any{
		"email":     "password-reset@example.com",
		"password":  "original correct horse battery staple",
		"full_name": "Password Reset User",
	}, http.StatusCreated)
	var registered sqlc.RegisterUserRow
	decodeAPIResponse(t, registerBody, &registered)

	_, loginCookies := doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/login", "", nil,
		map[string]any{
			"email":    "password-reset@example.com",
			"password": "original correct horse battery staple",
		},
		http.StatusOK,
	)
	preResetRefreshCookie := requireRefreshCookie(t, loginCookies)

	unknownRequestBody := doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/request", "", "",
		map[string]any{"email": "unknown-password-reset@example.com"},
		http.StatusAccepted,
	)
	if emailSender.count() != 0 {
		t.Errorf("unknown-email reset sent %d emails, want 0", emailSender.count())
	}

	knownRequestBody := doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/request", "", "",
		map[string]any{"email": "password-reset@example.com"},
		http.StatusAccepted,
	)
	if string(knownRequestBody) != string(unknownRequestBody) {
		t.Errorf("known and unknown reset responses differ: known=%q unknown=%q", knownRequestBody, unknownRequestBody)
	}
	resetEmail := emailSender.requireLatestFor(t, "password-reset@example.com")
	wrongOTP := "000000"
	if resetEmail.OTP == wrongOTP {
		wrongOTP = "111111"
	}

	doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/verify", "", "",
		map[string]any{"email": "password-reset@example.com", "otp": wrongOTP},
		http.StatusBadRequest,
	)
	verifyBody := doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/verify", "", "",
		map[string]any{"email": "password-reset@example.com", "otp": resetEmail.OTP},
		http.StatusOK,
	)
	var verified handler.VerifyPasswordResetResponse
	decodeAPIResponse(t, verifyBody, &verified)
	if verified.ResetToken == "" || !verified.ExpiresAt.After(time.Now()) {
		t.Errorf("verified reset response = %+v, want non-empty unexpired token", verified)
	}

	// Verification consumes the OTP challenge, so even the correct code cannot
	// be exchanged for another reset token.
	doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/verify", "", "",
		map[string]any{"email": "password-reset@example.com", "otp": resetEmail.OTP},
		http.StatusBadRequest,
	)

	const newPassword = "replacement correct horse battery staple"
	doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/confirm", "", "",
		map[string]any{"reset_token": verified.ResetToken, "new_password": newPassword},
		http.StatusNoContent,
	)
	doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/confirm", "", "",
		map[string]any{"reset_token": verified.ResetToken, "new_password": newPassword},
		http.StatusBadRequest,
	)

	doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/login", "", "", map[string]any{
		"email":    "password-reset@example.com",
		"password": "original correct horse battery staple",
	}, http.StatusUnauthorized)
	doCookieAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/refresh",
		lifecycleOrigin, preResetRefreshCookie, nil, http.StatusUnauthorized,
	)
	assertNoActiveRefreshTokens(t, pool, registered.ID)
	doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/login", "", "", map[string]any{
		"email":    "password-reset@example.com",
		"password": newPassword,
	}, http.StatusOK)

	expiredOTPUser := registerPasswordResetTestUser(
		t, client, server.URL,
		"expired-otp@example.com", "Expired OTP User",
	)
	doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/request", "", "",
		map[string]any{"email": "expired-otp@example.com"},
		http.StatusAccepted,
	)
	expiredOTPEmail := emailSender.requireLatestFor(t, "expired-otp@example.com")
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE password_reset_requests
		 SET created_at = now() - interval '2 minutes',
		     expires_at = now() - interval '1 minute'
		 WHERE user_id = $1`,
		expiredOTPUser.ID,
	); err != nil {
		t.Fatalf("expire password reset OTP: %v", err)
	}
	doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/verify", "", "",
		map[string]any{"email": "expired-otp@example.com", "otp": expiredOTPEmail.OTP},
		http.StatusBadRequest,
	)

	expiredTokenUser := registerPasswordResetTestUser(
		t, client, server.URL,
		"expired-reset-token@example.com", "Expired Reset Token User",
	)
	doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/request", "", "",
		map[string]any{"email": "expired-reset-token@example.com"},
		http.StatusAccepted,
	)
	expiredTokenEmail := emailSender.requireLatestFor(t, "expired-reset-token@example.com")
	expiredTokenVerifyBody := doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/verify", "", "",
		map[string]any{"email": "expired-reset-token@example.com", "otp": expiredTokenEmail.OTP},
		http.StatusOK,
	)
	var expiredTokenResponse handler.VerifyPasswordResetResponse
	decodeAPIResponse(t, expiredTokenVerifyBody, &expiredTokenResponse)
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE password_reset_requests
		 SET created_at = now() - interval '4 minutes',
		     expires_at = now() - interval '2 minutes',
		     verified_at = now() - interval '3 minutes',
		     reset_token_expires_at = now() - interval '1 minute'
		 WHERE user_id = $1`,
		expiredTokenUser.ID,
	); err != nil {
		t.Fatalf("expire password reset token: %v", err)
	}
	doAPIRequest(
		t, client, http.MethodPost, server.URL+"/auth/password-reset/confirm", "", "",
		map[string]any{
			"reset_token":  expiredTokenResponse.ResetToken,
			"new_password": "another correct horse battery staple",
		},
		http.StatusBadRequest,
	)
}

func TestOAuthHTTPLifecycleIntegration(t *testing.T) {
	pool := newAPIIntegrationPool(t)
	googleProvider := &recordingGoogleOAuthProvider{
		identitiesByCode: map[string]appoauth.GoogleIdentity{
			"first-google-code": {
				Subject: "google-user-123", Email: "oauth-user@example.com", Name: "OAuth User",
			},
			"repeat-google-code": {
				Subject: "google-user-123", Email: "oauth-user@example.com", Name: "OAuth User",
			},
			"conflicting-google-code": {
				Subject: "different-google-user", Email: "oauth-user@example.com", Name: "OAuth User",
			},
			"expired-login-code": {
				Subject: "google-user-123", Email: "oauth-user@example.com", Name: "OAuth User",
			},
		},
	}
	server := newBookingLifecycleServer(t, pool, lifecycleServerOptions{
		oauthProvider: googleProvider,
	})
	defer server.Close()
	redirectClient := *server.Client()
	redirectClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	wrongStateFlow := startOAuthFlow(t, &redirectClient, server.URL, googleProvider)
	doOAuthGET(
		t,
		&redirectClient,
		server.URL+"/auth/google/callback?state=wrong-state&code=first-google-code",
		wrongStateFlow.cookies,
		http.StatusBadRequest,
	)

	tamperedFlow := startOAuthFlow(t, &redirectClient, server.URL, googleProvider)
	tamperedCookies := cloneHTTPCookies(tamperedFlow.cookies)
	tamperedCookies[0].Value += "tampered"
	doOAuthGET(
		t,
		&redirectClient,
		server.URL+"/auth/google/callback?state="+url.QueryEscape(tamperedFlow.authorization.state)+"&code=first-google-code",
		tamperedCookies,
		http.StatusBadRequest,
	)

	validFlow := startOAuthFlow(t, &redirectClient, server.URL, googleProvider)
	callback := doOAuthGET(
		t,
		&redirectClient,
		server.URL+"/auth/google/callback?state="+url.QueryEscape(validFlow.authorization.state)+"&code=first-google-code",
		validFlow.cookies,
		http.StatusFound,
	)
	if callback.header.Get("Cache-Control") != "no-store" {
		t.Errorf("OAuth callback Cache-Control = %q, want no-store", callback.header.Get("Cache-Control"))
	}
	if len(callback.cookies) != 3 {
		t.Errorf("cleared OAuth flow cookie count = %d, want 3", len(callback.cookies))
	} else {
		for _, cookie := range callback.cookies {
			if cookie.MaxAge >= 0 || cookie.Value != "" {
				t.Errorf("OAuth callback did not clear flow cookie: %+v", cookie)
			}
		}
	}
	loginCode := oauthLoginCodeFromRedirect(t, callback.header.Get("Location"))
	exchangeCall := googleProvider.latestExchange(t)
	if exchangeCall.code != "first-google-code" ||
		exchangeCall.verifier != validFlow.authorization.verifier ||
		exchangeCall.nonce != validFlow.authorization.nonce {
		t.Errorf("Google exchange call = %+v, want callback flow verifier and nonce", exchangeCall)
	}

	var oauthUserID int64
	var userCount, accountCount int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT id FROM users WHERE lower(email) = lower($1)",
		"oauth-user@example.com",
	).Scan(&oauthUserID); err != nil {
		t.Fatalf("load OAuth-created user: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM users WHERE lower(email) = lower($1)",
		"oauth-user@example.com",
	).Scan(&userCount); err != nil {
		t.Fatalf("count OAuth users: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM oauth_accounts WHERE user_id = $1 AND provider = 'google'",
		oauthUserID,
	).Scan(&accountCount); err != nil {
		t.Fatalf("count OAuth accounts: %v", err)
	}
	if userCount != 1 || accountCount != 1 {
		t.Errorf("OAuth records = {users:%d accounts:%d}, want {1 1}", userCount, accountCount)
	}

	exchangeBody, exchangeCookies := doCookieAPIRequest(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/auth/oauth/exchange",
		"",
		nil,
		map[string]any{"code": loginCode},
		http.StatusOK,
	)
	var sessionResponse handler.LoginResponse
	decodeAPIResponse(t, exchangeBody, &sessionResponse)
	if sessionResponse.AccessToken == "" {
		t.Fatal("OAuth exchange access token is empty")
	}
	requireRefreshCookie(t, exchangeCookies)
	doAPIRequest(
		t, server.Client(), http.MethodGet, server.URL+"/me",
		sessionResponse.AccessToken, "", nil, http.StatusOK,
	)
	doCookieAPIRequest(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/auth/oauth/exchange",
		"",
		nil,
		map[string]any{"code": loginCode},
		http.StatusUnauthorized,
	)

	repeatFlow := startOAuthFlow(t, &redirectClient, server.URL, googleProvider)
	doOAuthGET(
		t,
		&redirectClient,
		server.URL+"/auth/google/callback?state="+url.QueryEscape(repeatFlow.authorization.state)+"&code=repeat-google-code",
		repeatFlow.cookies,
		http.StatusFound,
	)
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM users WHERE lower(email) = lower($1)",
		"oauth-user@example.com",
	).Scan(&userCount); err != nil {
		t.Fatalf("recount OAuth users: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM oauth_accounts WHERE user_id = $1 AND provider = 'google'",
		oauthUserID,
	).Scan(&accountCount); err != nil {
		t.Fatalf("recount OAuth accounts: %v", err)
	}
	if userCount != 1 || accountCount != 1 {
		t.Errorf("repeated OAuth records = {users:%d accounts:%d}, want {1 1}", userCount, accountCount)
	}

	conflictFlow := startOAuthFlow(t, &redirectClient, server.URL, googleProvider)
	doOAuthGET(
		t,
		&redirectClient,
		server.URL+"/auth/google/callback?state="+url.QueryEscape(conflictFlow.authorization.state)+"&code=conflicting-google-code",
		conflictFlow.cookies,
		http.StatusConflict,
	)

	expiredFlow := startOAuthFlow(t, &redirectClient, server.URL, googleProvider)
	expiredCallback := doOAuthGET(
		t,
		&redirectClient,
		server.URL+"/auth/google/callback?state="+url.QueryEscape(expiredFlow.authorization.state)+"&code=expired-login-code",
		expiredFlow.cookies,
		http.StatusFound,
	)
	expiredLoginCode := oauthLoginCodeFromRedirect(t, expiredCallback.header.Get("Location"))
	expiredCodeHash := sha256.Sum256([]byte(expiredLoginCode))
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE oauth_login_codes
		 SET created_at = now() - interval '2 minutes',
		     expires_at = now() - interval '1 minute'
		 WHERE code_hash = $1`,
		expiredCodeHash[:],
	); err != nil {
		t.Fatalf("expire OAuth login code: %v", err)
	}
	doCookieAPIRequest(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/auth/oauth/exchange",
		"",
		nil,
		map[string]any{"code": expiredLoginCode},
		http.StatusUnauthorized,
	)
}

func TestReviewHTTPLifecycleIntegration(t *testing.T) {
	pool := newAPIIntegrationPool(t)
	server := newBookingLifecycleServer(t, pool)
	defer server.Close()
	client := server.Client()

	doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/register", "", "", map[string]any{
		"email":     "review-owner@example.com",
		"password":  "review owner correct horse battery staple",
		"full_name": "Review Owner",
	}, http.StatusCreated)
	ownerLoginBody := doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/login", "", "", map[string]any{
		"email":    "review-owner@example.com",
		"password": "review owner correct horse battery staple",
	}, http.StatusOK)
	var ownerLogin handler.LoginResponse
	decodeAPIResponse(t, ownerLoginBody, &ownerLogin)

	doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/register", "", "", map[string]any{
		"email":     "other-review-user@example.com",
		"password":  "other reviewer correct horse battery staple",
		"full_name": "Other Review User",
	}, http.StatusCreated)
	otherLoginBody := doAPIRequest(t, client, http.MethodPost, server.URL+"/auth/login", "", "", map[string]any{
		"email":    "other-review-user@example.com",
		"password": "other reviewer correct horse battery staple",
	}, http.StatusOK)
	var otherLogin handler.LoginResponse
	decodeAPIResponse(t, otherLoginBody, &otherLogin)

	hotelID, roomTypeID := insertHTTPBookingFixtures(t, pool)
	checkIn := utcDateOnly(time.Now()).AddDate(1, 0, 0)
	checkOut := checkIn.AddDate(0, 0, 3)
	createdBody := doAPIRequest(
		t,
		client,
		http.MethodPost,
		server.URL+"/room-types/"+strconv.FormatInt(roomTypeID, 10)+"/bookings",
		ownerLogin.AccessToken,
		"review-lifecycle-booking",
		map[string]any{
			"check_in":    checkIn.Format(time.DateOnly),
			"check_out":   checkOut.Format(time.DateOnly),
			"rooms_count": 1,
			"guest_count": 2,
		},
		http.StatusCreated,
	)
	var completedBooking handler.BookingResponse
	decodeAPIResponse(t, createdBody, &completedBooking)
	reviewCreateURL := server.URL + "/me/bookings/" + strconv.FormatInt(completedBooking.ID, 10) + "/review"

	// Confirmed bookings and bookings owned by someone else are not reviewable.
	doAPIRequest(
		t, client, http.MethodPost, reviewCreateURL,
		ownerLogin.AccessToken, "", map[string]any{"rating": 5, "comment": "Too early"},
		http.StatusConflict,
	)
	doAPIRequest(
		t, client, http.MethodPost, reviewCreateURL,
		otherLogin.AccessToken, "", map[string]any{"rating": 5, "comment": "Not my stay"},
		http.StatusConflict,
	)

	completionService := booking.NewService(pool, sqlc.New(pool))
	completedCount, err := completionService.CompletePastBookings(
		context.Background(),
		checkOut.AddDate(0, 0, 1),
	)
	if err != nil {
		t.Fatalf("complete booking for review lifecycle: %v", err)
	}
	if completedCount != 1 {
		t.Fatalf("completed booking count = %d, want 1", completedCount)
	}

	createdReviewBody := doAPIRequest(
		t, client, http.MethodPost, reviewCreateURL,
		ownerLogin.AccessToken, "", map[string]any{
			"rating":  5,
			"comment": "Wonderful stay",
		},
		http.StatusCreated,
	)
	var createdReview sqlc.Review
	decodeAPIResponse(t, createdReviewBody, &createdReview)
	if createdReview.ID <= 0 || createdReview.BookingID != completedBooking.ID || createdReview.Rating != 5 {
		t.Fatalf("created review = %+v, want five-star review for booking %d", createdReview, completedBooking.ID)
	}

	doAPIRequest(
		t, client, http.MethodPost, reviewCreateURL,
		ownerLogin.AccessToken, "", map[string]any{"rating": 4, "comment": "Duplicate"},
		http.StatusConflict,
	)

	publicReviewURL := server.URL + "/reviews/" + strconv.FormatInt(createdReview.ID, 10)
	publicBody := doAPIRequest(t, client, http.MethodGet, publicReviewURL, "", "", nil, http.StatusOK)
	var publicReview sqlc.GetReviewByIDRow
	decodeAPIResponse(t, publicBody, &publicReview)
	if publicReview.ID != createdReview.ID || publicReview.HotelID != hotelID || publicReview.ReviewerName != "Review Owner" {
		t.Errorf("public review = %+v, want review %d for hotel %d", publicReview, createdReview.ID, hotelID)
	}

	hotelReviewsURL := server.URL + "/hotels/" + strconv.FormatInt(hotelID, 10) + "/reviews"
	listBody := doAPIRequest(t, client, http.MethodGet, hotelReviewsURL, "", "", nil, http.StatusOK)
	var hotelReviews review.HotelReviewResult
	decodeAPIResponse(t, listBody, &hotelReviews)
	if len(hotelReviews.Reviews) != 1 || hotelReviews.Reviews[0].ID != createdReview.ID {
		t.Errorf("hotel reviews = %+v, want review %d", hotelReviews.Reviews, createdReview.ID)
	}

	ownedReviewURL := server.URL + "/me/reviews/" + strconv.FormatInt(createdReview.ID, 10)
	doAPIRequest(
		t, client, http.MethodPut, ownedReviewURL,
		otherLogin.AccessToken, "", map[string]any{"rating": 1, "comment": "Unauthorized edit"},
		http.StatusConflict,
	)
	doAPIRequest(
		t, client, http.MethodDelete, ownedReviewURL,
		otherLogin.AccessToken, "", nil, http.StatusNotFound,
	)

	updatedBody := doAPIRequest(
		t, client, http.MethodPut, ownedReviewURL,
		ownerLogin.AccessToken, "", map[string]any{"rating": 4, "comment": "Still a lovely stay"},
		http.StatusOK,
	)
	var updatedReview sqlc.Review
	decodeAPIResponse(t, updatedBody, &updatedReview)
	if updatedReview.Rating != 4 || !updatedReview.Comment.Valid || updatedReview.Comment.String != "Still a lovely stay" || !updatedReview.EditedAt.Valid {
		t.Errorf("updated review = %+v", updatedReview)
	}
	doAPIRequest(
		t, client, http.MethodPut, ownedReviewURL,
		ownerLogin.AccessToken, "", map[string]any{"rating": 3, "comment": "Second edit"},
		http.StatusConflict,
	)

	updatedPublicBody := doAPIRequest(t, client, http.MethodGet, publicReviewURL, "", "", nil, http.StatusOK)
	decodeAPIResponse(t, updatedPublicBody, &publicReview)
	if publicReview.Rating != 4 || !publicReview.Comment.Valid || publicReview.Comment.String != "Still a lovely stay" {
		t.Errorf("updated public review = %+v", publicReview)
	}

	// A cancelled booking remains ineligible even though it represents the
	// authenticated user's own reservation.
	cancelCheckIn := checkOut.AddDate(0, 0, 10)
	cancelCheckOut := cancelCheckIn.AddDate(0, 0, 2)
	cancelBookingBody := doAPIRequest(
		t,
		client,
		http.MethodPost,
		server.URL+"/room-types/"+strconv.FormatInt(roomTypeID, 10)+"/bookings",
		ownerLogin.AccessToken,
		"cancelled-review-booking",
		map[string]any{
			"check_in":    cancelCheckIn.Format(time.DateOnly),
			"check_out":   cancelCheckOut.Format(time.DateOnly),
			"rooms_count": 1,
			"guest_count": 1,
		},
		http.StatusCreated,
	)
	var cancelledBooking handler.BookingResponse
	decodeAPIResponse(t, cancelBookingBody, &cancelledBooking)
	doAPIRequest(
		t,
		client,
		http.MethodPost,
		server.URL+"/me/bookings/"+strconv.FormatInt(cancelledBooking.ID, 10)+"/cancel",
		ownerLogin.AccessToken,
		"",
		nil,
		http.StatusOK,
	)
	doAPIRequest(
		t,
		client,
		http.MethodPost,
		server.URL+"/me/bookings/"+strconv.FormatInt(cancelledBooking.ID, 10)+"/review",
		ownerLogin.AccessToken,
		"",
		map[string]any{"rating": 3, "comment": "Cancelled stay"},
		http.StatusConflict,
	)

	doAPIRequest(
		t, client, http.MethodDelete, ownedReviewURL,
		ownerLogin.AccessToken, "", nil, http.StatusNoContent,
	)
	doAPIRequest(t, client, http.MethodGet, publicReviewURL, "", "", nil, http.StatusNotFound)
	emptyListBody := doAPIRequest(t, client, http.MethodGet, hotelReviewsURL, "", "", nil, http.StatusOK)
	decodeAPIResponse(t, emptyListBody, &hotelReviews)
	if len(hotelReviews.Reviews) != 0 {
		t.Errorf("hotel reviews after deletion = %+v, want empty", hotelReviews.Reviews)
	}
}

type recordingOTPEmailSender struct {
	mu     sync.Mutex
	emails []passwordreset.OTPEmail
}

type oauthFlowStart struct {
	authorization oauthAuthorizationCall
	cookies       []*http.Cookie
}

type oauthHTTPResponse struct {
	header  http.Header
	body    []byte
	cookies []*http.Cookie
}

func startOAuthFlow(
	t *testing.T,
	client *http.Client,
	serverURL string,
	provider *recordingGoogleOAuthProvider,
) oauthFlowStart {
	t.Helper()
	response := doOAuthGET(
		t,
		client,
		serverURL+"/auth/google",
		nil,
		http.StatusFound,
	)
	authorization := provider.latestAuthorization(t)
	location, err := url.Parse(response.header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Google authorization redirect: %v", err)
	}
	if location.Scheme != "https" || location.Host != "accounts.google.test" || location.Query().Get("state") != authorization.state {
		t.Errorf("Google authorization redirect = %q, want fake provider URL with matching state", location.String())
	}
	if len(response.cookies) != 3 {
		t.Fatalf("OAuth flow cookie count = %d, want 3", len(response.cookies))
	}
	for _, cookie := range response.cookies {
		if !cookie.HttpOnly || cookie.Path != "/auth/google" || cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("OAuth flow cookie attributes = %+v", cookie)
		}
	}
	return oauthFlowStart{authorization: authorization, cookies: response.cookies}
}

func doOAuthGET(
	t *testing.T,
	client *http.Client,
	requestURL string,
	cookies []*http.Cookie,
	wantStatus int,
) oauthHTTPResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, requestURL, nil)
	if err != nil {
		t.Fatalf("create OAuth GET %s: %v", requestURL, err)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send OAuth GET %s: %v", requestURL, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read OAuth GET %s response: %v", requestURL, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d; body = %q", requestURL, response.StatusCode, wantStatus, body)
	}
	return oauthHTTPResponse{
		header: response.Header.Clone(), body: body, cookies: response.Cookies(),
	}
}

func cloneHTTPCookies(cookies []*http.Cookie) []*http.Cookie {
	cloned := make([]*http.Cookie, len(cookies))
	for index, cookie := range cookies {
		cookieCopy := *cookie
		cloned[index] = &cookieCopy
	}
	return cloned
}

func oauthLoginCodeFromRedirect(t *testing.T, rawLocation string) string {
	t.Helper()
	location, err := url.Parse(rawLocation)
	if err != nil {
		t.Fatalf("parse OAuth success redirect: %v", err)
	}
	if location.Scheme != "http" || location.Host != "localhost:3000" || location.Path != "/auth/callback" {
		t.Fatalf("OAuth success redirect = %q, want frontend callback URL", rawLocation)
	}
	code := location.Query().Get("code")
	if code == "" {
		t.Fatalf("OAuth success redirect has no login code: %q", rawLocation)
	}
	return code
}

type oauthAuthorizationCall struct {
	state    string
	verifier string
	nonce    string
}

type oauthExchangeCall struct {
	code     string
	verifier string
	nonce    string
}

type recordingGoogleOAuthProvider struct {
	mu                 sync.Mutex
	identitiesByCode   map[string]appoauth.GoogleIdentity
	authorizationCalls []oauthAuthorizationCall
	exchangeCalls      []oauthExchangeCall
}

func (p *recordingGoogleOAuthProvider) AuthorizationURL(
	state string,
	verifier string,
	nonce string,
) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authorizationCalls = append(p.authorizationCalls, oauthAuthorizationCall{
		state: state, verifier: verifier, nonce: nonce,
	})
	values := url.Values{"state": []string{state}}
	return "https://accounts.google.test/authorize?" + values.Encode(), nil
}

func (p *recordingGoogleOAuthProvider) ExchangeIdentity(
	_ context.Context,
	code string,
	verifier string,
	nonce string,
) (appoauth.GoogleIdentity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exchangeCalls = append(p.exchangeCalls, oauthExchangeCall{
		code: code, verifier: verifier, nonce: nonce,
	})
	identity, ok := p.identitiesByCode[code]
	if !ok {
		return appoauth.GoogleIdentity{}, errors.New("unknown fake Google authorization code")
	}
	return identity, nil
}

func (p *recordingGoogleOAuthProvider) latestAuthorization(t *testing.T) oauthAuthorizationCall {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.authorizationCalls) == 0 {
		t.Fatal("Google provider received no authorization request")
	}
	return p.authorizationCalls[len(p.authorizationCalls)-1]
}

func (p *recordingGoogleOAuthProvider) latestExchange(t *testing.T) oauthExchangeCall {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.exchangeCalls) == 0 {
		t.Fatal("Google provider received no code exchange")
	}
	return p.exchangeCalls[len(p.exchangeCalls)-1]
}

type lifecycleServerOptions struct {
	emailSender   *recordingOTPEmailSender
	oauthProvider *recordingGoogleOAuthProvider
}

func (s *recordingOTPEmailSender) SendPasswordResetOTP(
	_ context.Context,
	email passwordreset.OTPEmail,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emails = append(s.emails, email)
	return nil
}

func (s *recordingOTPEmailSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.emails)
}

func (s *recordingOTPEmailSender) requireLatestFor(
	t *testing.T,
	emailAddress string,
) passwordreset.OTPEmail {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := len(s.emails) - 1; index >= 0; index-- {
		if s.emails[index].To == emailAddress {
			return s.emails[index]
		}
	}
	t.Fatalf("no password reset email was sent to %q", emailAddress)
	return passwordreset.OTPEmail{}
}

func newBookingLifecycleServer(
	t *testing.T,
	pool *pgxpool.Pool,
	options ...lifecycleServerOptions,
) *httptest.Server {
	t.Helper()

	queries := sqlc.New(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	otpEmailSender := &recordingOTPEmailSender{}
	googleProvider := &recordingGoogleOAuthProvider{}
	if len(options) > 0 {
		if options[0].emailSender != nil {
			otpEmailSender = options[0].emailSender
		}
		if options[0].oauthProvider != nil {
			googleProvider = options[0].oauthProvider
		}
	}
	tokenManager, err := auth.NewTokenManager(
		lifecycleJWTSecret,
		lifecycleJWTIssuer,
		lifecycleJWTAudience,
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create test token manager: %v", err)
	}
	sessionService, err := session.NewService(pool, queries, tokenManager, 24*time.Hour)
	if err != nil {
		t.Fatalf("create test session service: %v", err)
	}
	oauthFlowCookies, err := appoauth.NewFlowCookieManager(
		lifecycleOAuthSecret,
		false,
		10*time.Minute,
	)
	if err != nil {
		t.Fatalf("create test OAuth flow cookies: %v", err)
	}
	oauthService, err := appoauth.NewService(pool, queries, sessionService, 2*time.Minute)
	if err != nil {
		t.Fatalf("create test OAuth service: %v", err)
	}
	oauthHandler, err := handler.NewOAuthHandler(
		googleProvider,
		oauthFlowCookies,
		oauthService,
		session.NewRefreshCookieManager(false),
		lifecycleOrigin+"/auth/callback",
	)
	if err != nil {
		t.Fatalf("create test OAuth handler: %v", err)
	}
	passwordResetService, err := passwordreset.NewService(
		pool,
		queries,
		otpEmailSender,
		lifecycleResetPepper,
		10*time.Minute,
		15*time.Minute,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("create test password reset service: %v", err)
	}
	corsMiddleware, err := middleware.NewCORSMiddleware(lifecycleOrigin)
	if err != nil {
		t.Fatalf("create test CORS middleware: %v", err)
	}
	rateLimiter, err := middleware.NewIPRateLimiter(
		map[string]middleware.RateLimitPolicy{
			"register":               {Requests: 100, Window: time.Minute},
			"login":                  {Requests: 100, Window: time.Minute},
			"password-reset-request": {Requests: 100, Window: time.Minute},
			"password-reset-verify":  {Requests: 100, Window: time.Minute},
			"password-reset-confirm": {Requests: 100, Window: time.Minute},
			"oauth-exchange":         {Requests: 100, Window: time.Minute},
			"refresh":                {Requests: 100, Window: time.Minute},
		},
		time.Minute,
	)
	if err != nil {
		t.Fatalf("create test rate limiter: %v", err)
	}

	bookingService := booking.NewService(pool, queries)
	adminBookingService := adminbooking.NewService(queries)
	reviewService := review.NewService(queries)
	diagnosticBookingService := &bookingLifecycleDiagnosticService{
		Service: bookingService,
		t:       t,
	}
	apiHandler := newAPIHandler(apiDependencies{
		healthHandler:   handler.NewHealthHandler(pool, time.Second, logger),
		hotelHandler:    new(handler.HotelHandler),
		roomTypeHandler: new(handler.RoomTypeHandler),
		amenityHandler:  new(handler.AmenityHandler),
		userHandler:     handler.NewUserHandler(queries),
		authHandler:     handler.NewAuthHandler(queries, sessionService, session.NewRefreshCookieManager(false)),
		oauthHandler:    oauthHandler,
		passwordResetHandler: handler.NewPasswordResetHandler(
			passwordResetService,
			log.New(io.Discard, "", 0),
		),
		bookingHandler:      handler.NewBookingHandler(diagnosticBookingService),
		adminBookingHandler: handler.NewAdminBookingHandler(adminBookingService, diagnosticBookingService),
		reviewHandler:       handler.NewReviewHandler(reviewService),
		hotelImageHandler:   new(handler.HotelImageHandler),
		inventoryHandler:    new(handler.InventoryHandler),
		authMiddleware:      middleware.NewAuthMiddleware(tokenManager),
		corsMiddleware:      corsMiddleware,
		authRateLimiter:     rateLimiter,
		logger:              logger,
	})
	return httptest.NewServer(apiHandler)
}

// bookingLifecycleDiagnosticService preserves the real service behavior while
// making otherwise-sanitized handler errors visible in verbose integration-test
// output. Internal database errors must never be returned to API clients.
type bookingLifecycleDiagnosticService struct {
	*booking.Service
	t *testing.T
}

func (s *bookingLifecycleDiagnosticService) CreateBooking(
	ctx context.Context,
	input booking.CreateInput,
) (sqlc.Booking, error) {
	created, err := s.Service.CreateBooking(ctx, input)
	if err != nil {
		s.t.Logf("CreateBooking service error: %+v", err)
	}
	return created, err
}

func (s *bookingLifecycleDiagnosticService) CancelBookingAsAdmin(
	ctx context.Context,
	input booking.AdminCancellationInput,
) (sqlc.Booking, error) {
	cancelled, err := s.Service.CancelBookingAsAdmin(ctx, input)
	if err != nil &&
		!errors.Is(err, booking.ErrInvalidCancellationReason) &&
		!errors.Is(err, booking.ErrBookingNotFound) &&
		!errors.Is(err, booking.ErrBookingNotCancellable) {
		s.t.Logf("CancelBookingAsAdmin service error: %+v", err)
	}
	return cancelled, err
}

func newAPIIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL, explicitlyConfigured := os.LookupEnv("TEST_DATABASE_URL")
	if !explicitlyConfigured {
		databaseURL = "postgres://test:test@localhost:5433/testdb?sslmode=disable"
	}
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run HTTP lifecycle integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create integration admin pool: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		if !explicitlyConfigured {
			t.Skipf("local PostgreSQL test database is unavailable: %v", err)
		}
		t.Fatalf("ping integration database: %v", err)
	}

	schemaName := randomAPIIntegrationSchema(t)
	schemaIdentifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		adminPool.Close()
		t.Fatalf("create integration schema: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		adminPool.Close()
		t.Fatalf("parse integration database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		_, _ = adminPool.Exec(context.Background(), "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		adminPool.Close()
		t.Fatalf("create isolated integration pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
		adminPool.Close()
	})

	applyAPIIntegrationMigrations(t, pool)
	return pool
}

func applyAPIIntegrationMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("find migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no up migrations found")
	}
	sort.Strings(paths)
	for _, path := range paths {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		if _, err := pool.Exec(context.Background(), string(migration)); err != nil {
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}
}

func insertHTTPBookingFixtures(t *testing.T, pool *pgxpool.Pool) (int64, int64) {
	t.Helper()

	ctx := context.Background()
	var hotelID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO hotels (name, address, city)
		 VALUES ('HTTP Test Hotel', '1 Test Street', 'Test City')
		 RETURNING id`,
	).Scan(&hotelID); err != nil {
		t.Fatalf("insert HTTP test hotel: %v", err)
	}

	var roomTypeID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO room_types (hotel_id, name, price_per_night, capacity, total_rooms)
		 VALUES ($1, 'Only Room', 100.00, 2, 1)
		 RETURNING id`,
		hotelID,
	).Scan(&roomTypeID); err != nil {
		t.Fatalf("insert HTTP test room type: %v", err)
	}
	return hotelID, roomTypeID
}

func insertHTTPAdminUser(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	passwordHash, err := auth.HashPassword("admin correct horse battery staple")
	if err != nil {
		t.Fatalf("hash HTTP admin password: %v", err)
	}

	var adminID int64
	if err := pool.QueryRow(
		context.Background(),
		`INSERT INTO users (email, full_name, password_hash, role)
		 VALUES ('booking-admin@example.com', 'Booking Administrator', $1, 'admin')
		 RETURNING id`,
		passwordHash,
	).Scan(&adminID); err != nil {
		t.Fatalf("insert HTTP admin user: %v", err)
	}
	return adminID
}

func registerPasswordResetTestUser(
	t *testing.T,
	client *http.Client,
	serverURL string,
	email string,
	fullName string,
) sqlc.RegisterUserRow {
	t.Helper()
	body := doAPIRequest(
		t,
		client,
		http.MethodPost,
		serverURL+"/auth/register",
		"",
		"",
		map[string]any{
			"email":     email,
			"password":  "temporary correct horse battery staple",
			"full_name": fullName,
		},
		http.StatusCreated,
	)
	var registered sqlc.RegisterUserRow
	decodeAPIResponse(t, body, &registered)
	return registered
}

func doAPIRequest(
	t *testing.T,
	client *http.Client,
	method string,
	url string,
	accessToken string,
	idempotencyKey string,
	payload any,
	wantStatus int,
) []byte {
	t.Helper()

	var requestBody io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode %s %s request: %v", method, url, err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, url, requestBody)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, url, err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send %s %s request: %v", method, url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, url, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body = %q", method, url, response.StatusCode, wantStatus, body)
	}
	return body
}

func doCookieAPIRequest(
	t *testing.T,
	client *http.Client,
	method string,
	url string,
	origin string,
	cookie *http.Cookie,
	payload any,
	wantStatus int,
) ([]byte, []*http.Cookie) {
	t.Helper()

	var requestBody io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode %s %s request: %v", method, url, err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, url, requestBody)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, url, err)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send %s %s request: %v", method, url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, url, err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body = %q", method, url, response.StatusCode, wantStatus, body)
	}
	return body, response.Cookies()
}

func requireRefreshCookie(t *testing.T, cookies []*http.Cookie) *http.Cookie {
	t.Helper()
	if len(cookies) != 1 {
		t.Fatalf("response cookie count = %d, want 1", len(cookies))
	}
	if cookies[0].Value == "" && cookies[0].MaxAge >= 0 {
		t.Fatal("response refresh cookie value is empty")
	}
	return cookies[0]
}

func decodeAPIResponse(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode API response %q: %v", body, err)
	}
}

func assertSearchReturnsHotel(t *testing.T, client *http.Client, url string, hotelID int64, want bool) {
	t.Helper()
	body := doAPIRequest(t, client, http.MethodGet, url, "", "", nil, http.StatusOK)
	var result booking.HotelSearchResult
	decodeAPIResponse(t, body, &result)
	found := false
	for _, hotel := range result.Hotels {
		if hotel.ID == hotelID {
			found = true
			break
		}
	}
	if found != want {
		t.Errorf("search contains hotel %d = %t, want %t; hotels = %+v", hotelID, found, want, result.Hotels)
	}
}

func assertBookingInventory(t *testing.T, pool *pgxpool.Pool, roomTypeID int64, wantBooked int32, wantRows int64) {
	t.Helper()
	var rows int64
	var minimum int32
	var maximum int32
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), min(rooms_booked), max(rooms_booked)
		 FROM room_type_availability
		 WHERE room_type_id = $1`,
		roomTypeID,
	).Scan(&rows, &minimum, &maximum); err != nil {
		t.Fatalf("query HTTP booking inventory: %v", err)
	}
	if rows != wantRows || minimum != wantBooked || maximum != wantBooked {
		t.Errorf("inventory = {rows:%d min:%d max:%d}, want {rows:%d min:%d max:%d}", rows, minimum, maximum, wantRows, wantBooked, wantBooked)
	}
}

func assertSingleBooking(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var count int64
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM bookings").Scan(&count); err != nil {
		t.Fatalf("count HTTP lifecycle bookings: %v", err)
	}
	if count != 1 {
		t.Errorf("booking count = %d, want 1", count)
	}
}

func assertNoActiveRefreshTokens(t *testing.T, pool *pgxpool.Pool, userID int64) {
	t.Helper()
	var count int64
	if err := pool.QueryRow(
		context.Background(),
		`SELECT count(*)
		 FROM refresh_tokens
		 WHERE user_id = $1
		   AND revoked_at IS NULL
		   AND expires_at > now()`,
		userID,
	).Scan(&count); err != nil {
		t.Fatalf("count active refresh tokens: %v", err)
	}
	if count != 0 {
		t.Errorf("active refresh token count = %d, want 0", count)
	}
}

func randomAPIIntegrationSchema(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate integration schema name: %v", err)
	}
	return "ryoko_api_test_" + hex.EncodeToString(randomBytes[:])
}

func utcDateOnly(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

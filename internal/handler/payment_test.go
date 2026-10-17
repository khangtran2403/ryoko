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

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
	"github.com/khangtran2403/ryoko/internal/payments"
)

type fakePaymentService struct {
	result       payments.InitiatePaymentResult
	err          error
	statusResult sqlc.Payment
	statusErr    error

	called    bool
	userID    int64
	bookingID int64
}

func (f *fakePaymentService) GetPaymentStatus(
	_ context.Context,
	userID int64,
	bookingID int64,
) (sqlc.Payment, error) {
	f.called = true
	f.userID = userID
	f.bookingID = bookingID
	return f.statusResult, f.statusErr
}

func (f *fakePaymentService) InitiatePayment(
	_ context.Context,
	userID int64,
	bookingID int64,
) (payments.InitiatePaymentResult, error) {
	f.called = true
	f.userID = userID
	f.bookingID = bookingID
	return f.result, f.err
}

func TestPaymentHandlerInitiatePaymentReturnsCheckoutURL(t *testing.T) {
	service := &fakePaymentService{
		result: paymentHandlerResult("https://pay.example.com/session/123", ""),
	}
	mux, token := newAuthenticatedPaymentMux(t, service, 42)

	response := performPaymentRequest(mux, token, "/me/bookings/88/payment-session")

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusCreated, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if !service.called || service.userID != 42 || service.bookingID != 88 {
		t.Errorf(
			"service call = {called:%t user:%d booking:%d}, want {true 42 88}",
			service.called,
			service.userID,
			service.bookingID,
		)
	}

	var body PaymentSessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PaymentID != 7 || body.ProviderPaymentID != "provider-payment-7" {
		t.Errorf("payment identity = %+v, want payment 7/provider-payment-7", body)
	}
	if body.CheckoutURL != "https://pay.example.com/session/123" || body.ClientSecret != "" {
		t.Errorf("checkout action = %+v, want checkout URL only", body)
	}
	if body.AmountMinor != 12500 || body.Currency != "USD" {
		t.Errorf("amount = %d %s, want 12500 USD", body.AmountMinor, body.Currency)
	}
	if strings.Contains(response.Body.String(), "client_secret") {
		t.Errorf("response contains empty client_secret: %s", response.Body.String())
	}
}

func TestPaymentHandlerGetPaymentStatus(t *testing.T) {
	paidAt := time.Date(2026, time.October, 17, 4, 5, 6, 0, time.FixedZone("test", 7*60*60))
	service := &fakePaymentService{statusResult: sqlc.Payment{
		ID:                7,
		BookingID:         88,
		Provider:          "fake",
		ProviderPaymentID: pgtype.Text{String: "secret-provider-id", Valid: true},
		AmountMinor:       250000,
		Currency:          "VND",
		Status:            "succeeded",
		FailureMessage:    pgtype.Text{String: "internal provider detail", Valid: true},
		PaidAt:            pgtype.Timestamptz{Time: paidAt, Valid: true},
	}}
	mux, token := newAuthenticatedPaymentMux(t, service, 42)
	response := performPaymentMethodRequest(mux, http.MethodGet, token, "/me/bookings/88/payment")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %q", response.Code, response.Body.String())
	}
	if !service.called || service.userID != 42 || service.bookingID != 88 {
		t.Errorf("service call = {called:%t user:%d booking:%d}", service.called, service.userID, service.bookingID)
	}
	var body PaymentStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PaymentID != 7 || body.BookingID != 88 || body.AmountMinor != 250000 || body.Currency != "VND" || body.Status != "succeeded" {
		t.Errorf("response = %+v", body)
	}
	if body.PaidAt == nil || !body.PaidAt.Equal(paidAt) || body.PaidAt.Location() != time.UTC || body.RefundedAt != nil {
		t.Errorf("payment timestamps = paid %v refunded %v", body.PaidAt, body.RefundedAt)
	}
	if body.Retryable || body.FailureCode != nil {
		t.Errorf("successful payment retry state = {retryable:%t code:%v}, want false/nil", body.Retryable, body.FailureCode)
	}
	for _, privateField := range []string{"provider_payment_id", "secret-provider-id", "failure_message", "internal provider detail"} {
		if strings.Contains(response.Body.String(), privateField) {
			t.Errorf("response exposes private field %q: %s", privateField, response.Body.String())
		}
	}
}

func TestPaymentHandlerGetPaymentStatusSanitizesRetryableFailure(t *testing.T) {
	service := &fakePaymentService{statusResult: sqlc.Payment{
		ID:             7,
		BookingID:      88,
		AmountMinor:    250000,
		Currency:       "VND",
		Status:         "pending",
		FailureCode:    pgtype.Text{String: "card_declined_provider_code", Valid: true},
		FailureMessage: pgtype.Text{String: "sensitive provider diagnostic", Valid: true},
	}}
	mux, token := newAuthenticatedPaymentMux(t, service, 42)
	response := performPaymentMethodRequest(mux, http.MethodGet, token, "/me/bookings/88/payment")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body PaymentStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Retryable || body.FailureCode == nil || *body.FailureCode != "payment_failed" {
		t.Errorf("retry state = {retryable:%t code:%v}, want true/payment_failed", body.Retryable, body.FailureCode)
	}
	for _, privateValue := range []string{"card_declined_provider_code", "sensitive provider diagnostic"} {
		if strings.Contains(response.Body.String(), privateValue) {
			t.Errorf("response exposes provider value %q: %s", privateValue, response.Body.String())
		}
	}
}

func TestPaymentHandlerGetPaymentStatusMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		bookingID  string
		err        error
		wantStatus int
	}{
		{name: "invalid path", bookingID: "abc", wantStatus: http.StatusBadRequest},
		{name: "not found or not owned", bookingID: "88", err: payments.ErrPaymentNotFound, wantStatus: http.StatusNotFound},
		{name: "invalid user", bookingID: "88", err: payments.ErrInvalidUserID, wantStatus: http.StatusUnauthorized},
		{name: "database failure", bookingID: "88", err: errors.New("database failed"), wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePaymentService{statusErr: tt.err}
			mux, token := newAuthenticatedPaymentMux(t, service, 42)
			response := performPaymentMethodRequest(mux, http.MethodGet, token, "/me/bookings/"+tt.bookingID+"/payment")
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, tt.wantStatus, response.Body.String())
			}
			if tt.bookingID == "abc" && service.called {
				t.Fatal("service called for invalid booking ID")
			}
		})
	}
}

func TestPaymentHandlerInitiatePaymentReturnsClientSecret(t *testing.T) {
	service := &fakePaymentService{
		result: paymentHandlerResult("", "secret_123"),
	}
	mux, token := newAuthenticatedPaymentMux(t, service, 42)

	response := performPaymentRequest(mux, token, "/me/bookings/88/payment-session")

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusCreated, response.Body.String())
	}
	var body PaymentSessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ClientSecret != "secret_123" || body.CheckoutURL != "" {
		t.Errorf("checkout action = %+v, want client secret only", body)
	}
	if strings.Contains(response.Body.String(), "checkout_url") {
		t.Errorf("response contains empty checkout_url: %s", response.Body.String())
	}
}

func TestPaymentHandlerInitiatePaymentRequiresAuthentication(t *testing.T) {
	service := &fakePaymentService{}
	handler := NewPaymentHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/me/bookings/88/payment-session", nil)
	request.SetPathValue("bookingID", "88")
	response := httptest.NewRecorder()

	handler.InitiatePayment(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if response.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", response.Header().Get("WWW-Authenticate"))
	}
	if service.called {
		t.Fatal("payment service was called without an authenticated principal")
	}
}

func TestPaymentHandlerInitiatePaymentRejectsInvalidBookingID(t *testing.T) {
	tests := []string{"missing", "abc", "0", "-1"}
	for _, bookingID := range tests {
		t.Run(bookingID, func(t *testing.T) {
			service := &fakePaymentService{}
			mux, token := newAuthenticatedPaymentMux(t, service, 42)
			path := "/me/bookings/" + bookingID + "/payment-session"

			response := performPaymentRequest(mux, token, path)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if service.called {
				t.Fatal("payment service was called for an invalid booking ID")
			}
		})
	}
}

func TestPaymentHandlerInitiatePaymentMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{name: "invalid booking", err: payments.ErrInvalidBookingID, wantStatus: http.StatusBadRequest, wantBody: "booking ID must be positive"},
		{name: "invalid user", err: payments.ErrInvalidUserID, wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "not available", err: payments.ErrPaymentNotAvailable, wantStatus: http.StatusNotFound, wantBody: "Payment not available"},
		{name: "gateway unavailable", err: payments.ErrGatewayUnavailable, wantStatus: http.StatusServiceUnavailable, wantBody: "Payment service unavailable"},
		{name: "provider mismatch", err: payments.ErrProviderMismatch, wantStatus: http.StatusBadGateway, wantBody: "Payment provider error"},
		{name: "missing provider ID", err: payments.ErrProviderPaymentIDRequired, wantStatus: http.StatusBadGateway, wantBody: "Payment provider error"},
		{name: "provider ID mismatch", err: payments.ErrProviderPaymentIDMismatch, wantStatus: http.StatusBadGateway, wantBody: "Payment provider error"},
		{name: "invalid checkout action", err: payments.ErrCheckoutURLOrClientSecretRequired, wantStatus: http.StatusBadGateway, wantBody: "Payment provider error"},
		{name: "unsupported provider currency", err: payments.ErrInvalidCurrency, wantStatus: http.StatusBadGateway, wantBody: "Payment provider error"},
		{name: "invalid gateway request", err: payments.ErrInvalidRequest, wantStatus: http.StatusBadGateway, wantBody: "Payment provider error"},
		{name: "unexpected", err: errors.New("database failed"), wantStatus: http.StatusInternalServerError, wantBody: "Failed to initiate payment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePaymentService{err: tt.err}
			mux, token := newAuthenticatedPaymentMux(t, service, 42)

			response := performPaymentRequest(mux, token, "/me/bookings/88/payment-session")

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, tt.wantStatus, response.Body.String())
			}
			if strings.TrimSpace(response.Body.String()) != tt.wantBody {
				t.Errorf("body = %q, want %q", strings.TrimSpace(response.Body.String()), tt.wantBody)
			}
		})
	}
}

func paymentHandlerResult(checkoutURL string, clientSecret string) payments.InitiatePaymentResult {
	return payments.InitiatePaymentResult{
		Payment: sqlc.Payment{
			ID:          7,
			BookingID:   88,
			AmountMinor: 12500,
			Currency:    "USD",
		},
		Session: payments.PaymentSession{
			ProviderPaymentID: "provider-payment-7",
			CheckoutURL:       checkoutURL,
			ClientSecret:      clientSecret,
		},
	}
}

func newAuthenticatedPaymentMux(
	t *testing.T,
	service paymentService,
	userID int64,
) (*http.ServeMux, string) {
	t.Helper()

	tokenManager, err := auth.NewTokenManager(
		"payment-handler-test-secret-at-least-32-bytes",
		"ryoko-payment-handler-test",
		"ryoko-payment-handler-test-api",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}
	token, err := tokenManager.GenerateToken(userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	paymentHandler := NewPaymentHandler(service)
	authMiddleware := middleware.NewAuthMiddleware(tokenManager)
	mux := http.NewServeMux()
	mux.Handle(
		"POST /me/bookings/{bookingID}/payment-session",
		authMiddleware.Authenticate(http.HandlerFunc(paymentHandler.InitiatePayment)),
	)
	mux.Handle(
		"GET /me/bookings/{bookingID}/payment",
		authMiddleware.Authenticate(http.HandlerFunc(paymentHandler.GetPaymentStatus)),
	)
	return mux, token
}

func performPaymentRequest(handler http.Handler, token string, path string) *httptest.ResponseRecorder {
	return performPaymentMethodRequest(handler, http.MethodPost, token, path)
}

func performPaymentMethodRequest(handler http.Handler, method string, token string, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
	"github.com/khangtran2403/ryoko/internal/payments"
)

type paymentService interface {
	InitiatePayment(ctx context.Context, userID int64, bookingID int64) (payments.InitiatePaymentResult, error)
	GetPaymentStatus(ctx context.Context, userID int64, bookingID int64) (sqlc.Payment, error)
}

type PaymentHandler struct {
	service paymentService
}

func NewPaymentHandler(service paymentService) *PaymentHandler {
	return &PaymentHandler{service: service}
}

type PaymentSessionResponse struct {
	PaymentID         int64  `json:"payment_id"`
	ProviderPaymentID string `json:"provider_payment_id"`
	CheckoutURL       string `json:"checkout_url,omitempty"`
	ClientSecret      string `json:"client_secret,omitempty"`
	AmountMinor       int64  `json:"amount_minor"`
	Currency          string `json:"currency"`
}

type PaymentStatusResponse struct {
	PaymentID   int64      `json:"payment_id"`
	BookingID   int64      `json:"booking_id"`
	AmountMinor int64      `json:"amount_minor"`
	Currency    string     `json:"currency"`
	Status      string     `json:"status"`
	PaidAt      *time.Time `json:"paid_at"`
	RefundedAt  *time.Time `json:"refunded_at"`
	Retryable   bool       `json:"retryable"`
	FailureCode *string    `json:"failure_code"`
}

func (h *PaymentHandler) GetPaymentStatus(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	bookingID, err := strconv.ParseInt(r.PathValue("bookingID"), 10, 64)
	if err != nil || bookingID <= 0 {
		http.Error(w, "Invalid booking ID", http.StatusBadRequest)
		return
	}

	payment, err := h.service.GetPaymentStatus(r.Context(), principal.UserID, bookingID)
	switch {
	case errors.Is(err, payments.ErrInvalidBookingID):
		http.Error(w, "Invalid booking ID", http.StatusBadRequest)
		return
	case errors.Is(err, payments.ErrInvalidUserID):
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	case errors.Is(err, payments.ErrPaymentNotFound):
		http.Error(w, "Payment not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, "Failed to get payment", http.StatusInternalServerError)
		return
	}

	retryable := payment.Status == "pending" && payment.FailureCode.Valid
	response := PaymentStatusResponse{
		PaymentID:   payment.ID,
		BookingID:   payment.BookingID,
		AmountMinor: payment.AmountMinor,
		Currency:    payment.Currency,
		Status:      payment.Status,
		PaidAt:      optionalPaymentTime(payment.PaidAt.Valid, payment.PaidAt.Time),
		RefundedAt:  optionalPaymentTime(payment.RefundedAt.Valid, payment.RefundedAt.Time),
		Retryable:   retryable,
		FailureCode: customerPaymentFailureCode(retryable),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func customerPaymentFailureCode(retryable bool) *string {
	if !retryable {
		return nil
	}
	code := "payment_failed"
	return &code
}

func optionalPaymentTime(valid bool, value time.Time) *time.Time {
	if !valid {
		return nil
	}
	value = value.UTC()
	return &value
}

func (h *PaymentHandler) InitiatePayment(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	bookingID, err := strconv.ParseInt(r.PathValue("bookingID"), 10, 64)
	if err != nil || bookingID <= 0 {
		http.Error(w, "Invalid booking ID", http.StatusBadRequest)
		return
	}

	result, err := h.service.InitiatePayment(r.Context(), principal.UserID, bookingID)
	switch {
	case errors.Is(err, payments.ErrInvalidBookingID):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, payments.ErrInvalidUserID):
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	case errors.Is(err, payments.ErrPaymentNotAvailable):
		http.Error(w, "Payment not available", http.StatusNotFound)
		return
	case errors.Is(err, payments.ErrGatewayUnavailable):
		http.Error(w, "Payment service unavailable", http.StatusServiceUnavailable)
		return
	case errors.Is(err, payments.ErrProviderMismatch),
		errors.Is(err, payments.ErrProviderPaymentIDRequired),
		errors.Is(err, payments.ErrProviderPaymentIDMismatch),
		errors.Is(err, payments.ErrCheckoutURLOrClientSecretRequired),
		errors.Is(err, payments.ErrInvalidCurrency),
		errors.Is(err, payments.ErrInvalidRequest):
		http.Error(w, "Payment provider error", http.StatusBadGateway)
		return
	case err != nil:
		http.Error(w, "Failed to initiate payment", http.StatusInternalServerError)
		return
	}

	response := PaymentSessionResponse{
		PaymentID:         result.Payment.ID,
		ProviderPaymentID: result.Session.ProviderPaymentID,
		CheckoutURL:       result.Session.CheckoutURL,
		ClientSecret:      result.Session.ClientSecret,
		AmountMinor:       result.Payment.AmountMinor,
		Currency:          result.Payment.Currency,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}

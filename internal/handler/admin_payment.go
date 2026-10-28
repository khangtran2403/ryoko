package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/khangtran2403/ryoko/internal/payments"
)

type adminPaymentService interface {
	ListPayments(context.Context, payments.ListAdminPaymentsInput) (payments.ListAdminPaymentsResult, error)
	ListRefundAttempts(context.Context, payments.ListAdminRefundAttemptsInput) (payments.ListAdminRefundAttemptsResult, error)
	GetPayment(context.Context, int64) (payments.AdminPayment, error)
	RetryFailedRefund(context.Context, int64) (payments.AdminPayment, error)
}

type AdminPaymentHandler struct {
	service adminPaymentService
}

func NewAdminPaymentHandler(service adminPaymentService) *AdminPaymentHandler {
	if service == nil {
		panic("admin payment service must not be nil")
	}
	return &AdminPaymentHandler{service: service}
}

type AdminPaymentResponse struct {
	ID                int64      `json:"id"`
	BookingID         int64      `json:"booking_id"`
	BookingStatus     string     `json:"booking_status"`
	CustomerID        int64      `json:"customer_id"`
	CustomerName      string     `json:"customer_name"`
	CustomerEmail     string     `json:"customer_email"`
	HotelID           int64      `json:"hotel_id"`
	HotelName         string     `json:"hotel_name"`
	Provider          string     `json:"provider"`
	ProviderPaymentID *string    `json:"provider_payment_id"`
	AmountMinor       int64      `json:"amount_minor"`
	Currency          string     `json:"currency"`
	Status            string     `json:"status"`
	FailureCode       *string    `json:"failure_code"`
	FailureMessage    *string    `json:"failure_message"`
	PaidAt            *time.Time `json:"paid_at"`
	RefundedAt        *time.Time `json:"refunded_at"`
	RefundAttempts    int32      `json:"refund_attempts"`
	RefundAvailableAt *time.Time `json:"refund_available_at"`
	RefundLockedAt    *time.Time `json:"refund_locked_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type ListAdminPaymentsResponse struct {
	Payments   []AdminPaymentResponse          `json:"payments"`
	Pagination payments.AdminPaymentPagination `json:"pagination"`
}

type AdminRefundAttemptResponse struct {
	ID             int64     `json:"id"`
	PaymentID      int64     `json:"payment_id"`
	AttemptNumber  int32     `json:"attempt_number"`
	Status         string    `json:"status"`
	FailureCode    *string   `json:"failure_code"`
	FailureMessage *string   `json:"failure_message"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
}

type ListAdminRefundAttemptsResponse struct {
	Attempts   []AdminRefundAttemptResponse    `json:"attempts"`
	Pagination payments.AdminPaymentPagination `json:"pagination"`
}

func (h *AdminPaymentHandler) List(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, ok := positiveInt32Query(w, query.Get("page"), payments.DefaultAdminPaymentPage, "page")
	if !ok {
		return
	}
	pageSize, ok := positiveInt32Query(
		w,
		query.Get("page_size"),
		payments.DefaultAdminPaymentPageSize,
		"page_size",
	)
	if !ok {
		return
	}
	if pageSize > payments.MaxAdminPaymentPageSize {
		http.Error(w, payments.ErrInvalidAdminPaymentPageSize.Error(), http.StatusBadRequest)
		return
	}

	var status *string
	if raw := strings.TrimSpace(query.Get("status")); raw != "" {
		normalized := strings.ToLower(raw)
		status = &normalized
	}
	var provider *string
	if raw := strings.TrimSpace(query.Get("provider")); raw != "" {
		normalized := strings.ToLower(raw)
		provider = &normalized
	}
	var bookingID *int64
	if raw := query.Get("booking_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			http.Error(w, payments.ErrInvalidBookingID.Error(), http.StatusBadRequest)
			return
		}
		bookingID = &parsed
	}

	result, err := h.service.ListPayments(r.Context(), payments.ListAdminPaymentsInput{
		Status: status, Provider: provider, BookingID: bookingID, Page: page, PageSize: pageSize,
	})
	switch {
	case errors.Is(err, payments.ErrInvalidAdminPaymentStatus),
		errors.Is(err, payments.ErrInvalidAdminPaymentProvider),
		errors.Is(err, payments.ErrInvalidBookingID),
		errors.Is(err, payments.ErrInvalidAdminPaymentPage),
		errors.Is(err, payments.ErrInvalidAdminPaymentPageSize):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "Failed to list payments", http.StatusInternalServerError)
		return
	}

	response := make([]AdminPaymentResponse, len(result.Payments))
	for index, payment := range result.Payments {
		response[index] = adminPaymentResponse(payment)
	}
	writeJSON(w, http.StatusOK, ListAdminPaymentsResponse{
		Payments: response, Pagination: result.Pagination,
	})
}

func (h *AdminPaymentHandler) Get(w http.ResponseWriter, r *http.Request) {
	paymentID, err := strconv.ParseInt(r.PathValue("paymentID"), 10, 64)
	if err != nil || paymentID <= 0 {
		http.Error(w, "Invalid payment ID", http.StatusBadRequest)
		return
	}

	payment, err := h.service.GetPayment(r.Context(), paymentID)
	switch {
	case errors.Is(err, payments.ErrInvalidAdminPaymentID):
		http.Error(w, "Invalid payment ID", http.StatusBadRequest)
		return
	case errors.Is(err, payments.ErrPaymentNotFound):
		http.Error(w, "Payment not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, "Failed to get payment", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, adminPaymentResponse(payment))
}

func (h *AdminPaymentHandler) ListRefundAttempts(w http.ResponseWriter, r *http.Request) {
	paymentID, err := strconv.ParseInt(r.PathValue("paymentID"), 10, 64)
	if err != nil || paymentID <= 0 {
		http.Error(w, "Invalid payment ID", http.StatusBadRequest)
		return
	}

	query := r.URL.Query()
	page, ok := positiveInt32Query(w, query.Get("page"), payments.DefaultAdminPaymentPage, "page")
	if !ok {
		return
	}
	pageSize, ok := positiveInt32Query(
		w,
		query.Get("page_size"),
		payments.DefaultAdminPaymentPageSize,
		"page_size",
	)
	if !ok {
		return
	}
	if pageSize > payments.MaxAdminPaymentPageSize {
		http.Error(w, payments.ErrInvalidAdminPaymentPageSize.Error(), http.StatusBadRequest)
		return
	}

	result, err := h.service.ListRefundAttempts(
		r.Context(),
		payments.ListAdminRefundAttemptsInput{
			PaymentID: paymentID, Page: page, PageSize: pageSize,
		},
	)
	switch {
	case errors.Is(err, payments.ErrInvalidAdminPaymentID),
		errors.Is(err, payments.ErrInvalidAdminPaymentPage),
		errors.Is(err, payments.ErrInvalidAdminPaymentPageSize):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, payments.ErrPaymentNotFound):
		http.Error(w, "Payment not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, "Failed to list payment refund attempts", http.StatusInternalServerError)
		return
	}

	attempts := make([]AdminRefundAttemptResponse, len(result.Attempts))
	for index, attempt := range result.Attempts {
		attempts[index] = AdminRefundAttemptResponse{
			ID: attempt.ID, PaymentID: attempt.PaymentID, AttemptNumber: attempt.AttemptNumber,
			Status:         attempt.Status,
			FailureCode:    optionalAdminPaymentText(attempt.FailureCode.Valid, attempt.FailureCode.String),
			FailureMessage: optionalAdminPaymentText(attempt.FailureMessage.Valid, attempt.FailureMessage.String),
			StartedAt:      attempt.StartedAt.Time.UTC(), CompletedAt: attempt.CompletedAt.Time.UTC(),
		}
	}
	writeJSON(w, http.StatusOK, ListAdminRefundAttemptsResponse{
		Attempts: attempts, Pagination: result.Pagination,
	})
}

func (h *AdminPaymentHandler) RetryRefund(w http.ResponseWriter, r *http.Request) {
	paymentID, err := strconv.ParseInt(r.PathValue("paymentID"), 10, 64)
	if err != nil || paymentID <= 0 {
		http.Error(w, "Invalid payment ID", http.StatusBadRequest)
		return
	}

	payment, err := h.service.RetryFailedRefund(r.Context(), paymentID)
	switch {
	case errors.Is(err, payments.ErrInvalidAdminPaymentID):
		http.Error(w, "Invalid payment ID", http.StatusBadRequest)
		return
	case errors.Is(err, payments.ErrPaymentNotFound):
		http.Error(w, "Payment not found", http.StatusNotFound)
		return
	case errors.Is(err, payments.ErrAdminRefundNotRetryable),
		errors.Is(err, payments.ErrAdminRefundInProgress),
		errors.Is(err, payments.ErrAdminRefundStateChanged):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "Failed to retry payment refund", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusAccepted, adminPaymentResponse(payment))
}

func adminPaymentResponse(payment payments.AdminPayment) AdminPaymentResponse {
	return AdminPaymentResponse{
		ID: payment.ID, BookingID: payment.BookingID, BookingStatus: payment.BookingStatus,
		CustomerID: payment.CustomerID, CustomerName: payment.CustomerName, CustomerEmail: payment.CustomerEmail,
		HotelID: payment.HotelID, HotelName: payment.HotelName, Provider: payment.Provider,
		ProviderPaymentID: optionalAdminPaymentText(payment.ProviderPaymentID.Valid, payment.ProviderPaymentID.String),
		AmountMinor:       payment.AmountMinor, Currency: payment.Currency, Status: payment.Status,
		FailureCode:    optionalAdminPaymentText(payment.FailureCode.Valid, payment.FailureCode.String),
		FailureMessage: optionalAdminPaymentText(payment.FailureMessage.Valid, payment.FailureMessage.String),
		PaidAt:         optionalPaymentTime(payment.PaidAt.Valid, payment.PaidAt.Time),
		RefundedAt:     optionalPaymentTime(payment.RefundedAt.Valid, payment.RefundedAt.Time),
		RefundAttempts: payment.RefundAttempts,
		RefundAvailableAt: optionalPaymentTime(
			payment.RefundAvailableAt.Valid,
			payment.RefundAvailableAt.Time,
		),
		RefundLockedAt: optionalPaymentTime(payment.RefundLockedAt.Valid, payment.RefundLockedAt.Time),
		CreatedAt:      payment.CreatedAt.Time.UTC(), UpdatedAt: payment.UpdatedAt.Time.UTC(),
	}
}

func optionalAdminPaymentText(valid bool, value string) *string {
	if !valid {
		return nil
	}
	return &value
}

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
	"github.com/khangtran2403/ryoko/internal/payments"
)

type fakeAdminPaymentService struct {
	listResult    payments.ListAdminPaymentsResult
	listErr       error
	listInput     payments.ListAdminPaymentsInput
	getResult     payments.AdminPayment
	getErr        error
	getID         int64
	retryResult   payments.AdminPayment
	retryErr      error
	retryID       int64
	attemptResult payments.ListAdminRefundAttemptsResult
	attemptErr    error
	attemptInput  payments.ListAdminRefundAttemptsInput
	summaryResult payments.AdminPaymentOperationsSummary
	summaryErr    error
	summaryCalls  int
}

func (f *fakeAdminPaymentService) GetOperationsSummary(
	_ context.Context,
) (payments.AdminPaymentOperationsSummary, error) {
	f.summaryCalls++
	return f.summaryResult, f.summaryErr
}

func (f *fakeAdminPaymentService) ListRefundAttempts(
	_ context.Context,
	input payments.ListAdminRefundAttemptsInput,
) (payments.ListAdminRefundAttemptsResult, error) {
	f.attemptInput = input
	return f.attemptResult, f.attemptErr
}

func (f *fakeAdminPaymentService) ListPayments(
	_ context.Context,
	input payments.ListAdminPaymentsInput,
) (payments.ListAdminPaymentsResult, error) {
	f.listInput = input
	return f.listResult, f.listErr
}

func (f *fakeAdminPaymentService) GetPayment(
	_ context.Context,
	paymentID int64,
) (payments.AdminPayment, error) {
	f.getID = paymentID
	return f.getResult, f.getErr
}

func (f *fakeAdminPaymentService) RetryFailedRefund(
	_ context.Context,
	paymentID int64,
) (payments.AdminPayment, error) {
	f.retryID = paymentID
	return f.retryResult, f.retryErr
}

func TestAdminPaymentHandlerListsPayments(t *testing.T) {
	now := time.Date(2026, time.October, 27, 10, 0, 0, 0, time.UTC)
	service := &fakeAdminPaymentService{listResult: payments.ListAdminPaymentsResult{
		Payments: []payments.AdminPayment{{
			ID: 4, BookingID: 10, BookingStatus: "cancelled", CustomerID: 2,
			CustomerName: "Khang", CustomerEmail: "khang@example.com", HotelID: 3, HotelName: "Ryoko",
			Provider: "stripe", ProviderPaymentID: pgtype.Text{String: "pi_123", Valid: true},
			AmountMinor: 2500, Currency: "USD", Status: "refund_pending",
			FailureCode:       pgtype.Text{String: "refund_provider_error", Valid: true},
			RefundAttempts:    2,
			RefundAvailableAt: pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true},
			CreatedAt:         pgtype.Timestamptz{Time: now, Valid: true}, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		}},
		Pagination: payments.AdminPaymentPagination{Page: 2, PageSize: 5, HasMore: true},
	}}
	handler := NewAdminPaymentHandler(service)
	request := httptest.NewRequest(
		http.MethodGet,
		"/admin/payments?status=REFUND_PENDING&provider=STRIPE&booking_id=10&page=2&page_size=5",
		nil,
	)
	response := httptest.NewRecorder()

	handler.List(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	if service.listInput.Status == nil || *service.listInput.Status != "refund_pending" ||
		service.listInput.Provider == nil || *service.listInput.Provider != "stripe" ||
		service.listInput.BookingID == nil || *service.listInput.BookingID != 10 ||
		service.listInput.Page != 2 || service.listInput.PageSize != 5 {
		t.Errorf("service input = %+v", service.listInput)
	}
	var body ListAdminPaymentsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Payments) != 1 || body.Payments[0].ProviderPaymentID == nil ||
		*body.Payments[0].ProviderPaymentID != "pi_123" || body.Payments[0].FailureCode == nil ||
		body.Payments[0].RefundAttempts != 2 || body.Payments[0].RefundAvailableAt == nil {
		t.Errorf("response = %+v", body)
	}
}

func TestAdminPaymentHandlerGetsOperationsSummary(t *testing.T) {
	oldestPayment := time.Date(2026, time.October, 29, 8, 0, 0, 0, time.UTC)
	oldestRefund := oldestPayment.Add(time.Hour)
	service := &fakeAdminPaymentService{summaryResult: payments.AdminPaymentOperationsSummary{
		PaymentCounts: payments.AdminPaymentStatusCounts{
			Pending: 2, Succeeded: 3, Expired: 4, RefundPending: 5, Refunded: 6,
		},
		RefundQueue: payments.AdminRefundQueueSummary{
			Ready: 1, Delayed: 2, Leased: 1, Failed: 3,
			OldestPendingAt: pgtype.Timestamptz{Time: oldestRefund, Valid: true},
		},
		CurrencyTotals: []payments.AdminPaymentCurrencyTotal{
			{Currency: "USD", PaidCount: 2, PaidAmountMinor: 5000, RefundedCount: 1, RefundedAmountMinor: 2000},
			{Currency: "VND", PaidCount: 3, PaidAmountMinor: 9000000, RefundedCount: 2, RefundedAmountMinor: 4000000},
		},
		OldestPendingPaymentAt: pgtype.Timestamptz{Time: oldestPayment, Valid: true},
	}}
	response := httptest.NewRecorder()
	NewAdminPaymentHandler(service).Summary(
		response,
		httptest.NewRequest(http.MethodGet, "/admin/payments/summary", nil),
	)

	if response.Code != http.StatusOK || service.summaryCalls != 1 {
		t.Fatalf("status = %d; calls = %d; body = %s", response.Code, service.summaryCalls, response.Body.String())
	}
	var body AdminPaymentOperationsSummaryResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.PaymentCounts.Pending != 2 || body.PaymentCounts.Refunded != 6 ||
		body.RefundQueue.Failed != 3 || body.RefundQueue.OldestPendingAt == nil ||
		body.OldestPendingPaymentAt == nil || len(body.CurrencyTotals) != 2 ||
		body.CurrencyTotals[0].Currency != "USD" || body.CurrencyTotals[1].Currency != "VND" {
		t.Errorf("response = %+v", body)
	}
}

func TestAdminPaymentHandlerMapsOperationsSummaryError(t *testing.T) {
	response := httptest.NewRecorder()
	NewAdminPaymentHandler(&fakeAdminPaymentService{
		summaryErr: errors.New("database unavailable"),
	}).Summary(response, httptest.NewRequest(http.MethodGet, "/admin/payments/summary", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", response.Code, response.Body.String())
	}
}

func TestAdminPaymentHandlerRejectsInvalidListParameters(t *testing.T) {
	tests := []string{
		"/admin/payments?page=0",
		"/admin/payments?page_size=101",
		"/admin/payments?booking_id=nope",
	}
	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewAdminPaymentHandler(&fakeAdminPaymentService{}).List(
				response,
				httptest.NewRequest(http.MethodGet, path, nil),
			)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
		})
	}
}

func TestAdminPaymentHandlerMapsListServiceErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "validation", err: payments.ErrInvalidAdminPaymentStatus, want: http.StatusBadRequest},
		{name: "internal", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewAdminPaymentHandler(&fakeAdminPaymentService{listErr: tt.err}).List(
				response,
				httptest.NewRequest(http.MethodGet, "/admin/payments", nil),
			)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
		})
	}
}

func TestAdminPaymentHandlerGetsPayment(t *testing.T) {
	now := time.Now().UTC()
	service := &fakeAdminPaymentService{getResult: payments.AdminPayment{
		ID: 5, Status: "succeeded", CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}}
	request := httptest.NewRequest(http.MethodGet, "/admin/payments/5", nil)
	request.SetPathValue("paymentID", "5")
	response := httptest.NewRecorder()
	NewAdminPaymentHandler(service).Get(response, request)

	if response.Code != http.StatusOK || service.getID != 5 {
		t.Fatalf("status = %d; queried ID = %d; body = %s", response.Code, service.getID, response.Body.String())
	}
	var body AdminPaymentResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != 5 || body.Status != "succeeded" {
		t.Errorf("response = %+v", body)
	}
}

func TestAdminPaymentHandlerMapsGetErrors(t *testing.T) {
	tests := []struct {
		name string
		id   string
		err  error
		want int
	}{
		{name: "invalid path", id: "bad", want: http.StatusBadRequest},
		{name: "missing", id: "5", err: payments.ErrPaymentNotFound, want: http.StatusNotFound},
		{name: "internal", id: "5", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin/payments/"+tt.id, nil)
			request.SetPathValue("paymentID", tt.id)
			response := httptest.NewRecorder()
			NewAdminPaymentHandler(&fakeAdminPaymentService{getErr: tt.err}).Get(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
		})
	}
}

func TestAdminPaymentHandlerRetriesRefund(t *testing.T) {
	now := time.Now().UTC()
	service := &fakeAdminPaymentService{retryResult: payments.AdminPayment{
		ID: 8, Status: "refund_pending",
		RefundAvailableAt: pgtype.Timestamptz{Time: now, Valid: true},
		CreatedAt:         pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt:         pgtype.Timestamptz{Time: now, Valid: true},
	}}
	request := httptest.NewRequest(http.MethodPost, "/admin/payments/8/retry-refund", nil)
	request.SetPathValue("paymentID", "8")
	response := httptest.NewRecorder()
	NewAdminPaymentHandler(service).RetryRefund(response, request)

	if response.Code != http.StatusAccepted || service.retryID != 8 {
		t.Fatalf("status = %d; retry ID = %d; body = %s", response.Code, service.retryID, response.Body.String())
	}
	var body AdminPaymentResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != 8 || body.RefundAvailableAt == nil || body.FailureCode != nil {
		t.Errorf("response = %+v", body)
	}
}

func TestAdminPaymentHandlerMapsRefundRetryErrors(t *testing.T) {
	tests := []struct {
		name string
		id   string
		err  error
		want int
	}{
		{name: "invalid path", id: "bad", want: http.StatusBadRequest},
		{name: "missing", id: "8", err: payments.ErrPaymentNotFound, want: http.StatusNotFound},
		{name: "not retryable", id: "8", err: payments.ErrAdminRefundNotRetryable, want: http.StatusConflict},
		{name: "in progress", id: "8", err: payments.ErrAdminRefundInProgress, want: http.StatusConflict},
		{name: "state changed", id: "8", err: payments.ErrAdminRefundStateChanged, want: http.StatusConflict},
		{name: "internal", id: "8", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/admin/payments/"+tt.id+"/retry-refund", nil)
			request.SetPathValue("paymentID", tt.id)
			response := httptest.NewRecorder()
			NewAdminPaymentHandler(&fakeAdminPaymentService{retryErr: tt.err}).RetryRefund(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
		})
	}
}

func TestAdminPaymentHandlerListsRefundAttempts(t *testing.T) {
	now := time.Date(2026, time.October, 28, 14, 0, 0, 0, time.UTC)
	service := &fakeAdminPaymentService{attemptResult: payments.ListAdminRefundAttemptsResult{
		Attempts: []payments.AdminRefundAttempt{{
			ID: 7, PaymentID: 8, AttemptNumber: 2, Status: "failed",
			FailureCode: pgtype.Text{String: "refund_provider_error", Valid: true},
			StartedAt:   pgtype.Timestamptz{Time: now, Valid: true},
			CompletedAt: pgtype.Timestamptz{Time: now.Add(time.Second), Valid: true},
		}},
		Pagination: payments.AdminPaymentPagination{Page: 2, PageSize: 5, HasMore: true},
	}}
	request := httptest.NewRequest(http.MethodGet, "/admin/payments/8/refund-attempts?page=2&page_size=5", nil)
	request.SetPathValue("paymentID", "8")
	response := httptest.NewRecorder()
	NewAdminPaymentHandler(service).ListRefundAttempts(response, request)

	if response.Code != http.StatusOK || service.attemptInput.PaymentID != 8 ||
		service.attemptInput.Page != 2 || service.attemptInput.PageSize != 5 {
		t.Fatalf("status = %d; input = %+v; body = %s", response.Code, service.attemptInput, response.Body.String())
	}
	var body ListAdminRefundAttemptsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Attempts) != 1 || body.Attempts[0].AttemptNumber != 2 ||
		body.Attempts[0].FailureCode == nil || !body.Pagination.HasMore {
		t.Errorf("response = %+v", body)
	}
}

func TestAdminPaymentHandlerMapsRefundAttemptListErrors(t *testing.T) {
	tests := []struct {
		name string
		path string
		err  error
		want int
	}{
		{name: "invalid ID", path: "/admin/payments/bad/refund-attempts", want: http.StatusBadRequest},
		{name: "invalid page", path: "/admin/payments/8/refund-attempts?page=0", want: http.StatusBadRequest},
		{name: "invalid page size", path: "/admin/payments/8/refund-attempts?page_size=101", want: http.StatusBadRequest},
		{name: "missing", path: "/admin/payments/8/refund-attempts", err: payments.ErrPaymentNotFound, want: http.StatusNotFound},
		{name: "internal", path: "/admin/payments/8/refund-attempts", err: errors.New("database unavailable"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.SetPathValue("paymentID", strings.Split(tt.path, "/")[3])
			response := httptest.NewRecorder()
			NewAdminPaymentHandler(&fakeAdminPaymentService{attemptErr: tt.err}).ListRefundAttempts(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}

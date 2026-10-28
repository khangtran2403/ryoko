package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type fakeAdminPaymentQueries struct {
	listRows      []sqlc.ListPaymentsForAdminRow
	listErr       error
	listParams    sqlc.ListPaymentsForAdminParams
	getRow        sqlc.GetPaymentForAdminRow
	getErr        error
	getID         int64
	listCalls     int
	getCalls      int
	retryRow      sqlc.RetryFailedPaymentRefundForAdminRow
	retryErr      error
	retryID       int64
	retryCalls    int
	attemptRows   []sqlc.PaymentRefundAttempt
	attemptErr    error
	attemptParams sqlc.ListPaymentRefundAttemptsForAdminParams
	attemptCalls  int
}

func (f *fakeAdminPaymentQueries) ListPaymentRefundAttemptsForAdmin(
	_ context.Context,
	params sqlc.ListPaymentRefundAttemptsForAdminParams,
) ([]sqlc.PaymentRefundAttempt, error) {
	f.attemptCalls++
	f.attemptParams = params
	return f.attemptRows, f.attemptErr
}

func (f *fakeAdminPaymentQueries) ListPaymentsForAdmin(
	_ context.Context,
	params sqlc.ListPaymentsForAdminParams,
) ([]sqlc.ListPaymentsForAdminRow, error) {
	f.listCalls++
	f.listParams = params
	return f.listRows, f.listErr
}

func (f *fakeAdminPaymentQueries) GetPaymentForAdmin(
	_ context.Context,
	paymentID int64,
) (sqlc.GetPaymentForAdminRow, error) {
	f.getCalls++
	f.getID = paymentID
	return f.getRow, f.getErr
}

func (f *fakeAdminPaymentQueries) RetryFailedPaymentRefundForAdmin(
	_ context.Context,
	paymentID int64,
) (sqlc.RetryFailedPaymentRefundForAdminRow, error) {
	f.retryCalls++
	f.retryID = paymentID
	return f.retryRow, f.retryErr
}

func TestAdminPaymentServiceListsFilteredPayments(t *testing.T) {
	status, provider, bookingID := "refund_pending", "stripe", int64(77)
	queries := &fakeAdminPaymentQueries{listRows: []sqlc.ListPaymentsForAdminRow{
		{ID: 3, BookingID: bookingID, Status: status},
		{ID: 2, BookingID: bookingID, Status: status},
		{ID: 1, BookingID: bookingID, Status: status},
	}}

	result, err := NewAdminService(queries).ListPayments(context.Background(), ListAdminPaymentsInput{
		Status: &status, Provider: &provider, BookingID: &bookingID, Page: 2, PageSize: 2,
	})
	if err != nil {
		t.Fatalf("ListPayments() error = %v", err)
	}
	if len(result.Payments) != 2 || result.Payments[0].ID != 3 || !result.Pagination.HasMore {
		t.Fatalf("result = %+v, want two payments with has_more", result)
	}
	if queries.listParams.PaymentStatus.String != status || !queries.listParams.PaymentStatus.Valid ||
		queries.listParams.Provider.String != provider || !queries.listParams.Provider.Valid ||
		queries.listParams.BookingID.Int64 != bookingID || !queries.listParams.BookingID.Valid {
		t.Errorf("filter params = %+v", queries.listParams)
	}
	if queries.listParams.ResultLimit != 3 || queries.listParams.ResultOffset != 2 {
		t.Errorf("pagination params = %+v, want limit 3 offset 2", queries.listParams)
	}
}

func TestAdminPaymentServiceReturnsNonNilEmptyList(t *testing.T) {
	result, err := NewAdminService(&fakeAdminPaymentQueries{}).ListPayments(
		context.Background(),
		ListAdminPaymentsInput{Page: 1, PageSize: 20},
	)
	if err != nil {
		t.Fatalf("ListPayments() error = %v", err)
	}
	if result.Payments == nil || len(result.Payments) != 0 {
		t.Fatalf("payments = %#v, want non-nil empty slice", result.Payments)
	}
}

func TestAdminPaymentServiceValidatesListInput(t *testing.T) {
	invalidStatus, blankProvider, longProvider := "failed", "  ", string(make([]byte, 101))
	invalidBookingID := int64(0)
	tests := []struct {
		name  string
		input ListAdminPaymentsInput
		want  error
	}{
		{name: "status", input: ListAdminPaymentsInput{Status: &invalidStatus, Page: 1, PageSize: 20}, want: ErrInvalidAdminPaymentStatus},
		{name: "blank provider", input: ListAdminPaymentsInput{Provider: &blankProvider, Page: 1, PageSize: 20}, want: ErrInvalidAdminPaymentProvider},
		{name: "long provider", input: ListAdminPaymentsInput{Provider: &longProvider, Page: 1, PageSize: 20}, want: ErrInvalidAdminPaymentProvider},
		{name: "booking", input: ListAdminPaymentsInput{BookingID: &invalidBookingID, Page: 1, PageSize: 20}, want: ErrInvalidBookingID},
		{name: "page", input: ListAdminPaymentsInput{Page: 0, PageSize: 20}, want: ErrInvalidAdminPaymentPage},
		{name: "page size", input: ListAdminPaymentsInput{Page: 1, PageSize: 101}, want: ErrInvalidAdminPaymentPageSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries := &fakeAdminPaymentQueries{}
			_, err := NewAdminService(queries).ListPayments(context.Background(), tt.input)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if queries.listCalls != 0 {
				t.Errorf("query calls = %d, want 0", queries.listCalls)
			}
		})
	}
}

func TestAdminPaymentServiceGetsPayment(t *testing.T) {
	queries := &fakeAdminPaymentQueries{getRow: sqlc.GetPaymentForAdminRow{
		ID: 9, BookingID: 17, Provider: "stripe", ProviderPaymentID: pgtype.Text{String: "pi_123", Valid: true},
	}}
	payment, err := NewAdminService(queries).GetPayment(context.Background(), 9)
	if err != nil {
		t.Fatalf("GetPayment() error = %v", err)
	}
	if payment.ID != 9 || payment.ProviderPaymentID.String != "pi_123" || queries.getID != 9 {
		t.Errorf("payment = %+v; queried ID = %d", payment, queries.getID)
	}
}

func TestAdminPaymentServiceMapsMissingPayment(t *testing.T) {
	queries := &fakeAdminPaymentQueries{getErr: pgx.ErrNoRows}
	_, err := NewAdminService(queries).GetPayment(context.Background(), 8)
	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("error = %v, want ErrPaymentNotFound", err)
	}

	_, err = NewAdminService(queries).GetPayment(context.Background(), 0)
	if !errors.Is(err, ErrInvalidAdminPaymentID) {
		t.Fatalf("invalid ID error = %v", err)
	}
	if queries.getCalls != 1 {
		t.Errorf("get calls = %d, want 1", queries.getCalls)
	}
}

func TestAdminPaymentServiceRetriesFailedRefund(t *testing.T) {
	now := time.Date(2026, time.October, 28, 12, 0, 0, 0, time.UTC)
	queries := &fakeAdminPaymentQueries{retryRow: sqlc.RetryFailedPaymentRefundForAdminRow{
		ID: 12, Status: "refund_pending", RefundAttempts: 3,
		RefundAvailableAt: pgtype.Timestamptz{Time: now, Valid: true},
	}}
	payment, err := NewAdminService(queries).RetryFailedRefund(context.Background(), 12)
	if err != nil {
		t.Fatalf("RetryFailedRefund() error = %v", err)
	}
	if payment.ID != 12 || payment.Status != "refund_pending" || payment.RefundAttempts != 3 ||
		payment.FailureCode.Valid || payment.RefundLockedAt.Valid || queries.retryID != 12 {
		t.Errorf("payment = %+v; retry ID = %d", payment, queries.retryID)
	}
}

func TestAdminPaymentServiceMapsRefundRetryConflicts(t *testing.T) {
	lockedAt := pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	tests := []struct {
		name   string
		getRow sqlc.GetPaymentForAdminRow
		getErr error
		want   error
	}{
		{name: "missing", getErr: pgx.ErrNoRows, want: ErrPaymentNotFound},
		{name: "wrong status", getRow: sqlc.GetPaymentForAdminRow{Status: "refunded"}, want: ErrAdminRefundNotRetryable},
		{name: "no failure", getRow: sqlc.GetPaymentForAdminRow{Status: "refund_pending"}, want: ErrAdminRefundNotRetryable},
		{name: "active lease", getRow: sqlc.GetPaymentForAdminRow{
			Status: "refund_pending", FailureCode: pgtype.Text{String: "refund_provider_error", Valid: true}, RefundLockedAt: lockedAt,
		}, want: ErrAdminRefundInProgress},
		{name: "concurrent change", getRow: sqlc.GetPaymentForAdminRow{
			Status: "refund_pending", FailureCode: pgtype.Text{String: "refund_provider_error", Valid: true},
		}, want: ErrAdminRefundStateChanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries := &fakeAdminPaymentQueries{
				retryErr: pgx.ErrNoRows, getRow: tt.getRow, getErr: tt.getErr,
			}
			_, err := NewAdminService(queries).RetryFailedRefund(context.Background(), 7)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAdminPaymentServiceRejectsInvalidRefundRetryID(t *testing.T) {
	queries := &fakeAdminPaymentQueries{}
	_, err := NewAdminService(queries).RetryFailedRefund(context.Background(), 0)
	if !errors.Is(err, ErrInvalidAdminPaymentID) || queries.retryCalls != 0 {
		t.Fatalf("error = %v; retry calls = %d", err, queries.retryCalls)
	}
}

func TestAdminPaymentServiceListsRefundAttempts(t *testing.T) {
	now := time.Date(2026, time.October, 28, 13, 0, 0, 0, time.UTC)
	queries := &fakeAdminPaymentQueries{
		getRow: sqlc.GetPaymentForAdminRow{ID: 12},
		attemptRows: []sqlc.PaymentRefundAttempt{
			{ID: 3, PaymentID: 12, AttemptNumber: 3, Status: "succeeded", StartedAt: pgtype.Timestamptz{Time: now, Valid: true}, CompletedAt: pgtype.Timestamptz{Time: now.Add(time.Second), Valid: true}},
			{ID: 2, PaymentID: 12, AttemptNumber: 2, Status: "failed", FailureCode: pgtype.Text{String: "refund_provider_error", Valid: true}},
			{ID: 1, PaymentID: 12, AttemptNumber: 1, Status: "failed"},
		},
	}

	result, err := NewAdminService(queries).ListRefundAttempts(
		context.Background(),
		ListAdminRefundAttemptsInput{PaymentID: 12, Page: 2, PageSize: 2},
	)
	if err != nil {
		t.Fatalf("ListRefundAttempts() error = %v", err)
	}
	if len(result.Attempts) != 2 || result.Attempts[0].AttemptNumber != 3 ||
		!result.Pagination.HasMore {
		t.Fatalf("result = %+v", result)
	}
	if queries.getID != 12 || queries.attemptParams.PaymentID != 12 ||
		queries.attemptParams.ResultOffset != 2 || queries.attemptParams.ResultLimit != 3 {
		t.Errorf("get ID = %d; attempt params = %+v", queries.getID, queries.attemptParams)
	}
}

func TestAdminPaymentServiceReturnsNonNilEmptyRefundAttempts(t *testing.T) {
	queries := &fakeAdminPaymentQueries{getRow: sqlc.GetPaymentForAdminRow{ID: 4}}
	result, err := NewAdminService(queries).ListRefundAttempts(
		context.Background(),
		ListAdminRefundAttemptsInput{PaymentID: 4, Page: 1, PageSize: 20},
	)
	if err != nil {
		t.Fatalf("ListRefundAttempts() error = %v", err)
	}
	if result.Attempts == nil || len(result.Attempts) != 0 {
		t.Fatalf("attempts = %#v, want non-nil empty slice", result.Attempts)
	}
}

func TestAdminPaymentServiceValidatesRefundAttemptList(t *testing.T) {
	tests := []struct {
		name  string
		input ListAdminRefundAttemptsInput
		want  error
	}{
		{name: "payment ID", input: ListAdminRefundAttemptsInput{Page: 1, PageSize: 20}, want: ErrInvalidAdminPaymentID},
		{name: "page", input: ListAdminRefundAttemptsInput{PaymentID: 1, PageSize: 20}, want: ErrInvalidAdminPaymentPage},
		{name: "page size", input: ListAdminRefundAttemptsInput{PaymentID: 1, Page: 1, PageSize: 101}, want: ErrInvalidAdminPaymentPageSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queries := &fakeAdminPaymentQueries{}
			_, err := NewAdminService(queries).ListRefundAttempts(context.Background(), tt.input)
			if !errors.Is(err, tt.want) || queries.getCalls != 0 || queries.attemptCalls != 0 {
				t.Fatalf("error = %v; get calls = %d; attempt calls = %d", err, queries.getCalls, queries.attemptCalls)
			}
		})
	}
}

func TestAdminPaymentServiceMapsRefundAttemptListErrors(t *testing.T) {
	queries := &fakeAdminPaymentQueries{getErr: pgx.ErrNoRows}
	_, err := NewAdminService(queries).ListRefundAttempts(
		context.Background(),
		ListAdminRefundAttemptsInput{PaymentID: 99, Page: 1, PageSize: 20},
	)
	if !errors.Is(err, ErrPaymentNotFound) || queries.attemptCalls != 0 {
		t.Fatalf("missing error = %v; attempt calls = %d", err, queries.attemptCalls)
	}

	queries = &fakeAdminPaymentQueries{
		getRow: sqlc.GetPaymentForAdminRow{ID: 8}, attemptErr: errors.New("database unavailable"),
	}
	_, err = NewAdminService(queries).ListRefundAttempts(
		context.Background(),
		ListAdminRefundAttemptsInput{PaymentID: 8, Page: 1, PageSize: 20},
	)
	if err == nil || !strings.Contains(err.Error(), "list payment refund attempts") {
		t.Fatalf("query error = %v", err)
	}
}

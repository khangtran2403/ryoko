package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type fakeRefundQueries struct {
	pending      []sqlc.Payment
	claimErr     error
	markErr      error
	retryErr     error
	retryRows    int64
	successErr   error
	successRows  int64
	claimParams  sqlc.ClaimRefundPendingPaymentsParams
	successCalls []sqlc.RecordSuccessfulPaymentRefundAttemptParams
	markCalls    []sqlc.MarkPaymentRefundedParams
	retryCalls   []sqlc.SchedulePaymentRefundRetryParams
}

func (f *fakeRefundQueries) RecordSuccessfulPaymentRefundAttempt(
	_ context.Context,
	params sqlc.RecordSuccessfulPaymentRefundAttemptParams,
) (int64, error) {
	f.successCalls = append(f.successCalls, params)
	return f.successRows, f.successErr
}

func (f *fakeRefundQueries) ClaimRefundPendingPayments(
	_ context.Context,
	params sqlc.ClaimRefundPendingPaymentsParams,
) ([]sqlc.Payment, error) {
	f.claimParams = params
	return f.pending, f.claimErr
}

func (f *fakeRefundQueries) MarkPaymentRefunded(
	_ context.Context,
	params sqlc.MarkPaymentRefundedParams,
) (sqlc.MarkPaymentRefundedRow, error) {
	f.markCalls = append(f.markCalls, params)
	return sqlc.MarkPaymentRefundedRow{ID: params.PaymentID, Status: "refunded"}, f.markErr
}

func (f *fakeRefundQueries) SchedulePaymentRefundRetry(
	_ context.Context,
	params sqlc.SchedulePaymentRefundRetryParams,
) (int64, error) {
	f.retryCalls = append(f.retryCalls, params)
	return f.retryRows, f.retryErr
}

func TestRefundServiceProcessesClaimedRefund(t *testing.T) {
	payment := refundableTestPayment()
	queries := &fakeRefundQueries{pending: []sqlc.Payment{payment}, successRows: 1}
	gateway := &recordingGateway{provider: FakeProvider}
	service := NewRefundService(queries, gateway, 5*time.Minute, 25, time.Minute)

	result, err := service.ProcessPendingRefunds(context.Background())
	if err != nil {
		t.Fatalf("ProcessPendingRefunds() error = %v", err)
	}
	if result != (RefundBatchResult{Attempted: 1, Refunded: 1}) {
		t.Errorf("result = %+v, want one refunded payment", result)
	}
	if queries.claimParams.BatchSize != 25 || queries.claimParams.LeaseSeconds != 300 {
		t.Errorf("claim params = %+v", queries.claimParams)
	}
	if len(queries.markCalls) != 1 || queries.markCalls[0].PaymentID != payment.ID ||
		queries.markCalls[0].RefundLockedAt != payment.RefundLockedAt {
		t.Errorf("mark calls = %+v", queries.markCalls)
	}
	if len(queries.successCalls) != 1 || queries.successCalls[0].PaymentID != payment.ID ||
		queries.successCalls[0].RefundLockedAt != payment.RefundLockedAt {
		t.Errorf("successful-attempt calls = %+v", queries.successCalls)
	}
	calls := gateway.recordedRefundCalls()
	if len(calls) != 1 {
		t.Fatalf("gateway calls = %d, want 1", len(calls))
	}
	want := RefundRequest{
		PaymentID: payment.ID, ProviderPaymentID: payment.ProviderPaymentID.String,
		AmountMinor: payment.AmountMinor, Currency: payment.Currency,
		IdempotencyKey: "booking:29:payment:refund",
	}
	if calls[0] != want {
		t.Errorf("refund request = %+v, want %+v", calls[0], want)
	}
}

func TestRefundServiceSchedulesProviderFailure(t *testing.T) {
	payment := refundableTestPayment()
	queries := &fakeRefundQueries{pending: []sqlc.Payment{payment}, retryRows: 1}
	gateway := &recordingGateway{provider: FakeProvider, refundErr: ErrGatewayUnavailable}
	service := NewRefundService(queries, gateway, 5*time.Minute, 100, time.Minute)

	result, err := service.ProcessPendingRefunds(context.Background())
	if !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("ProcessPendingRefunds() error = %v, want gateway error", err)
	}
	if result != (RefundBatchResult{Attempted: 1, Failed: 1}) {
		t.Errorf("result = %+v, want one failed payment", result)
	}
	if len(queries.markCalls) != 0 {
		t.Errorf("marked refunds = %+v, want none", queries.markCalls)
	}
	if len(queries.retryCalls) != 1 {
		t.Fatalf("retry calls = %+v", queries.retryCalls)
	}
	retry := queries.retryCalls[0]
	if retry.PaymentID != payment.ID || retry.RefundLockedAt != payment.RefundLockedAt ||
		retry.RetryAfterSeconds != 60 || !retry.FailureMessage.Valid {
		t.Errorf("retry call = %+v", retry)
	}
}

func TestRefundServiceRejectsInvalidStoredPaymentWithoutCallingGateway(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*sqlc.Payment)
	}{
		{name: "provider mismatch", mutate: func(p *sqlc.Payment) { p.Provider = "other" }},
		{name: "missing provider payment ID", mutate: func(p *sqlc.Payment) { p.ProviderPaymentID = pgtype.Text{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payment := refundableTestPayment()
			tt.mutate(&payment)
			queries := &fakeRefundQueries{pending: []sqlc.Payment{payment}, retryRows: 1}
			gateway := &recordingGateway{provider: FakeProvider}
			result, err := NewRefundService(
				queries, gateway, 5*time.Minute, 100, time.Minute,
			).ProcessPendingRefunds(context.Background())
			if err == nil || result.Failed != 1 {
				t.Fatalf("result = %+v error = %v, want failed invariant", result, err)
			}
			if len(gateway.recordedRefundCalls()) != 0 {
				t.Fatal("gateway called for invalid stored payment")
			}
			if len(queries.retryCalls) != 1 {
				t.Errorf("retry calls = %d, want 1", len(queries.retryCalls))
			}
		})
	}
}

func TestRefundServiceReturnsClaimFailure(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	result, err := NewRefundService(
		&fakeRefundQueries{claimErr: databaseErr},
		&recordingGateway{provider: FakeProvider},
		5*time.Minute,
		100,
		time.Minute,
	).ProcessPendingRefunds(context.Background())
	if !errors.Is(err, databaseErr) || result != (RefundBatchResult{}) {
		t.Errorf("result = %+v error = %v, want database error", result, err)
	}
}

func TestRefundServiceDetectsLostLease(t *testing.T) {
	payment := refundableTestPayment()
	queries := &fakeRefundQueries{pending: []sqlc.Payment{payment}, successRows: 1, markErr: pgx.ErrNoRows}
	result, err := NewRefundService(
		queries,
		&recordingGateway{provider: FakeProvider},
		5*time.Minute,
		100,
		time.Minute,
	).ProcessPendingRefunds(context.Background())
	if !errors.Is(err, ErrRefundLeaseLost) || result.Failed != 1 {
		t.Fatalf("result = %+v error = %v, want lost lease", result, err)
	}
}

func TestRefundServiceDoesNotFinalizeWhenSuccessfulAttemptLeaseIsLost(t *testing.T) {
	payment := refundableTestPayment()
	queries := &fakeRefundQueries{pending: []sqlc.Payment{payment}}
	result, err := NewRefundService(
		queries,
		&recordingGateway{provider: FakeProvider},
		5*time.Minute,
		100,
		time.Minute,
	).ProcessPendingRefunds(context.Background())
	if !errors.Is(err, ErrRefundLeaseLost) || result != (RefundBatchResult{Attempted: 1, Failed: 1}) {
		t.Fatalf("result = %+v error = %v, want lost lease", result, err)
	}
	if len(queries.markCalls) != 0 {
		t.Errorf("mark calls = %+v, want none", queries.markCalls)
	}
}

func TestRefundServiceRetryDelay(t *testing.T) {
	service := NewRefundService(
		&fakeRefundQueries{},
		&recordingGateway{provider: FakeProvider},
		5*time.Minute,
		100,
		time.Minute,
	)
	tests := []struct {
		attempts int32
		want     time.Duration
	}{
		{attempts: 0, want: time.Minute},
		{attempts: 1, want: time.Minute},
		{attempts: 2, want: 2 * time.Minute},
		{attempts: 5, want: 16 * time.Minute},
		{attempts: 20, want: 1024 * time.Minute},
	}
	for _, tt := range tests {
		if got := service.retryDelay(tt.attempts); got != tt.want {
			t.Errorf("retryDelay(%d) = %v, want %v", tt.attempts, got, tt.want)
		}
	}
}

func refundableTestPayment() sqlc.Payment {
	lockedAt := time.Date(2026, time.October, 28, 9, 0, 0, 0, time.UTC)
	return sqlc.Payment{
		ID: 17, BookingID: 29, Provider: FakeProvider,
		ProviderPaymentID:      pgtype.Text{String: "fake-payment-17", Valid: true},
		ProviderIdempotencyKey: "booking:29:payment:create",
		AmountMinor:            250_000, Currency: "VND", Status: "refund_pending",
		RefundAttempts: 1,
		RefundLockedAt: pgtype.Timestamptz{Time: lockedAt, Valid: true},
	}
}

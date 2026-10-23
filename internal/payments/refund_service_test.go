package payments

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type fakeRefundQueries struct {
	pending      []sqlc.Payment
	listErr      error
	markErr      error
	recordErr    error
	batchSize    int32
	markedIDs    []int64
	failureCalls []sqlc.RecordPaymentRefundFailureParams
}

func (f *fakeRefundQueries) ListRefundPendingPayments(_ context.Context, batchSize int32) ([]sqlc.Payment, error) {
	f.batchSize = batchSize
	return f.pending, f.listErr
}

func (f *fakeRefundQueries) MarkPaymentRefunded(_ context.Context, paymentID int64) (sqlc.MarkPaymentRefundedRow, error) {
	f.markedIDs = append(f.markedIDs, paymentID)
	return sqlc.MarkPaymentRefundedRow{ID: paymentID, Status: "refunded"}, f.markErr
}

func (f *fakeRefundQueries) RecordPaymentRefundFailure(_ context.Context, arg sqlc.RecordPaymentRefundFailureParams) error {
	f.failureCalls = append(f.failureCalls, arg)
	return f.recordErr
}

func TestRefundServiceProcessesPendingRefund(t *testing.T) {
	payment := refundableTestPayment()
	queries := &fakeRefundQueries{pending: []sqlc.Payment{payment}}
	gateway := &recordingGateway{provider: FakeProvider}
	service := NewRefundService(queries, gateway, 25)

	result, err := service.ProcessPendingRefunds(context.Background())
	if err != nil {
		t.Fatalf("ProcessPendingRefunds() error = %v", err)
	}
	if result != (RefundBatchResult{Attempted: 1, Refunded: 1}) {
		t.Errorf("result = %+v, want one refunded payment", result)
	}
	if queries.batchSize != 25 || len(queries.markedIDs) != 1 || queries.markedIDs[0] != payment.ID {
		t.Errorf("query calls = batch %d marked %v", queries.batchSize, queries.markedIDs)
	}
	calls := gateway.recordedRefundCalls()
	if len(calls) != 1 {
		t.Fatalf("gateway calls = %d, want 1", len(calls))
	}
	want := RefundRequest{
		PaymentID:         payment.ID,
		ProviderPaymentID: payment.ProviderPaymentID.String,
		AmountMinor:       payment.AmountMinor,
		Currency:          payment.Currency,
		IdempotencyKey:    "booking:29:payment:refund",
	}
	if calls[0] != want {
		t.Errorf("refund request = %+v, want %+v", calls[0], want)
	}
}

func TestRefundServiceRecordsProviderFailureForRetry(t *testing.T) {
	payment := refundableTestPayment()
	queries := &fakeRefundQueries{pending: []sqlc.Payment{payment}}
	gateway := &recordingGateway{provider: FakeProvider, refundErr: ErrGatewayUnavailable}
	service := NewRefundService(queries, gateway, 100)

	result, err := service.ProcessPendingRefunds(context.Background())
	if !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("ProcessPendingRefunds() error = %v, want gateway error", err)
	}
	if result != (RefundBatchResult{Attempted: 1, Failed: 1}) {
		t.Errorf("result = %+v, want one failed payment", result)
	}
	if len(queries.markedIDs) != 0 {
		t.Errorf("marked refunded IDs = %v, want none", queries.markedIDs)
	}
	if len(queries.failureCalls) != 1 || queries.failureCalls[0].PaymentID != payment.ID || !queries.failureCalls[0].FailureMessage.Valid {
		t.Fatalf("failure calls = %+v, want recorded provider failure", queries.failureCalls)
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
			queries := &fakeRefundQueries{pending: []sqlc.Payment{payment}}
			gateway := &recordingGateway{provider: FakeProvider}
			result, err := NewRefundService(queries, gateway, 100).ProcessPendingRefunds(context.Background())
			if err == nil || result.Failed != 1 {
				t.Fatalf("result = %+v error = %v, want failed invariant", result, err)
			}
			if len(gateway.recordedRefundCalls()) != 0 {
				t.Fatal("gateway called for invalid stored payment")
			}
			if len(queries.failureCalls) != 1 {
				t.Errorf("failure calls = %d, want 1", len(queries.failureCalls))
			}
		})
	}
}

func TestRefundServiceReturnsListFailure(t *testing.T) {
	databaseErr := errors.New("database unavailable")
	result, err := NewRefundService(
		&fakeRefundQueries{listErr: databaseErr},
		&recordingGateway{provider: FakeProvider},
		100,
	).ProcessPendingRefunds(context.Background())
	if !errors.Is(err, databaseErr) || result != (RefundBatchResult{}) {
		t.Errorf("result = %+v error = %v, want database error", result, err)
	}
}

func refundableTestPayment() sqlc.Payment {
	return sqlc.Payment{
		ID:                     17,
		BookingID:              29,
		Provider:               FakeProvider,
		ProviderPaymentID:      pgtype.Text{String: "fake-payment-17", Valid: true},
		ProviderIdempotencyKey: "booking:29:payment:create",
		AmountMinor:            250_000,
		Currency:               "VND",
		Status:                 "refund_pending",
	}
}

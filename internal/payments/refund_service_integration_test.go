package payments

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestRefundServiceRetriesProviderFailureAndMarksRefunded(t *testing.T) {
	pool := newPaymentWebhookIntegrationPool(t)
	bookingID, paymentID, providerPaymentID := insertPaymentWebhookFixture(t, pool, 15*time.Minute)
	if _, err := pool.Exec(
		context.Background(),
		"UPDATE bookings SET status = 'cancelled' WHERE id = $1",
		bookingID,
	); err != nil {
		t.Fatalf("prepare cancelled booking: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE payments
		SET status = 'refund_pending', provider_payment_id = $1, paid_at = now()
		WHERE id = $2`, providerPaymentID, paymentID); err != nil {
		t.Fatalf("prepare refund-pending payment: %v", err)
	}

	gateway := &recordingGateway{provider: FakeProvider, refundErr: ErrGatewayUnavailable}
	service := NewRefundService(sqlc.New(pool), gateway, 100)
	result, err := service.ProcessPendingRefunds(context.Background())
	if err == nil || result.Failed != 1 || result.Refunded != 0 {
		t.Fatalf("failed attempt result = %+v error = %v", result, err)
	}
	var status, failureCode, failureMessage string
	if err := pool.QueryRow(context.Background(), `
		SELECT status, failure_code, failure_message FROM payments WHERE id = $1`, paymentID,
	).Scan(&status, &failureCode, &failureMessage); err != nil {
		t.Fatalf("read failed refund: %v", err)
	}
	if status != "refund_pending" || failureCode != "refund_provider_error" || failureMessage == "" {
		t.Errorf("failed refund state = {%q %q %q}", status, failureCode, failureMessage)
	}

	gateway.mu.Lock()
	gateway.refundErr = nil
	gateway.mu.Unlock()
	result, err = service.ProcessPendingRefunds(context.Background())
	if err != nil || result.Refunded != 1 || result.Failed != 0 {
		t.Fatalf("retry result = %+v error = %v", result, err)
	}
	var failureCodeCleared, failureMessageCleared, refundedAtPresent bool
	if err := pool.QueryRow(context.Background(), `
		SELECT status, failure_code IS NULL, failure_message IS NULL, refunded_at IS NOT NULL
		FROM payments WHERE id = $1`, paymentID,
	).Scan(&status, &failureCodeCleared, &failureMessageCleared, &refundedAtPresent); err != nil {
		t.Fatalf("read refunded payment: %v", err)
	}
	if status != "refunded" || !failureCodeCleared || !failureMessageCleared || !refundedAtPresent {
		t.Errorf("refunded state = {%q code-cleared:%t message-cleared:%t refunded-at:%t}", status, failureCodeCleared, failureMessageCleared, refundedAtPresent)
	}
	assertEmailOutboxEvents(t, pool, bookingID, "payment_refunded")
}

func TestRefundServiceRollsBackRefundWhenEmailEnqueueFails(t *testing.T) {
	pool := newPaymentWebhookIntegrationPool(t)
	bookingID, paymentID, providerPaymentID := insertPaymentWebhookFixture(t, pool, 15*time.Minute)
	if _, err := pool.Exec(
		context.Background(),
		"UPDATE bookings SET status = 'cancelled' WHERE id = $1",
		bookingID,
	); err != nil {
		t.Fatalf("prepare cancelled booking: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE payments
		SET status = 'refund_pending', provider_payment_id = $1, paid_at = now()
		WHERE id = $2`, providerPaymentID, paymentID); err != nil {
		t.Fatalf("prepare refund-pending payment: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		`ALTER TABLE email_outbox
		 ADD CONSTRAINT email_outbox_test_reject_refund
		 CHECK (event_type <> 'payment_refunded')`,
	); err != nil {
		t.Fatalf("install rejecting refund-email constraint: %v", err)
	}

	service := NewRefundService(
		sqlc.New(pool),
		&recordingGateway{provider: FakeProvider},
		100,
	)
	result, err := service.ProcessPendingRefunds(context.Background())
	if err == nil || !strings.Contains(err.Error(), "finalize payment refund") {
		t.Fatalf("ProcessPendingRefunds() error = %v, want finalization error", err)
	}
	if result != (RefundBatchResult{Attempted: 1, Failed: 1}) {
		t.Errorf("result = %+v, want failed refund", result)
	}

	var status string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT status FROM payments WHERE id = $1",
		paymentID,
	).Scan(&status); err != nil {
		t.Fatalf("read rolled-back payment: %v", err)
	}
	if status != "refund_pending" {
		t.Errorf("payment status = %q, want refund_pending", status)
	}
	assertEmailOutboxEvents(t, pool, bookingID)
}

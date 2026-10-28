package payments

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
		SET status = 'refund_pending', provider_payment_id = $1, paid_at = now(),
		    refund_available_at = now()
		WHERE id = $2`, providerPaymentID, paymentID); err != nil {
		t.Fatalf("prepare refund-pending payment: %v", err)
	}

	gateway := &recordingGateway{provider: FakeProvider, refundErr: ErrGatewayUnavailable}
	service := NewRefundService(sqlc.New(pool), gateway, 5*time.Minute, 100, time.Minute)
	result, err := service.ProcessPendingRefunds(context.Background())
	if err == nil || result.Failed != 1 || result.Refunded != 0 {
		t.Fatalf("failed attempt result = %+v error = %v", result, err)
	}
	var status, failureCode, failureMessage string
	var attempts int32
	var availableInFuture, lockCleared bool
	if err := pool.QueryRow(context.Background(), `
		SELECT status, failure_code, failure_message, refund_attempts,
		       refund_available_at > now(), refund_locked_at IS NULL
		FROM payments WHERE id = $1`, paymentID,
	).Scan(&status, &failureCode, &failureMessage, &attempts, &availableInFuture, &lockCleared); err != nil {
		t.Fatalf("read failed refund: %v", err)
	}
	if status != "refund_pending" || failureCode != "refund_provider_error" || failureMessage == "" ||
		attempts != 1 || !availableInFuture || !lockCleared {
		t.Errorf("failed refund state = {%q %q %q attempts:%d future:%t unlocked:%t}", status, failureCode, failureMessage, attempts, availableInFuture, lockCleared)
	}
	assertRefundAttemptHistory(t, pool, paymentID, []string{"1:failed:refund_provider_error:true:true"})

	gateway.mu.Lock()
	gateway.refundErr = nil
	gateway.mu.Unlock()
	result, err = service.ProcessPendingRefunds(context.Background())
	if err != nil || result != (RefundBatchResult{}) {
		t.Fatalf("early retry result = %+v error = %v, want no claimed payment", result, err)
	}
	if _, err := pool.Exec(
		context.Background(),
		"UPDATE payments SET refund_available_at = now() WHERE id = $1",
		paymentID,
	); err != nil {
		t.Fatalf("make refund retry available: %v", err)
	}
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
	assertRefundAttemptHistory(t, pool, paymentID, []string{
		"2:succeeded::false:true",
		"1:failed:refund_provider_error:true:true",
	})
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
		SET status = 'refund_pending', provider_payment_id = $1, paid_at = now(),
		    refund_available_at = now()
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
		5*time.Minute,
		100,
		time.Minute,
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
	assertRefundAttemptHistory(t, pool, paymentID, []string{"1:succeeded::false:true"})
	assertEmailOutboxEvents(t, pool, bookingID)
}

func assertRefundAttemptHistory(
	t *testing.T,
	pool interface {
		Query(context.Context, string, ...any) (pgx.Rows, error)
	},
	paymentID int64,
	want []string,
) {
	t.Helper()
	rows, err := pool.Query(
		context.Background(),
		`SELECT attempt_number, status, COALESCE(failure_code, ''),
		        failure_message IS NOT NULL, completed_at >= started_at
		 FROM payment_refund_attempts
		 WHERE payment_id = $1
		 ORDER BY attempt_number DESC`,
		paymentID,
	)
	if err != nil {
		t.Fatalf("query refund attempt history: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var attempt int32
		var status, failureCode string
		var hasFailureMessage, timestampsOrdered bool
		if err := rows.Scan(
			&attempt, &status, &failureCode, &hasFailureMessage, &timestampsOrdered,
		); err != nil {
			t.Fatalf("scan refund attempt history: %v", err)
		}
		got = append(got, fmt.Sprintf(
			"%d:%s:%s:%t:%t",
			attempt, status, failureCode, hasFailureMessage, timestampsOrdered,
		))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read refund attempt history: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("refund attempt history = %v, want %v", got, want)
	}
}

func TestRefundServiceClaimsPaymentOnlyOnceAcrossWorkers(t *testing.T) {
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
		SET status = 'refund_pending', provider_payment_id = $2, paid_at = now(),
		    refund_available_at = now()
		WHERE id = $1`, paymentID, providerPaymentID); err != nil {
		t.Fatalf("prepare refund-pending payment: %v", err)
	}

	gateway := &blockingRefundGateway{
		recordingGateway: &recordingGateway{provider: FakeProvider},
		started:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	first := NewRefundService(sqlc.New(pool), gateway, 5*time.Minute, 100, time.Minute)
	second := NewRefundService(sqlc.New(pool), gateway, 5*time.Minute, 100, time.Minute)
	type processResult struct {
		batch RefundBatchResult
		err   error
	}
	firstResult := make(chan processResult, 1)
	go func() {
		batch, err := first.ProcessPendingRefunds(context.Background())
		firstResult <- processResult{batch: batch, err: err}
	}()

	select {
	case <-gateway.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first worker did not reach the refund provider")
	}
	secondBatch, err := second.ProcessPendingRefunds(context.Background())
	if err != nil || secondBatch != (RefundBatchResult{}) {
		t.Errorf("second worker result = %+v error = %v, want no claim", secondBatch, err)
	}
	close(gateway.release)
	completed := <-firstResult
	if completed.err != nil || completed.batch != (RefundBatchResult{Attempted: 1, Refunded: 1}) {
		t.Fatalf("first worker result = %+v error = %v", completed.batch, completed.err)
	}
	if calls := gateway.recordedRefundCalls(); len(calls) != 1 {
		t.Fatalf("provider refund calls = %d, want 1", len(calls))
	}
}

func TestRefundServiceRecoversStaleLease(t *testing.T) {
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
		SET status = 'refund_pending', provider_payment_id = $2, paid_at = now(),
		    refund_attempts = 1,
		    refund_available_at = now() - interval '10 minutes',
		    refund_locked_at = now() - interval '10 minutes'
		WHERE id = $1`, paymentID, providerPaymentID); err != nil {
		t.Fatalf("prepare stale refund lease: %v", err)
	}

	gateway := &recordingGateway{provider: FakeProvider}
	result, err := NewRefundService(
		sqlc.New(pool), gateway, 5*time.Minute, 100, time.Minute,
	).ProcessPendingRefunds(context.Background())
	if err != nil || result != (RefundBatchResult{Attempted: 1, Refunded: 1}) {
		t.Fatalf("stale lease result = %+v error = %v", result, err)
	}

	var status string
	var attempts int32
	var availableCleared, lockCleared bool
	if err := pool.QueryRow(context.Background(), `
		SELECT status, refund_attempts,
		       refund_available_at IS NULL, refund_locked_at IS NULL
		FROM payments
		WHERE id = $1`, paymentID,
	).Scan(&status, &attempts, &availableCleared, &lockCleared); err != nil {
		t.Fatalf("read recovered refund: %v", err)
	}
	if status != "refunded" || attempts != 2 || !availableCleared || !lockCleared {
		t.Errorf("recovered refund = {status:%q attempts:%d available-cleared:%t lock-cleared:%t}", status, attempts, availableCleared, lockCleared)
	}
	if calls := gateway.recordedRefundCalls(); len(calls) != 1 {
		t.Errorf("provider refund calls = %d, want 1", len(calls))
	}
}

type blockingRefundGateway struct {
	*recordingGateway
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *blockingRefundGateway) RefundPayment(ctx context.Context, request RefundRequest) error {
	g.once.Do(func() { close(g.started) })
	select {
	case <-g.release:
		return g.recordingGateway.RefundPayment(ctx, request)
	case <-ctx.Done():
		return ctx.Err()
	}
}

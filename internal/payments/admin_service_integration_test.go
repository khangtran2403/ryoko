package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestAdminPaymentServicePostgreSQLIntegration(t *testing.T) {
	pool := newPaymentWebhookIntegrationPool(t)
	ctx := context.Background()

	oldBookingID, oldPaymentID, _ := insertPaymentWebhookFixture(t, pool, 15*time.Minute)
	newBookingID, newPaymentID, newProviderPaymentID := insertPaymentWebhookFixture(t, pool, 15*time.Minute)
	if _, err := pool.Exec(ctx, `
		UPDATE payments
		SET created_at = now() - interval '2 hours', updated_at = now() - interval '2 hours'
		WHERE id = $1`, oldPaymentID); err != nil {
		t.Fatalf("age old payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE payments
		SET status = 'refund_pending', paid_at = now(),
		    failure_code = 'refund_provider_error', failure_message = 'provider unavailable',
		    refund_attempts = 3, refund_available_at = now() + interval '5 minutes'
		WHERE id = $1`, newPaymentID); err != nil {
		t.Fatalf("prepare refund-pending payment: %v", err)
	}

	service := NewAdminService(sqlc.New(pool))
	status, provider := "refund_pending", "fake"
	result, err := service.ListPayments(ctx, ListAdminPaymentsInput{
		Status: &status, Provider: &provider, BookingID: &newBookingID, Page: 1, PageSize: 1,
	})
	if err != nil {
		t.Fatalf("ListPayments() error = %v", err)
	}
	if len(result.Payments) != 1 || result.Payments[0].ID != newPaymentID {
		t.Fatalf("payments = %+v, want payment %d", result.Payments, newPaymentID)
	}
	payment := result.Payments[0]
	if payment.BookingID != newBookingID || payment.Provider != "fake" ||
		payment.ProviderPaymentID.String != newProviderPaymentID || payment.Status != "refund_pending" ||
		payment.CustomerEmail == "" || payment.HotelName != "Webhook Hotel" ||
		payment.FailureCode.String != "refund_provider_error" || payment.RefundAttempts != 3 ||
		!payment.RefundAvailableAt.Valid || payment.RefundLockedAt.Valid {
		t.Errorf("payment context = %+v", payment)
	}
	if result.Pagination.HasMore {
		t.Error("filtered single-row result unexpectedly has more pages")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO payment_refund_attempts (
			payment_id, attempt_number, status, failure_code, failure_message,
			started_at, completed_at
		)
		VALUES
			($1, 1, 'failed', 'refund_provider_error', 'first failure', now() - interval '3 minutes', now() - interval '2 minutes'),
			($1, 2, 'failed', 'refund_provider_error', 'second failure', now() - interval '2 minutes', now() - interval '1 minute'),
			($1, 3, 'succeeded', NULL, NULL, now() - interval '1 minute', now())`, newPaymentID); err != nil {
		t.Fatalf("insert refund attempt history: %v", err)
	}
	attempts, err := service.ListRefundAttempts(ctx, ListAdminRefundAttemptsInput{
		PaymentID: newPaymentID, Page: 1, PageSize: 2,
	})
	if err != nil {
		t.Fatalf("ListRefundAttempts() error = %v", err)
	}
	if len(attempts.Attempts) != 2 || attempts.Attempts[0].AttemptNumber != 3 ||
		attempts.Attempts[0].Status != "succeeded" || attempts.Attempts[1].AttemptNumber != 2 ||
		!attempts.Pagination.HasMore {
		t.Errorf("first refund-attempt page = %+v", attempts)
	}
	attempts, err = service.ListRefundAttempts(ctx, ListAdminRefundAttemptsInput{
		PaymentID: newPaymentID, Page: 2, PageSize: 2,
	})
	if err != nil || len(attempts.Attempts) != 1 || attempts.Attempts[0].AttemptNumber != 1 ||
		attempts.Pagination.HasMore {
		t.Errorf("second refund-attempt page = %+v; error = %v", attempts, err)
	}

	retried, err := service.RetryFailedRefund(ctx, newPaymentID)
	if err != nil {
		t.Fatalf("RetryFailedRefund() error = %v", err)
	}
	if retried.ID != newPaymentID || retried.Status != "refund_pending" ||
		retried.FailureCode.Valid || retried.FailureMessage.Valid ||
		!retried.RefundAvailableAt.Valid || retried.RefundLockedAt.Valid ||
		retried.RefundAttempts != 3 {
		t.Errorf("retried payment = %+v", retried)
	}
	var immediatelyAvailable, failureCleared, unlocked bool
	if err := pool.QueryRow(ctx, `
		SELECT refund_available_at <= now(),
		       failure_code IS NULL AND failure_message IS NULL,
		       refund_locked_at IS NULL
		FROM payments WHERE id = $1`, newPaymentID,
	).Scan(&immediatelyAvailable, &failureCleared, &unlocked); err != nil {
		t.Fatalf("read retried refund: %v", err)
	}
	if !immediatelyAvailable || !failureCleared || !unlocked {
		t.Errorf("retry state = {available:%t failure-cleared:%t unlocked:%t}", immediatelyAvailable, failureCleared, unlocked)
	}
	_, err = service.RetryFailedRefund(ctx, newPaymentID)
	if !errors.Is(err, ErrAdminRefundNotRetryable) {
		t.Fatalf("second retry error = %v, want ErrAdminRefundNotRetryable", err)
	}

	all, err := service.ListPayments(ctx, ListAdminPaymentsInput{Page: 1, PageSize: 1})
	if err != nil {
		t.Fatalf("ListPayments(all) error = %v", err)
	}
	if len(all.Payments) != 1 || all.Payments[0].ID != newPaymentID || !all.Pagination.HasMore {
		t.Errorf("newest-first page = %+v, want payment %d and has_more", all, newPaymentID)
	}

	got, err := service.GetPayment(ctx, oldPaymentID)
	if err != nil {
		t.Fatalf("GetPayment() error = %v", err)
	}
	if got.ID != oldPaymentID || got.BookingID != oldBookingID || got.CustomerName != "Webhook User" {
		t.Errorf("GetPayment() = %+v", got)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE payments
		SET status = 'refund_pending', paid_at = now(),
		    failure_code = 'refund_provider_error', failure_message = 'provider unavailable',
		    refund_available_at = now(), refund_locked_at = now()
		WHERE id = $1`, oldPaymentID); err != nil {
		t.Fatalf("prepare actively leased refund: %v", err)
	}
	_, err = service.RetryFailedRefund(ctx, oldPaymentID)
	if !errors.Is(err, ErrAdminRefundInProgress) {
		t.Fatalf("active refund retry error = %v, want ErrAdminRefundInProgress", err)
	}

	_, err = service.GetPayment(ctx, 999999)
	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("missing payment error = %v, want ErrPaymentNotFound", err)
	}
	_, err = service.RetryFailedRefund(ctx, 999999)
	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("missing refund retry error = %v, want ErrPaymentNotFound", err)
	}
	_, err = service.ListRefundAttempts(ctx, ListAdminRefundAttemptsInput{
		PaymentID: 999999, Page: 1, PageSize: 20,
	})
	if !errors.Is(err, ErrPaymentNotFound) {
		t.Fatalf("missing refund history error = %v, want ErrPaymentNotFound", err)
	}
}

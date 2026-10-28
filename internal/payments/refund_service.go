package payments

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

const (
	maxRefundRetryDelay = 24 * time.Hour
	maxRefundExponent   = 10
)

var ErrRefundLeaseLost = errors.New("payment refund lease changed concurrently")

type refundQueries interface {
	ClaimRefundPendingPayments(context.Context, sqlc.ClaimRefundPendingPaymentsParams) ([]sqlc.Payment, error)
	RecordSuccessfulPaymentRefundAttempt(context.Context, sqlc.RecordSuccessfulPaymentRefundAttemptParams) (int64, error)
	MarkPaymentRefunded(context.Context, sqlc.MarkPaymentRefundedParams) (sqlc.MarkPaymentRefundedRow, error)
	SchedulePaymentRefundRetry(context.Context, sqlc.SchedulePaymentRefundRetryParams) (int64, error)
}

type RefundBatchResult struct {
	Attempted int64
	Refunded  int64
	Failed    int64
}

type RefundService struct {
	queries        refundQueries
	gateway        Gateway
	leaseDuration  time.Duration
	batchSize      int32
	baseRetryDelay time.Duration
}

func NewRefundService(
	queries refundQueries,
	gateway Gateway,
	leaseDuration time.Duration,
	batchSize int32,
	baseRetryDelay time.Duration,
) *RefundService {
	if queries == nil {
		panic("payment refund queries must not be nil")
	}
	if gateway == nil {
		panic("payment refund gateway must not be nil")
	}
	if leaseDuration < time.Second || leaseDuration/time.Second > math.MaxInt32 {
		panic("payment refund lease duration must be from one second to 2147483647 seconds")
	}
	if batchSize < 1 || batchSize > MaxExpirationBatchSize {
		panic("payment refund batch size must be between 1 and 1000")
	}
	if baseRetryDelay < time.Second || baseRetryDelay > maxRefundRetryDelay {
		panic("payment refund base retry delay must be between one second and 24 hours")
	}
	return &RefundService{
		queries: queries, gateway: gateway, leaseDuration: leaseDuration,
		batchSize: batchSize, baseRetryDelay: baseRetryDelay,
	}
}

func (s *RefundService) ProcessPendingRefunds(ctx context.Context) (RefundBatchResult, error) {
	pending, err := s.queries.ClaimRefundPendingPayments(
		ctx,
		sqlc.ClaimRefundPendingPaymentsParams{
			LeaseSeconds: int32(s.leaseDuration / time.Second),
			BatchSize:    s.batchSize,
		},
	)
	if err != nil {
		return RefundBatchResult{}, fmt.Errorf("claim refund-pending payments: %w", err)
	}

	result := RefundBatchResult{Attempted: int64(len(pending))}
	var processingErrors []error
	provider := strings.TrimSpace(s.gateway.Provider())
	for _, payment := range pending {
		if err := s.refundPayment(ctx, provider, payment); err != nil {
			result.Failed++
			processingErrors = append(processingErrors, fmt.Errorf("refund payment %d: %w", payment.ID, err))
			continue
		}
		result.Refunded++
	}
	return result, errors.Join(processingErrors...)
}

func (s *RefundService) refundPayment(ctx context.Context, provider string, payment sqlc.Payment) error {
	if payment.Provider != provider {
		return s.recordFailure(ctx, payment, fmt.Errorf("provider mismatch: payment uses %q, gateway uses %q", payment.Provider, provider))
	}
	providerPaymentID := strings.TrimSpace(payment.ProviderPaymentID.String)
	if !payment.ProviderPaymentID.Valid || providerPaymentID == "" {
		return s.recordFailure(ctx, payment, ErrProviderPaymentIDRequired)
	}

	err := s.gateway.RefundPayment(ctx, RefundRequest{
		PaymentID:         payment.ID,
		ProviderPaymentID: providerPaymentID,
		AmountMinor:       payment.AmountMinor,
		Currency:          payment.Currency,
		IdempotencyKey:    fmt.Sprintf("booking:%d:payment:refund", payment.BookingID),
	})
	if err != nil {
		return s.recordFailure(ctx, payment, fmt.Errorf("provider refund: %w", err))
	}
	rows, err := s.queries.RecordSuccessfulPaymentRefundAttempt(
		ctx,
		sqlc.RecordSuccessfulPaymentRefundAttemptParams{
			PaymentID: payment.ID, RefundLockedAt: payment.RefundLockedAt,
		},
	)
	if err != nil {
		return fmt.Errorf("record successful payment refund attempt: %w", err)
	}
	if rows != 1 {
		return ErrRefundLeaseLost
	}

	_, err = s.queries.MarkPaymentRefunded(ctx, sqlc.MarkPaymentRefundedParams{
		PaymentID:      payment.ID,
		RefundLockedAt: payment.RefundLockedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRefundLeaseLost
	}
	if err != nil {
		return fmt.Errorf("finalize payment refund: %w", err)
	}
	return nil
}

func (s *RefundService) recordFailure(ctx context.Context, payment sqlc.Payment, cause error) error {
	message := strings.TrimSpace(cause.Error())
	if len(message) > 1000 {
		message = message[:1000]
	}
	delay := s.retryDelay(payment.RefundAttempts)
	rows, err := s.queries.SchedulePaymentRefundRetry(
		ctx,
		sqlc.SchedulePaymentRefundRetryParams{
			FailureMessage:    pgtype.Text{String: message, Valid: true},
			RetryAfterSeconds: int32(delay / time.Second),
			PaymentID:         payment.ID,
			RefundLockedAt:    payment.RefundLockedAt,
		},
	)
	if err != nil {
		return errors.Join(cause, fmt.Errorf("schedule payment refund retry: %w", err))
	}
	if rows != 1 {
		return errors.Join(cause, ErrRefundLeaseLost)
	}
	return cause
}

func (s *RefundService) retryDelay(attempts int32) time.Duration {
	exponent := attempts - 1
	if exponent < 0 {
		exponent = 0
	}
	if exponent > maxRefundExponent {
		exponent = maxRefundExponent
	}
	multiplier := time.Duration(1) << exponent
	if s.baseRetryDelay > maxRefundRetryDelay/multiplier {
		return maxRefundRetryDelay
	}
	delay := s.baseRetryDelay * multiplier
	if delay > maxRefundRetryDelay {
		return maxRefundRetryDelay
	}
	return delay
}

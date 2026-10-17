package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type refundQueries interface {
	ListRefundPendingPayments(context.Context, int32) ([]sqlc.Payment, error)
	MarkPaymentRefunded(context.Context, int64) (sqlc.Payment, error)
	RecordPaymentRefundFailure(context.Context, sqlc.RecordPaymentRefundFailureParams) error
}

type RefundBatchResult struct {
	Attempted int64
	Refunded  int64
	Failed    int64
}

type RefundService struct {
	queries   refundQueries
	gateway   Gateway
	batchSize int32
}

func NewRefundService(queries refundQueries, gateway Gateway, batchSize int32) *RefundService {
	if queries == nil {
		panic("payment refund queries must not be nil")
	}
	if gateway == nil {
		panic("payment refund gateway must not be nil")
	}
	if batchSize < 1 || batchSize > MaxExpirationBatchSize {
		panic("payment refund batch size must be between 1 and 1000")
	}
	return &RefundService{queries: queries, gateway: gateway, batchSize: batchSize}
}

func (s *RefundService) ProcessPendingRefunds(ctx context.Context) (RefundBatchResult, error) {
	pending, err := s.queries.ListRefundPendingPayments(ctx, s.batchSize)
	if err != nil {
		return RefundBatchResult{}, fmt.Errorf("list refund-pending payments: %w", err)
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
		return s.recordFailure(ctx, payment.ID, fmt.Errorf("provider mismatch: payment uses %q, gateway uses %q", payment.Provider, provider))
	}
	providerPaymentID := strings.TrimSpace(payment.ProviderPaymentID.String)
	if !payment.ProviderPaymentID.Valid || providerPaymentID == "" {
		return s.recordFailure(ctx, payment.ID, ErrProviderPaymentIDRequired)
	}

	err := s.gateway.RefundPayment(ctx, RefundRequest{
		PaymentID:         payment.ID,
		ProviderPaymentID: providerPaymentID,
		AmountMinor:       payment.AmountMinor,
		Currency:          payment.Currency,
		IdempotencyKey:    fmt.Sprintf("booking:%d:payment:refund", payment.BookingID),
	})
	if err != nil {
		return s.recordFailure(ctx, payment.ID, fmt.Errorf("provider refund: %w", err))
	}

	if _, err := s.queries.MarkPaymentRefunded(ctx, payment.ID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("mark payment refunded: %w", err)
	}
	return nil
}

func (s *RefundService) recordFailure(ctx context.Context, paymentID int64, cause error) error {
	message := cause.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	err := s.queries.RecordPaymentRefundFailure(ctx, sqlc.RecordPaymentRefundFailureParams{
		FailureMessage: pgtype.Text{String: message, Valid: true},
		PaymentID:      paymentID,
	})
	if err != nil {
		return errors.Join(cause, fmt.Errorf("record refund failure: %w", err))
	}
	return cause
}

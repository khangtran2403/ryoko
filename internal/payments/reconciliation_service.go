package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type reconciliationQueries interface {
	ListPaymentsForReconciliation(context.Context,sqlc.ListPaymentsForReconciliationParams) ([]sqlc.Payment, error)
	TouchPendingPaymentReconciliation(context.Context, int64) (int64, error)
}

type reconciliationEventProcessor interface {
	ProcessWebhook(context.Context, WebhookEvent) (WebhookResult, error)
}

type ReconciliationBatchResult struct {
	Checked    int64
	Reconciled int64
	Unchanged  int64
	Failed     int64
}

type ReconciliationService struct {
	queries    reconciliationQueries
	gateway    Gateway
	processor  reconciliationEventProcessor
	staleAfter time.Duration
	batchSize  int32
	now        func() time.Time
}

func NewReconciliationService(
	queries reconciliationQueries,
	gateway Gateway,
	processor reconciliationEventProcessor,
	staleAfter time.Duration,
	batchSize int32,
) *ReconciliationService {
	if queries == nil {
		panic("payment reconciliation queries must not be nil")
	}
	if gateway == nil {
		panic("payment reconciliation gateway must not be nil")
	}
	if processor == nil {
		panic("payment reconciliation event processor must not be nil")
	}
	if staleAfter <= 0 {
		panic("payment reconciliation stale duration must be positive")
	}
	if batchSize < 1 || batchSize > MaxExpirationBatchSize {
		panic("payment reconciliation batch size must be between 1 and 1000")
	}
	return &ReconciliationService{
		queries:    queries,
		gateway:    gateway,
		processor:  processor,
		staleAfter: staleAfter,
		batchSize:  batchSize,
		now:        time.Now,
	}
}

func (s *ReconciliationService) ReconcilePendingPayments(ctx context.Context) (ReconciliationBatchResult, error) {
	provider := strings.TrimSpace(s.gateway.Provider())
	if provider == "" {
		return ReconciliationBatchResult{}, ErrProviderMismatch
	}

	payments, err := s.queries.ListPaymentsForReconciliation(
		ctx,
		sqlc.ListPaymentsForReconciliationParams{
			Provider: provider,
			UpdatedBefore: pgtype.Timestamptz{
				Time:  s.now().UTC().Add(-s.staleAfter),
				Valid: true,
			},
			BatchSize: s.batchSize,
		},
	)
	if err != nil {
		return ReconciliationBatchResult{}, fmt.Errorf("list payments for reconciliation: %w", err)
	}

	result := ReconciliationBatchResult{Checked: int64(len(payments))}
	var processingErrors []error
	for _, payment := range payments {
		outcome, err := s.reconcilePayment(ctx, provider, payment)
		if err != nil {
			result.Failed++
			processingErrors = append(
				processingErrors,
				fmt.Errorf("reconcile payment %d: %w", payment.ID, err),
			)
			continue
		}
		if outcome {
			result.Reconciled++
		} else {
			result.Unchanged++
		}
	}
	return result, errors.Join(processingErrors...)
}

func (s *ReconciliationService) reconcilePayment(
	ctx context.Context,
	provider string,
	payment sqlc.Payment,
) (bool, error) {
	if payment.Provider != provider {
		return false, ErrProviderMismatch
	}
	providerPaymentID := strings.TrimSpace(payment.ProviderPaymentID.String)
	if !payment.ProviderPaymentID.Valid || providerPaymentID == "" {
		return false, ErrProviderPaymentIDRequired
	}

	rows, err := s.queries.TouchPendingPaymentReconciliation(ctx, payment.ID)
	if err != nil {
		return false, fmt.Errorf("mark reconciliation attempt: %w", err)
	}
	if rows == 0 {
		return false, nil
	}

	providerPayment, err := s.gateway.LookupPayment(ctx, providerPaymentID)
	if err != nil {
		return false, fmt.Errorf("lookup provider payment: %w", err)
	}
	event, terminal, err := reconciliationEvent(provider, providerPaymentID, providerPayment)
	if err != nil {
		return false, err
	}
	if !terminal {
		return false, nil
	}

	processed, err := s.processor.ProcessWebhook(ctx, event)
	if err != nil {
		return false, fmt.Errorf("apply provider payment state: %w", err)
	}
	return processed.Processed, nil
}

func reconciliationEvent(
	provider string,
	providerPaymentID string,
	payment ProviderPayment,
) (WebhookEvent, bool, error) {
	event := WebhookEvent{
		Provider:          provider,
		ProviderEventID:   "reconciliation:" + providerPaymentID + ":" + string(payment.State),
		ProviderPaymentID: providerPaymentID,
	}
	switch payment.State {
	case ProviderPaymentPending:
		return WebhookEvent{}, false, nil
	case ProviderPaymentSucceeded:
		event.Type = PaymentSucceededEvent
	case ProviderPaymentFailed:
		event.Type = PaymentFailedEvent
		event.FailureCode = strings.TrimSpace(payment.FailureCode)
		if event.FailureCode == "" {
			event.FailureCode = "payment_failed"
		}
		event.FailureMessage = strings.TrimSpace(payment.FailureMessage)
	case ProviderPaymentCanceled:
		event.Type = PaymentCanceledEvent
	default:
		return WebhookEvent{}, false, ErrUnsupportedPaymentState
	}
	return event, true, nil
}

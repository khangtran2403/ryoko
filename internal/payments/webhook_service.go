package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidWebhookEvent          = errors.New("invalid payment webhook event")
	ErrWebhookPaymentNotFound       = errors.New("webhook payment not found")
	ErrWebhookPaymentNotProcessable = errors.New("webhook payment cannot be processed")
)

type WebhookResult struct {
	PaymentID        int64
	BookingID        int64
	Processed        bool
	Duplicate        bool
	Ignored          bool
	AlreadyProcessed bool
	RefundQueued     bool
	Retryable        bool
}

type WebhookService struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

func NewWebhookService(pool *pgxpool.Pool, queries *sqlc.Queries) *WebhookService {
	if pool == nil {
		panic("payment webhook pool must not be nil")
	}
	if queries == nil {
		panic("payment webhook queries must not be nil")
	}
	return &WebhookService{pool: pool, queries: queries}
}

func (s *WebhookService) ProcessWebhook(
	ctx context.Context,
	event WebhookEvent,
) (WebhookResult, error) {
	event.Provider = strings.TrimSpace(event.Provider)
	event.ProviderEventID = strings.TrimSpace(event.ProviderEventID)
	event.Type = strings.TrimSpace(event.Type)
	event.ProviderPaymentID = strings.TrimSpace(event.ProviderPaymentID)
	event.FailureCode = strings.TrimSpace(event.FailureCode)
	event.FailureMessage = strings.TrimSpace(event.FailureMessage)
	if event.Provider == "" ||
		event.ProviderEventID == "" ||
		event.Type == "" ||
		event.ProviderPaymentID == "" {
		return WebhookResult{}, ErrInvalidWebhookEvent
	}
	if event.Type == PaymentFailedEvent &&
		(event.FailureCode == "" || len(event.FailureCode) > 100 || len(event.FailureMessage) > 1000) {
		return WebhookResult{}, ErrInvalidWebhookEvent
	}
	if (event.Type == PaymentSucceededEvent || event.Type == PaymentCanceledEvent) &&
		(event.FailureCode != "" || event.FailureMessage != "") {
		return WebhookResult{}, ErrInvalidWebhookEvent
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return WebhookResult{}, fmt.Errorf("begin payment webhook transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	_, err = qtx.ClaimPaymentWebhookEvent(ctx, sqlc.ClaimPaymentWebhookEventParams{
		Provider:        event.Provider,
		ProviderEventID: event.ProviderEventID,
		EventType:       event.Type,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookResult{Duplicate: true}, nil
	}
	if err != nil {
		return WebhookResult{}, fmt.Errorf("claim payment webhook event: %w", err)
	}

	if event.Type != PaymentSucceededEvent &&
		event.Type != PaymentFailedEvent &&
		event.Type != PaymentCanceledEvent {
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, fmt.Errorf("commit ignored payment webhook: %w", err)
		}
		return WebhookResult{Ignored: true}, nil
	}

	paymentCandidate, err := qtx.GetPaymentByProviderID(
		ctx,
		sqlc.GetPaymentByProviderIDParams{
			Provider: event.Provider,
			ProviderPaymentID: pgtype.Text{
				String: event.ProviderPaymentID,
				Valid:  true,
			},
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookResult{}, ErrWebhookPaymentNotFound
	}
	if err != nil {
		return WebhookResult{}, fmt.Errorf("find webhook payment: %w", err)
	}

	// All booking/payment transitions lock in this order. Keeping one order
	// prevents a success webhook and the expiration worker from deadlocking.
	lockedBooking, err := qtx.GetBookingForAdminCancellation(ctx, paymentCandidate.BookingID)
	if err != nil {
		return WebhookResult{}, fmt.Errorf("lock webhook booking: %w", err)
	}
	payment, err := qtx.GetPaymentByBookingIDForUpdate(ctx, paymentCandidate.BookingID)
	if err != nil {
		return WebhookResult{}, fmt.Errorf("lock webhook payment: %w", err)
	}
	if payment.ID != paymentCandidate.ID ||
		payment.Provider != event.Provider ||
		!payment.ProviderPaymentID.Valid ||
		payment.ProviderPaymentID.String != event.ProviderPaymentID {
		return WebhookResult{}, ErrWebhookPaymentNotProcessable
	}

	result := WebhookResult{
		PaymentID: payment.ID,
		BookingID: payment.BookingID,
	}
	if event.Type == PaymentCanceledEvent {
		if payment.Status != "pending" {
			if err := tx.Commit(ctx); err != nil {
				return WebhookResult{}, fmt.Errorf("commit repeated payment cancellation: %w", err)
			}
			result.AlreadyProcessed = true
			return result, nil
		}
		if err := cancelPendingBookingAndPayment(
			ctx,
			qtx,
			lockedBooking,
			payment,
			"Payment canceled by provider",
		); err != nil {
			return WebhookResult{}, fmt.Errorf("cancel provider-canceled payment: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, fmt.Errorf("commit payment cancellation: %w", err)
		}
		result.Processed = true
		return result, nil
	}
	if event.Type == PaymentFailedEvent {
		if payment.Status != "pending" {
			if err := tx.Commit(ctx); err != nil {
				return WebhookResult{}, fmt.Errorf("commit stale payment failure: %w", err)
			}
			result.AlreadyProcessed = true
			return result, nil
		}
		_, err := qtx.RecordPaymentFailure(ctx, sqlc.RecordPaymentFailureParams{
			FailureCode:    pgtype.Text{String: event.FailureCode, Valid: true},
			FailureMessage: nullableWebhookText(event.FailureMessage),
			PaymentID:      payment.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return WebhookResult{}, ErrWebhookPaymentNotProcessable
		}
		if err != nil {
			return WebhookResult{}, fmt.Errorf("record payment failure: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, fmt.Errorf("commit payment failure: %w", err)
		}
		result.Processed = true
		result.Retryable = true
		return result, nil
	}
	if payment.Status == "succeeded" || payment.Status == "refund_pending" || payment.Status == "refunded" {
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, fmt.Errorf("commit repeated payment success: %w", err)
		}
		result.AlreadyProcessed = true
		return result, nil
	}
	if payment.Status == "expired" {
		if lockedBooking.Status != "cancelled" {
			return WebhookResult{}, ErrWebhookPaymentNotProcessable
		}
		if _, err := qtx.MarkExpiredPaymentRefundPending(ctx, payment.ID); errors.Is(err, pgx.ErrNoRows) {
			return WebhookResult{}, ErrWebhookPaymentNotProcessable
		} else if err != nil {
			return WebhookResult{}, fmt.Errorf("queue late payment refund: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WebhookResult{}, fmt.Errorf("commit late payment refund: %w", err)
		}
		result.Processed = true
		result.RefundQueued = true
		return result, nil
	}
	if payment.Status != "pending" {
		return WebhookResult{}, ErrWebhookPaymentNotProcessable
	}

	booking, err := qtx.ConfirmPendingBooking(ctx, payment.BookingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return WebhookResult{}, ErrWebhookPaymentNotProcessable
	}
	if err != nil {
		return WebhookResult{}, fmt.Errorf("confirm paid booking: %w", err)
	}

	if _, err := qtx.MarkPaymentSucceeded(ctx, payment.ID); errors.Is(err, pgx.ErrNoRows) {
		return WebhookResult{}, ErrWebhookPaymentNotProcessable
	} else if err != nil {
		return WebhookResult{}, fmt.Errorf("mark payment succeeded: %w", err)
	}

	_, err = qtx.CreateBookingStatusHistory(ctx, sqlc.CreateBookingStatusHistoryParams{
		BookingID:  booking.ID,
		FromStatus: pgtype.Text{String: "pending_payment", Valid: true},
		ToStatus:   "confirmed",
		Reason:     pgtype.Text{String: "Payment confirmed", Valid: true},
	})
	if err != nil {
		return WebhookResult{}, fmt.Errorf("record payment confirmation history: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return WebhookResult{}, fmt.Errorf("commit payment webhook transaction: %w", err)
	}
	result.Processed = true
	return result, nil
}

func nullableWebhookText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

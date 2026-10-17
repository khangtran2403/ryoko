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

var (
	ErrInvalidBookingID                  = errors.New("booking ID must be positive")
	ErrInvalidUserID                     = errors.New("user ID must be positive")
	ErrPaymentNotAvailable               = errors.New("payment not available")
	ErrPaymentNotFound                   = errors.New("payment not found")
	ErrProviderMismatch                  = errors.New("payment provider mismatch")
	ErrProviderPaymentIDRequired         = errors.New("provider payment ID is required")
	ErrProviderPaymentIDMismatch         = errors.New("provider payment ID mismatch")
	ErrCheckoutURLOrClientSecretRequired = errors.New("exactly one of checkout URL or client secret is required")
)

type paymentQueries interface {
	GetInitiatablePaymentForUser(context.Context, sqlc.GetInitiatablePaymentForUserParams) (sqlc.Payment, error)
	GetPaymentByBookingIDForUser(context.Context, sqlc.GetPaymentByBookingIDForUserParams) (sqlc.Payment, error)
	SetPaymentProviderID(context.Context, sqlc.SetPaymentProviderIDParams) (sqlc.Payment, error)
	GetPaymentByBookingID(context.Context, int64) (sqlc.Payment, error)
}

func (s *Service) GetPaymentStatus(ctx context.Context, userID int64, bookingID int64) (sqlc.Payment, error) {
	if bookingID <= 0 {
		return sqlc.Payment{}, ErrInvalidBookingID
	}
	if userID <= 0 {
		return sqlc.Payment{}, ErrInvalidUserID
	}
	payment, err := s.queries.GetPaymentByBookingIDForUser(ctx, sqlc.GetPaymentByBookingIDForUserParams{
		BookingID: bookingID,
		UserID:    userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Payment{}, ErrPaymentNotFound
	}
	if err != nil {
		return sqlc.Payment{}, fmt.Errorf("get payment status: %w", err)
	}
	return payment, nil
}

type Service struct {
	queries paymentQueries
	gateway Gateway
}

type InitiatePaymentResult struct {
	Payment sqlc.Payment
	Session PaymentSession
}

func NewService(queries paymentQueries, gateway Gateway) *Service {
	if queries == nil {
		panic("payment queries must not be nil")
	}
	if gateway == nil {
		panic("payment gateway must not be nil")
	}
	if strings.TrimSpace(gateway.Provider()) == "" {
		panic("payment gateway provider must not be empty")
	}

	return &Service{
		queries: queries,
		gateway: gateway,
	}
}

func (s *Service) InitiatePayment(ctx context.Context, userID int64, bookingID int64) (InitiatePaymentResult, error) {
	if bookingID <= 0 {
		return InitiatePaymentResult{}, ErrInvalidBookingID
	}
	if userID <= 0 {
		return InitiatePaymentResult{}, ErrInvalidUserID
	}

	payment, err := s.queries.GetInitiatablePaymentForUser(
		ctx,
		sqlc.GetInitiatablePaymentForUserParams{
			BookingID: bookingID,
			UserID:    userID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return InitiatePaymentResult{}, ErrPaymentNotAvailable
	}
	if err != nil {
		return InitiatePaymentResult{}, fmt.Errorf("get initiatable payment for user: %w", err)
	}

	provider := strings.TrimSpace(s.gateway.Provider())
	if payment.Provider != provider {
		return InitiatePaymentResult{}, ErrProviderMismatch
	}

	session, err := s.gateway.CreatePayment(ctx, CreatePaymentRequest{
		PaymentID:      payment.ID,
		BookingID:      payment.BookingID,
		AmountMinor:    payment.AmountMinor,
		Currency:       payment.Currency,
		IdempotencyKey: payment.ProviderIdempotencyKey,
	})
	if err != nil {
		return InitiatePaymentResult{}, fmt.Errorf("create payment: %w", err)
	}

	session.ProviderPaymentID = strings.TrimSpace(session.ProviderPaymentID)
	session.CheckoutURL = strings.TrimSpace(session.CheckoutURL)
	session.ClientSecret = strings.TrimSpace(session.ClientSecret)

	if session.ProviderPaymentID == "" {
		return InitiatePaymentResult{}, ErrProviderPaymentIDRequired
	}

	hasCheckoutURL := session.CheckoutURL != ""
	hasClientSecret := session.ClientSecret != ""
	if hasCheckoutURL == hasClientSecret {
		return InitiatePaymentResult{}, ErrCheckoutURLOrClientSecretRequired
	}

	if payment.ProviderPaymentID.Valid {
		if strings.TrimSpace(payment.ProviderPaymentID.String) != session.ProviderPaymentID {
			return InitiatePaymentResult{}, ErrProviderPaymentIDMismatch
		}

		return InitiatePaymentResult{
			Payment: payment,
			Session: session,
		}, nil
	}

	payment, err = s.queries.SetPaymentProviderID(ctx, sqlc.SetPaymentProviderIDParams{
		ProviderPaymentID: pgtype.Text{
			String: session.ProviderPaymentID,
			Valid:  true,
		},
		PaymentID: payment.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		payment, err = s.queries.GetPaymentByBookingID(ctx, bookingID)
		if err != nil {
			return InitiatePaymentResult{}, fmt.Errorf("reload payment after concurrent update: %w", err)
		}
		if payment.Provider != provider ||
			!payment.ProviderPaymentID.Valid ||
			strings.TrimSpace(payment.ProviderPaymentID.String) != session.ProviderPaymentID {
			return InitiatePaymentResult{}, ErrProviderPaymentIDMismatch
		}
	} else if err != nil {
		return InitiatePaymentResult{}, fmt.Errorf("set payment provider ID: %w", err)
	}

	return InitiatePaymentResult{
		Payment: payment,
		Session: session,
	}, nil
}

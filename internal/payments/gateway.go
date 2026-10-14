package payments

import (
	"context"
	"errors"
)

var (
	ErrGatewayUnavailable = errors.New("payment gateway unavailable")
	ErrInvalidCurrency    = errors.New("unsupported payment currency")
)

type CreatePaymentRequest struct {
	PaymentID      int64
	BookingID      int64
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
}

type PaymentSession struct {
	ProviderPaymentID string
	CheckoutURL       string
	ClientSecret      string
}

type RefundRequest struct {
	PaymentID         int64
	ProviderPaymentID string
	AmountMinor       int64
	Currency          string
	IdempotencyKey    string
}

type Gateway interface {
	Provider() string
	CreatePayment(ctx context.Context, request CreatePaymentRequest) (PaymentSession, error)
	RefundPayment(ctx context.Context, request RefundRequest) error
}

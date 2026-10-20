package payments

import (
	"context"
	"errors"
)

var (
	ErrGatewayUnavailable      = errors.New("payment gateway unavailable")
	ErrInvalidAPIKey           = errors.New("invalid payment API key")
	ErrInvalidCurrency         = errors.New("unsupported payment currency")
	ErrInvalidCheckoutURL      = errors.New("invalid payment checkout URL")
	ErrInvalidRequest          = errors.New("invalid payment gateway request")
	ErrInvalidWebhookSecret    = errors.New("invalid payment webhook secret")
	ErrInvalidWebhookPayload   = errors.New("invalid payment webhook payload")
	ErrInvalidWebhookSignature = errors.New("invalid payment webhook signature")
	ErrRefundNotSucceeded      = errors.New("payment refund not succeeded")
	ErrUnsupportedPaymentState = errors.New("unsupported provider payment state")
)

const (
	PaymentSucceededEvent        = "payment.succeeded"
	PaymentFailedEvent           = "payment.failed"
	PaymentCanceledEvent         = "payment.canceled"
	StripeProvider               = "stripe"
	FakeWebhookSignatureHeader   = "X-Ryoko-Signature"
	StripeWebhookSignatureHeader = "Stripe-Signature"
)

type ProviderPaymentState string

const (
	ProviderPaymentPending   ProviderPaymentState = "pending"
	ProviderPaymentSucceeded ProviderPaymentState = "succeeded"
	ProviderPaymentFailed    ProviderPaymentState = "failed"
	ProviderPaymentCanceled  ProviderPaymentState = "canceled"
)

type ProviderPayment struct {
	State          ProviderPaymentState
	FailureCode    string
	FailureMessage string
}

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

type WebhookEvent struct {
	Provider          string
	ProviderEventID   string
	Type              string
	ProviderPaymentID string
	FailureCode       string
	FailureMessage    string
}

type Gateway interface {
	Provider() string
	CreatePayment(ctx context.Context, request CreatePaymentRequest) (PaymentSession, error)
	LookupPayment(ctx context.Context, providerPaymentID string) (ProviderPayment, error)
	RefundPayment(ctx context.Context, request RefundRequest) error
}

type WebhookVerifier interface {
	WebhookSignatureHeader() string
	VerifyWebhook(payload []byte, signature string) (WebhookEvent, error)
}

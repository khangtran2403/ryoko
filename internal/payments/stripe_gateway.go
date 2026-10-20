package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

type stripePaymentIntentClient interface {
	Create(context.Context, *stripe.PaymentIntentCreateParams) (*stripe.PaymentIntent, error)
	Retrieve(context.Context, string, *stripe.PaymentIntentRetrieveParams) (*stripe.PaymentIntent, error)
}

type stripeRefundCreator interface {
	Create(context.Context, *stripe.RefundCreateParams) (*stripe.Refund, error)
}

type StripeGateway struct {
	paymentIntents stripePaymentIntentClient
	refunds        stripeRefundCreator
	webhookSecret  string
}

var _ Gateway = (*StripeGateway)(nil)
var _ WebhookVerifier = (*StripeGateway)(nil)

func NewStripeGateway(secretKey string, webhookSecret string) (*StripeGateway, error) {
	secretKey = strings.TrimSpace(secretKey)
	if secretKey == "" {
		return nil, ErrInvalidAPIKey
	}
	webhookSecret = strings.TrimSpace(webhookSecret)
	if webhookSecret == "" {
		return nil, ErrInvalidWebhookSecret
	}

	client := stripe.NewClient(secretKey)
	return newStripeGateway(client.V1PaymentIntents, client.V1Refunds, webhookSecret), nil
}

func newStripeGateway(
	paymentIntents stripePaymentIntentClient,
	refunds stripeRefundCreator,
	webhookSecret string,
) *StripeGateway {
	return &StripeGateway{
		paymentIntents: paymentIntents,
		refunds:        refunds,
		webhookSecret:  webhookSecret,
	}
}

func (g *StripeGateway) LookupPayment(
	ctx context.Context,
	providerPaymentID string,
) (ProviderPayment, error) {
	providerPaymentID = strings.TrimSpace(providerPaymentID)
	if providerPaymentID == "" {
		return ProviderPayment{}, ErrInvalidRequest
	}

	paymentIntent, err := g.paymentIntents.Retrieve(
		ctx,
		providerPaymentID,
		&stripe.PaymentIntentRetrieveParams{},
	)
	if err != nil {
		return ProviderPayment{}, fmt.Errorf(
			"%w: retrieve Stripe PaymentIntent: %w",
			ErrGatewayUnavailable,
			err,
		)
	}
	if paymentIntent == nil || strings.TrimSpace(paymentIntent.ID) != providerPaymentID {
		return ProviderPayment{}, fmt.Errorf(
			"%w: Stripe returned an invalid PaymentIntent",
			ErrGatewayUnavailable,
		)
	}

	switch paymentIntent.Status {
	case stripe.PaymentIntentStatusSucceeded:
		return ProviderPayment{State: ProviderPaymentSucceeded}, nil
	case stripe.PaymentIntentStatusCanceled:
		return ProviderPayment{State: ProviderPaymentCanceled}, nil
	case stripe.PaymentIntentStatusRequiresPaymentMethod:
		if paymentIntent.LastPaymentError == nil {
			return ProviderPayment{State: ProviderPaymentPending}, nil
		}
		failureCode, failureMessage := stripePaymentFailure(paymentIntent.LastPaymentError)
		return ProviderPayment{
			State:          ProviderPaymentFailed,
			FailureCode:    failureCode,
			FailureMessage: failureMessage,
		}, nil
	case stripe.PaymentIntentStatusProcessing,
		stripe.PaymentIntentStatusRequiresAction,
		stripe.PaymentIntentStatusRequiresCapture,
		stripe.PaymentIntentStatusRequiresConfirmation:
		return ProviderPayment{State: ProviderPaymentPending}, nil
	default:
		return ProviderPayment{}, fmt.Errorf(
			"%w: Stripe PaymentIntent %s has status %q",
			ErrUnsupportedPaymentState,
			providerPaymentID,
			paymentIntent.Status,
		)
	}
}

func (g *StripeGateway) Provider() string {
	return StripeProvider
}

func (g *StripeGateway) WebhookSignatureHeader() string {
	return StripeWebhookSignatureHeader
}

func (g *StripeGateway) CreatePayment(
	ctx context.Context,
	request CreatePaymentRequest,
) (PaymentSession, error) {
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.PaymentID <= 0 ||
		request.BookingID <= 0 ||
		request.AmountMinor <= 0 ||
		request.IdempotencyKey == "" {
		return PaymentSession{}, ErrInvalidRequest
	}
	if !supportedPaymentCurrency(request.Currency) {
		return PaymentSession{}, ErrInvalidCurrency
	}

	params := &stripe.PaymentIntentCreateParams{
		Amount:   stripe.Int64(request.AmountMinor),
		Currency: stripe.String(strings.ToLower(request.Currency)),
		Description: stripe.String(fmt.Sprintf(
			"Ryoko booking %d",
			request.BookingID,
		)),
		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(true),
		},
		Metadata: map[string]string{
			"booking_id": strconv.FormatInt(request.BookingID, 10),
			"payment_id": strconv.FormatInt(request.PaymentID, 10),
		},
	}
	params.SetIdempotencyKey(request.IdempotencyKey)

	paymentIntent, err := g.paymentIntents.Create(ctx, params)
	if err != nil {
		return PaymentSession{}, fmt.Errorf(
			"%w: create Stripe PaymentIntent: %w",
			ErrGatewayUnavailable,
			err,
		)
	}
	if paymentIntent == nil ||
		strings.TrimSpace(paymentIntent.ID) == "" ||
		strings.TrimSpace(paymentIntent.ClientSecret) == "" {
		return PaymentSession{}, fmt.Errorf(
			"%w: Stripe returned an incomplete PaymentIntent",
			ErrGatewayUnavailable,
		)
	}

	return PaymentSession{
		ProviderPaymentID: strings.TrimSpace(paymentIntent.ID),
		ClientSecret:      strings.TrimSpace(paymentIntent.ClientSecret),
	}, nil
}

func (g *StripeGateway) RefundPayment(ctx context.Context, request RefundRequest) error {
	request.ProviderPaymentID = strings.TrimSpace(request.ProviderPaymentID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.PaymentID <= 0 ||
		request.ProviderPaymentID == "" ||
		request.AmountMinor <= 0 ||
		request.IdempotencyKey == "" {
		return ErrInvalidRequest
	}
	if !supportedPaymentCurrency(request.Currency) {
		return ErrInvalidCurrency
	}

	params := &stripe.RefundCreateParams{
		Amount:        stripe.Int64(request.AmountMinor),
		PaymentIntent: stripe.String(request.ProviderPaymentID),
		Metadata: map[string]string{
			"payment_id": strconv.FormatInt(request.PaymentID, 10),
		},
	}
	params.SetIdempotencyKey(request.IdempotencyKey)

	refund, err := g.refunds.Create(ctx, params)
	if err != nil {
		return fmt.Errorf(
			"%w: create Stripe refund: %w",
			ErrGatewayUnavailable,
			err,
		)
	}
	if refund == nil || strings.TrimSpace(refund.ID) == "" {
		return fmt.Errorf("%w: Stripe returned an incomplete refund", ErrGatewayUnavailable)
	}
	if refund.Status != stripe.RefundStatusSucceeded {
		return fmt.Errorf("%w: Stripe refund %s has status %q", ErrRefundNotSucceeded, refund.ID, refund.Status)
	}

	return nil
}

func (g *StripeGateway) VerifyWebhook(payload []byte, signature string) (WebhookEvent, error) {
	event, err := webhook.ConstructEvent(payload, signature, g.webhookSecret)
	if err != nil {
		if isStripeSignatureError(err) {
			return WebhookEvent{}, fmt.Errorf("%w: %v", ErrInvalidWebhookSignature, err)
		}
		return WebhookEvent{}, fmt.Errorf("%w: %v", ErrInvalidWebhookPayload, err)
	}

	providerEventID := strings.TrimSpace(event.ID)
	if providerEventID == "" {
		return WebhookEvent{}, ErrInvalidWebhookPayload
	}

	switch event.Type {
	case stripe.EventTypePaymentIntentSucceeded:
		paymentIntent, err := decodeStripePaymentIntent(event.Data.Raw)
		if err != nil {
			return WebhookEvent{}, err
		}
		return WebhookEvent{
			Provider:          StripeProvider,
			ProviderEventID:   providerEventID,
			Type:              PaymentSucceededEvent,
			ProviderPaymentID: paymentIntent.ID,
		}, nil

	case stripe.EventTypePaymentIntentPaymentFailed:
		paymentIntent, err := decodeStripePaymentIntent(event.Data.Raw)
		if err != nil {
			return WebhookEvent{}, err
		}
		failureCode, failureMessage := stripePaymentFailure(paymentIntent.LastPaymentError)
		return WebhookEvent{
			Provider:          StripeProvider,
			ProviderEventID:   providerEventID,
			Type:              PaymentFailedEvent,
			ProviderPaymentID: paymentIntent.ID,
			FailureCode:       failureCode,
			FailureMessage:    failureMessage,
		}, nil

	case stripe.EventTypePaymentIntentCanceled:
		paymentIntent, err := decodeStripePaymentIntent(event.Data.Raw)
		if err != nil {
			return WebhookEvent{}, err
		}
		return WebhookEvent{
			Provider:          StripeProvider,
			ProviderEventID:   providerEventID,
			Type:              PaymentCanceledEvent,
			ProviderPaymentID: paymentIntent.ID,
		}, nil

	default:
		providerObjectID, err := decodeStripeObjectID(event.Data.Raw)
		if err != nil {
			return WebhookEvent{}, err
		}
		return WebhookEvent{
			Provider:          StripeProvider,
			ProviderEventID:   providerEventID,
			Type:              string(event.Type),
			ProviderPaymentID: providerObjectID,
		}, nil
	}
}

func isStripeSignatureError(err error) bool {
	return errors.Is(err, webhook.ErrInvalidHeader) ||
		errors.Is(err, webhook.ErrNoValidSignature) ||
		errors.Is(err, webhook.ErrNotSigned) ||
		errors.Is(err, webhook.ErrTooOld) ||
		errors.Is(err, webhook.ErrEmptySecret)
}

func decodeStripePaymentIntent(raw json.RawMessage) (stripe.PaymentIntent, error) {
	var paymentIntent stripe.PaymentIntent
	if err := json.Unmarshal(raw, &paymentIntent); err != nil {
		return stripe.PaymentIntent{}, fmt.Errorf("%w: decode Stripe PaymentIntent", ErrInvalidWebhookPayload)
	}
	paymentIntent.ID = strings.TrimSpace(paymentIntent.ID)
	if paymentIntent.ID == "" {
		return stripe.PaymentIntent{}, ErrInvalidWebhookPayload
	}
	return paymentIntent, nil
}

func decodeStripeObjectID(raw json.RawMessage) (string, error) {
	var object struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("%w: decode Stripe event object", ErrInvalidWebhookPayload)
	}
	object.ID = strings.TrimSpace(object.ID)
	if object.ID == "" {
		return "", ErrInvalidWebhookPayload
	}
	return object.ID, nil
}

func stripePaymentFailure(stripeError *stripe.Error) (string, string) {
	if stripeError == nil {
		return "payment_failed", ""
	}

	failureCode := strings.TrimSpace(string(stripeError.DeclineCode))
	if failureCode == "" {
		failureCode = strings.TrimSpace(string(stripeError.Code))
	}
	if failureCode == "" {
		failureCode = "payment_failed"
	}
	failureMessage := strings.TrimSpace(stripeError.Msg)

	return truncateStripeWebhookField(failureCode, 100), truncateStripeWebhookField(failureMessage, 1000)
}

func truncateStripeWebhookField(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end]
}

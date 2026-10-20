package payments

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

const stripeWebhookTestSecret = "whsec_stripe_webhook_test_secret"

type recordingStripePaymentIntentCreator struct {
	ctx    context.Context
	params *stripe.PaymentIntentCreateParams
	result *stripe.PaymentIntent
	err    error
}

type recordingStripeRefundCreator struct {
	ctx    context.Context
	params *stripe.RefundCreateParams
	result *stripe.Refund
	err    error
}

func (c *recordingStripeRefundCreator) Create(
	ctx context.Context,
	params *stripe.RefundCreateParams,
) (*stripe.Refund, error) {
	c.ctx = ctx
	c.params = params
	return c.result, c.err
}

func (c *recordingStripePaymentIntentCreator) Create(
	ctx context.Context,
	params *stripe.PaymentIntentCreateParams,
) (*stripe.PaymentIntent, error) {
	c.ctx = ctx
	c.params = params
	return c.result, c.err
}

func TestNewStripeGatewayRejectsMissingSecretKey(t *testing.T) {
	t.Parallel()

	for _, secretKey := range []string{"", "   "} {
		gateway, err := NewStripeGateway(secretKey, stripeWebhookTestSecret)
		if !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("NewStripeGateway(%q) error = %v, want ErrInvalidAPIKey", secretKey, err)
		}
		if gateway != nil {
			t.Fatalf("NewStripeGateway(%q) gateway = %+v, want nil", secretKey, gateway)
		}
	}
}

func TestNewStripeGatewayRejectsMissingWebhookSecret(t *testing.T) {
	t.Parallel()

	for _, webhookSecret := range []string{"", "   "} {
		gateway, err := NewStripeGateway("sk_test_example", webhookSecret)
		if !errors.Is(err, ErrInvalidWebhookSecret) {
			t.Fatalf("NewStripeGateway() error = %v, want ErrInvalidWebhookSecret", err)
		}
		if gateway != nil {
			t.Fatalf("NewStripeGateway() gateway = %+v, want nil", gateway)
		}
	}
}

func TestStripeGatewayCreatePayment(t *testing.T) {
	t.Parallel()

	creator := &recordingStripePaymentIntentCreator{
		result: &stripe.PaymentIntent{
			ID:           " pi_123 ",
			ClientSecret: " pi_123_secret_example ",
		},
	}
	gateway := newStripeGateway(creator, nil, stripeWebhookTestSecret)
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("request"), "value")
	request := CreatePaymentRequest{
		PaymentID:      17,
		BookingID:      29,
		AmountMinor:    250_000,
		Currency:       "VND",
		IdempotencyKey: " booking:29:payment:create ",
	}

	session, err := gateway.CreatePayment(ctx, request)
	if err != nil {
		t.Fatalf("CreatePayment() error = %v", err)
	}
	if session.ProviderPaymentID != "pi_123" ||
		session.ClientSecret != "pi_123_secret_example" ||
		session.CheckoutURL != "" {
		t.Errorf("CreatePayment() session = %+v", session)
	}
	if gateway.Provider() != StripeProvider {
		t.Errorf("Provider() = %q, want %q", gateway.Provider(), StripeProvider)
	}
	if creator.ctx != ctx {
		t.Error("CreatePayment() did not pass the caller context to Stripe")
	}
	if creator.params == nil {
		t.Fatal("Stripe Create() params = nil")
	}
	if creator.params.Amount == nil || *creator.params.Amount != request.AmountMinor {
		t.Errorf("Stripe amount = %v, want %d", creator.params.Amount, request.AmountMinor)
	}
	if creator.params.Currency == nil || *creator.params.Currency != "vnd" {
		t.Errorf("Stripe currency = %v, want vnd", creator.params.Currency)
	}
	if creator.params.Description == nil || *creator.params.Description != "Ryoko booking 29" {
		t.Errorf("Stripe description = %v", creator.params.Description)
	}
	if creator.params.AutomaticPaymentMethods == nil ||
		creator.params.AutomaticPaymentMethods.Enabled == nil ||
		!*creator.params.AutomaticPaymentMethods.Enabled {
		t.Error("Stripe automatic payment methods are not enabled")
	}
	if creator.params.Metadata["booking_id"] != "29" || creator.params.Metadata["payment_id"] != "17" {
		t.Errorf("Stripe metadata = %+v", creator.params.Metadata)
	}
	if creator.params.IdempotencyKey == nil || *creator.params.IdempotencyKey != "booking:29:payment:create" {
		t.Errorf("Stripe idempotency key = %v", creator.params.IdempotencyKey)
	}
}

func TestStripeGatewayRejectsInvalidCreateRequests(t *testing.T) {
	t.Parallel()

	valid := CreatePaymentRequest{
		PaymentID:      17,
		BookingID:      29,
		AmountMinor:    250_000,
		Currency:       "VND",
		IdempotencyKey: "booking:29:payment:create",
	}
	tests := []struct {
		name    string
		mutate  func(*CreatePaymentRequest)
		wantErr error
	}{
		{name: "invalid payment ID", mutate: func(r *CreatePaymentRequest) { r.PaymentID = 0 }, wantErr: ErrInvalidRequest},
		{name: "invalid booking ID", mutate: func(r *CreatePaymentRequest) { r.BookingID = 0 }, wantErr: ErrInvalidRequest},
		{name: "invalid amount", mutate: func(r *CreatePaymentRequest) { r.AmountMinor = 0 }, wantErr: ErrInvalidRequest},
		{name: "missing idempotency key", mutate: func(r *CreatePaymentRequest) { r.IdempotencyKey = " " }, wantErr: ErrInvalidRequest},
		{name: "unsupported currency", mutate: func(r *CreatePaymentRequest) { r.Currency = "EUR" }, wantErr: ErrInvalidCurrency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			creator := &recordingStripePaymentIntentCreator{}
			gateway := newStripeGateway(creator, nil, stripeWebhookTestSecret)
			request := valid
			tt.mutate(&request)

			_, err := gateway.CreatePayment(context.Background(), request)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreatePayment() error = %v, want %v", err, tt.wantErr)
			}
			if creator.params != nil {
				t.Error("Stripe Create() was called for an invalid request")
			}
		})
	}
}

func TestStripeGatewayMapsCreateFailure(t *testing.T) {
	t.Parallel()

	stripeErr := errors.New("Stripe API unavailable")
	gateway := newStripeGateway(&recordingStripePaymentIntentCreator{err: stripeErr}, nil, stripeWebhookTestSecret)
	_, err := gateway.CreatePayment(context.Background(), validStripeCreatePaymentRequest())
	if !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("CreatePayment() error = %v, want ErrGatewayUnavailable", err)
	}
	if !errors.Is(err, stripeErr) {
		t.Fatalf("CreatePayment() error = %v, want wrapped Stripe error", err)
	}
}

func TestStripeGatewayRejectsIncompleteCreateResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result *stripe.PaymentIntent
	}{
		{name: "nil PaymentIntent"},
		{name: "missing ID", result: &stripe.PaymentIntent{ClientSecret: "pi_secret"}},
		{name: "missing client secret", result: &stripe.PaymentIntent{ID: "pi_123"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gateway := newStripeGateway(&recordingStripePaymentIntentCreator{result: tt.result}, nil, stripeWebhookTestSecret)
			_, err := gateway.CreatePayment(context.Background(), validStripeCreatePaymentRequest())
			if !errors.Is(err, ErrGatewayUnavailable) {
				t.Fatalf("CreatePayment() error = %v, want ErrGatewayUnavailable", err)
			}
		})
	}
}

func TestStripeGatewayRefundPayment(t *testing.T) {
	t.Parallel()

	creator := &recordingStripeRefundCreator{
		result: &stripe.Refund{ID: "re_123", Status: stripe.RefundStatusSucceeded},
	}
	gateway := newStripeGateway(nil, creator, stripeWebhookTestSecret)
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("request"), "value")
	request := validStripeRefundRequest()
	request.ProviderPaymentID = " pi_123 "
	request.IdempotencyKey = " booking:29:payment:refund "

	if err := gateway.RefundPayment(ctx, request); err != nil {
		t.Fatalf("RefundPayment() error = %v", err)
	}
	if creator.ctx != ctx {
		t.Error("RefundPayment() did not pass the caller context to Stripe")
	}
	if creator.params == nil {
		t.Fatal("Stripe refund Create() params = nil")
	}
	if creator.params.Amount == nil || *creator.params.Amount != request.AmountMinor {
		t.Errorf("Stripe refund amount = %v, want %d", creator.params.Amount, request.AmountMinor)
	}
	if creator.params.PaymentIntent == nil || *creator.params.PaymentIntent != "pi_123" {
		t.Errorf("Stripe refund PaymentIntent = %v, want pi_123", creator.params.PaymentIntent)
	}
	if creator.params.Metadata["payment_id"] != "17" {
		t.Errorf("Stripe refund metadata = %+v", creator.params.Metadata)
	}
	if creator.params.IdempotencyKey == nil || *creator.params.IdempotencyKey != "booking:29:payment:refund" {
		t.Errorf("Stripe refund idempotency key = %v", creator.params.IdempotencyKey)
	}
}

func TestStripeGatewayRejectsInvalidRefundRequests(t *testing.T) {
	t.Parallel()

	valid := validStripeRefundRequest()
	tests := []struct {
		name    string
		mutate  func(*RefundRequest)
		wantErr error
	}{
		{name: "invalid payment ID", mutate: func(r *RefundRequest) { r.PaymentID = 0 }, wantErr: ErrInvalidRequest},
		{name: "missing provider payment ID", mutate: func(r *RefundRequest) { r.ProviderPaymentID = " " }, wantErr: ErrInvalidRequest},
		{name: "invalid amount", mutate: func(r *RefundRequest) { r.AmountMinor = 0 }, wantErr: ErrInvalidRequest},
		{name: "missing idempotency key", mutate: func(r *RefundRequest) { r.IdempotencyKey = " " }, wantErr: ErrInvalidRequest},
		{name: "unsupported currency", mutate: func(r *RefundRequest) { r.Currency = "EUR" }, wantErr: ErrInvalidCurrency},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			creator := &recordingStripeRefundCreator{}
			gateway := newStripeGateway(nil, creator, stripeWebhookTestSecret)
			request := valid
			tt.mutate(&request)

			err := gateway.RefundPayment(context.Background(), request)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RefundPayment() error = %v, want %v", err, tt.wantErr)
			}
			if creator.params != nil {
				t.Error("Stripe refund Create() was called for an invalid request")
			}
		})
	}
}

func TestStripeGatewayMapsRefundFailure(t *testing.T) {
	t.Parallel()

	stripeErr := errors.New("Stripe API unavailable")
	gateway := newStripeGateway(nil, &recordingStripeRefundCreator{err: stripeErr}, stripeWebhookTestSecret)
	err := gateway.RefundPayment(context.Background(), validStripeRefundRequest())
	if !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("RefundPayment() error = %v, want ErrGatewayUnavailable", err)
	}
	if !errors.Is(err, stripeErr) {
		t.Fatalf("RefundPayment() error = %v, want wrapped Stripe error", err)
	}
}

func TestStripeGatewayRejectsIncompleteOrUnfinishedRefund(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		result  *stripe.Refund
		wantErr error
	}{
		{name: "nil refund", wantErr: ErrGatewayUnavailable},
		{name: "missing refund ID", result: &stripe.Refund{Status: stripe.RefundStatusSucceeded}, wantErr: ErrGatewayUnavailable},
		{name: "pending refund", result: &stripe.Refund{ID: "re_pending", Status: stripe.RefundStatusPending}, wantErr: ErrRefundNotSucceeded},
		{name: "failed refund", result: &stripe.Refund{ID: "re_failed", Status: stripe.RefundStatusFailed}, wantErr: ErrRefundNotSucceeded},
		{name: "canceled refund", result: &stripe.Refund{ID: "re_canceled", Status: stripe.RefundStatusCanceled}, wantErr: ErrRefundNotSucceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gateway := newStripeGateway(nil, &recordingStripeRefundCreator{result: tt.result}, stripeWebhookTestSecret)
			err := gateway.RefundPayment(context.Background(), validStripeRefundRequest())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RefundPayment() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestStripeGatewayVerifySucceededWebhook(t *testing.T) {
	t.Parallel()

	gateway := newStripeGateway(nil, nil, stripeWebhookTestSecret)
	signed := signedStripeWebhook(t, stripe.EventTypePaymentIntentSucceeded, map[string]any{
		"id":     "pi_123",
		"object": "payment_intent",
	})

	event, err := gateway.VerifyWebhook(signed.Payload, signed.Header)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	want := WebhookEvent{
		Provider:          StripeProvider,
		ProviderEventID:   "evt_test_123",
		Type:              PaymentSucceededEvent,
		ProviderPaymentID: "pi_123",
	}
	if event != want {
		t.Errorf("VerifyWebhook() event = %+v, want %+v", event, want)
	}
	if gateway.WebhookSignatureHeader() != StripeWebhookSignatureHeader {
		t.Errorf("WebhookSignatureHeader() = %q", gateway.WebhookSignatureHeader())
	}
}

func TestStripeGatewayVerifyFailedWebhook(t *testing.T) {
	t.Parallel()

	gateway := newStripeGateway(nil, nil, stripeWebhookTestSecret)
	signed := signedStripeWebhook(t, stripe.EventTypePaymentIntentPaymentFailed, map[string]any{
		"id":     "pi_failed",
		"object": "payment_intent",
		"last_payment_error": map[string]any{
			"code":         "card_declined",
			"decline_code": "insufficient_funds",
			"message":      "Your card has insufficient funds.",
		},
	})

	event, err := gateway.VerifyWebhook(signed.Payload, signed.Header)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	want := WebhookEvent{
		Provider:          StripeProvider,
		ProviderEventID:   "evt_test_123",
		Type:              PaymentFailedEvent,
		ProviderPaymentID: "pi_failed",
		FailureCode:       "insufficient_funds",
		FailureMessage:    "Your card has insufficient funds.",
	}
	if event != want {
		t.Errorf("VerifyWebhook() event = %+v, want %+v", event, want)
	}
}

func TestStripeGatewayVerifyFailedWebhookUsesFallbackCode(t *testing.T) {
	t.Parallel()

	gateway := newStripeGateway(nil, nil, stripeWebhookTestSecret)
	signed := signedStripeWebhook(t, stripe.EventTypePaymentIntentPaymentFailed, map[string]any{
		"id":     "pi_failed",
		"object": "payment_intent",
	})

	event, err := gateway.VerifyWebhook(signed.Payload, signed.Header)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if event.FailureCode != "payment_failed" || event.FailureMessage != "" {
		t.Errorf("failure = {%q %q}, want fallback code", event.FailureCode, event.FailureMessage)
	}
}

func TestStripeGatewayVerifyIgnoresUnrelatedEventSafely(t *testing.T) {
	t.Parallel()

	gateway := newStripeGateway(nil, nil, stripeWebhookTestSecret)
	signed := signedStripeWebhook(t, stripe.EventTypeCustomerCreated, map[string]any{
		"id":     "cus_123",
		"object": "customer",
	})

	event, err := gateway.VerifyWebhook(signed.Payload, signed.Header)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if event.Provider != StripeProvider ||
		event.ProviderEventID != "evt_test_123" ||
		event.Type != string(stripe.EventTypeCustomerCreated) ||
		event.ProviderPaymentID != "cus_123" {
		t.Errorf("VerifyWebhook() event = %+v", event)
	}
}

func TestStripeGatewayVerifyWebhookRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	gateway := newStripeGateway(nil, nil, stripeWebhookTestSecret)
	valid := signedStripeWebhook(t, stripe.EventTypePaymentIntentSucceeded, map[string]any{
		"id":     "pi_123",
		"object": "payment_intent",
	})
	wrongSecret := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: valid.Payload,
		Secret:  "whsec_wrong_secret",
	})
	malformedPayload := []byte(`{"id":`)
	malformed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: malformedPayload,
		Secret:  stripeWebhookTestSecret,
	})
	missingObjectID := signedStripeWebhook(t, stripe.EventTypePaymentIntentSucceeded, map[string]any{
		"object": "payment_intent",
	})

	tests := []struct {
		name      string
		payload   []byte
		signature string
		wantErr   error
	}{
		{name: "missing signature", payload: valid.Payload, wantErr: ErrInvalidWebhookSignature},
		{name: "wrong signature", payload: valid.Payload, signature: wrongSecret.Header, wantErr: ErrInvalidWebhookSignature},
		{name: "malformed signed payload", payload: malformed.Payload, signature: malformed.Header, wantErr: ErrInvalidWebhookPayload},
		{name: "missing PaymentIntent ID", payload: missingObjectID.Payload, signature: missingObjectID.Header, wantErr: ErrInvalidWebhookPayload},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := gateway.VerifyWebhook(tt.payload, tt.signature)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("VerifyWebhook() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTruncateStripeWebhookFieldPreservesUTF8(t *testing.T) {
	t.Parallel()

	value := strings.Repeat("đ", 600)
	truncated := truncateStripeWebhookField(value, 1000)
	if len(truncated) > 1000 {
		t.Fatalf("truncated value has %d bytes, want at most 1000", len(truncated))
	}
	if !utf8.ValidString(truncated) {
		t.Fatal("truncated value is not valid UTF-8")
	}
}

func validStripeCreatePaymentRequest() CreatePaymentRequest {
	return CreatePaymentRequest{
		PaymentID:      17,
		BookingID:      29,
		AmountMinor:    250_000,
		Currency:       "VND",
		IdempotencyKey: "booking:29:payment:create",
	}
}

func validStripeRefundRequest() RefundRequest {
	return RefundRequest{
		PaymentID:         17,
		ProviderPaymentID: "pi_123",
		AmountMinor:       250_000,
		Currency:          "VND",
		IdempotencyKey:    "booking:29:payment:refund",
	}
}

func signedStripeWebhook(
	t *testing.T,
	eventType stripe.EventType,
	object map[string]any,
) *webhook.SignedPayload {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id":          "evt_test_123",
		"object":      "event",
		"api_version": stripe.APIVersion,
		"type":        eventType,
		"data": map[string]any{
			"object": object,
		},
	})
	if err != nil {
		t.Fatalf("encode Stripe webhook fixture: %v", err)
	}
	return webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  stripeWebhookTestSecret,
	})
}

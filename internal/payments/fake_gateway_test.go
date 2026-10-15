package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

const fakeWebhookTestSecret = "fake-webhook-test-secret-at-least-32-bytes"

func TestNewFakeGatewayValidatesCheckoutURL(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"/payments/fake",
		"ftp://example.com/payments/fake",
		"https://user@example.com/payments/fake",
		"https://example.com/payments/fake#fragment",
	}
	for _, checkoutURL := range tests {
		checkoutURL := checkoutURL
		t.Run(checkoutURL, func(t *testing.T) {
			t.Parallel()
			gateway, err := NewFakeGateway(checkoutURL, fakeWebhookTestSecret)
			if !errors.Is(err, ErrInvalidCheckoutURL) {
				t.Fatalf("NewFakeGateway() error = %v, want ErrInvalidCheckoutURL", err)
			}
			if gateway != nil {
				t.Fatalf("NewFakeGateway() gateway = %+v, want nil", gateway)
			}
		})
	}
}

func TestNewFakeGatewayValidatesWebhookSecret(t *testing.T) {
	t.Parallel()

	gateway, err := NewFakeGateway("https://app.example.com/payments/fake", "too-short")
	if !errors.Is(err, ErrInvalidWebhookSecret) {
		t.Fatalf("NewFakeGateway() error = %v, want ErrInvalidWebhookSecret", err)
	}
	if gateway != nil {
		t.Fatalf("NewFakeGateway() gateway = %+v, want nil", gateway)
	}
}

func TestFakeGatewayCreatePaymentIsDeterministic(t *testing.T) {
	t.Parallel()

	gateway, err := NewFakeGateway("https://app.example.com/payments/fake?source=ryoko", fakeWebhookTestSecret)
	if err != nil {
		t.Fatalf("NewFakeGateway() error = %v", err)
	}
	request := CreatePaymentRequest{
		PaymentID:      17,
		BookingID:      29,
		AmountMinor:    250_000,
		Currency:       "VND",
		IdempotencyKey: "booking:29:payment:create",
	}

	first, err := gateway.CreatePayment(context.Background(), request)
	if err != nil {
		t.Fatalf("first CreatePayment() error = %v", err)
	}
	second, err := gateway.CreatePayment(context.Background(), request)
	if err != nil {
		t.Fatalf("second CreatePayment() error = %v", err)
	}
	if first != second {
		t.Fatalf("sessions differ: first=%+v second=%+v", first, second)
	}
	if first.ProviderPaymentID != "fake-payment-17" {
		t.Errorf("provider payment ID = %q, want fake-payment-17", first.ProviderPaymentID)
	}
	wantURL := "https://app.example.com/payments/fake?booking_id=29&payment_id=17&source=ryoko"
	if first.CheckoutURL != wantURL || first.ClientSecret != "" {
		t.Errorf("session = %+v, want checkout URL %q only", first, wantURL)
	}
	if gateway.Provider() != FakeProvider {
		t.Errorf("Provider() = %q, want %q", gateway.Provider(), FakeProvider)
	}
}

func TestFakeGatewayRejectsInvalidCreateRequests(t *testing.T) {
	t.Parallel()

	gateway, err := NewFakeGateway("https://app.example.com/payments/fake", fakeWebhookTestSecret)
	if err != nil {
		t.Fatalf("NewFakeGateway() error = %v", err)
	}
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
			request := valid
			tt.mutate(&request)
			_, err := gateway.CreatePayment(context.Background(), request)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreatePayment() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestFakeGatewayRefundPayment(t *testing.T) {
	t.Parallel()

	gateway, err := NewFakeGateway("https://app.example.com/payments/fake", fakeWebhookTestSecret)
	if err != nil {
		t.Fatalf("NewFakeGateway() error = %v", err)
	}
	request := RefundRequest{
		PaymentID:         17,
		ProviderPaymentID: "fake-payment-17",
		AmountMinor:       250_000,
		Currency:          "VND",
		IdempotencyKey:    "booking:29:payment:refund",
	}
	if err := gateway.RefundPayment(context.Background(), request); err != nil {
		t.Fatalf("RefundPayment() error = %v", err)
	}

	request.Currency = "EUR"
	if err := gateway.RefundPayment(context.Background(), request); !errors.Is(err, ErrInvalidCurrency) {
		t.Fatalf("RefundPayment() error = %v, want ErrInvalidCurrency", err)
	}
}

func TestFakeGatewayVerifyWebhook(t *testing.T) {
	t.Parallel()

	gateway, err := NewFakeGateway("https://app.example.com/payments/fake", fakeWebhookTestSecret)
	if err != nil {
		t.Fatalf("NewFakeGateway() error = %v", err)
	}
	payload := []byte(`{"id":"event-17","type":"payment.succeeded","provider_payment_id":"fake-payment-17"}`)
	signature := signFakeWebhook(payload, fakeWebhookTestSecret)

	event, err := gateway.VerifyWebhook(payload, signature)
	if err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	want := WebhookEvent{
		Provider:          FakeProvider,
		ProviderEventID:   "event-17",
		Type:              PaymentSucceededEvent,
		ProviderPaymentID: "fake-payment-17",
	}
	if event != want {
		t.Errorf("VerifyWebhook() event = %+v, want %+v", event, want)
	}
}

func TestFakeGatewayVerifyWebhookRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	gateway, err := NewFakeGateway("https://app.example.com/payments/fake", fakeWebhookTestSecret)
	if err != nil {
		t.Fatalf("NewFakeGateway() error = %v", err)
	}
	validPayload := []byte(`{"id":"event-17","type":"payment.succeeded","provider_payment_id":"fake-payment-17"}`)
	tests := []struct {
		name      string
		payload   []byte
		signature string
		wantErr   error
	}{
		{name: "missing signature", payload: validPayload, wantErr: ErrInvalidWebhookSignature},
		{name: "malformed signature", payload: validPayload, signature: "not-hex", wantErr: ErrInvalidWebhookSignature},
		{name: "wrong signature", payload: validPayload, signature: signFakeWebhook(validPayload, "different-secret-at-least-32-bytes-long"), wantErr: ErrInvalidWebhookSignature},
		{name: "malformed JSON", payload: []byte(`{"id":`), signature: signFakeWebhook([]byte(`{"id":`), fakeWebhookTestSecret), wantErr: ErrInvalidWebhookPayload},
		{name: "unknown field", payload: []byte(`{"id":"event-17","type":"payment.succeeded","provider_payment_id":"fake-payment-17","extra":true}`), wantErr: ErrInvalidWebhookPayload},
		{name: "missing event ID", payload: []byte(`{"type":"payment.succeeded","provider_payment_id":"fake-payment-17"}`), wantErr: ErrInvalidWebhookPayload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signature := tt.signature
			if signature == "" && tt.wantErr != ErrInvalidWebhookSignature {
				signature = signFakeWebhook(tt.payload, fakeWebhookTestSecret)
			}
			_, err := gateway.VerifyWebhook(tt.payload, signature)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("VerifyWebhook() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func signFakeWebhook(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

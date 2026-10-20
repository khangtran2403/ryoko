package main

import (
	"strings"
	"testing"

	"github.com/khangtran2403/ryoko/internal/config"
	"github.com/khangtran2403/ryoko/internal/payments"
)

func TestNewPaymentGatewayBuildsFakeGateway(t *testing.T) {
	t.Parallel()

	gateway, err := newPaymentGateway(config.PaymentConfig{
		Provider:            payments.FakeProvider,
		FakeCheckoutBaseURL: "https://app.example.com/payments/fake",
		FakeWebhookSecret:   "fake-webhook-test-secret-at-least-32-bytes",
	})
	if err != nil {
		t.Fatalf("newPaymentGateway() error = %v", err)
	}
	if gateway.Provider() != payments.FakeProvider {
		t.Errorf("Provider() = %q, want %q", gateway.Provider(), payments.FakeProvider)
	}
}

func TestNewPaymentGatewayBuildsStripeGateway(t *testing.T) {
	t.Parallel()

	gateway, err := newPaymentGateway(config.PaymentConfig{
		Provider:            payments.StripeProvider,
		StripeSecretKey:     "sk_test_example",
		StripeWebhookSecret: "whsec_example",
	})
	if err != nil {
		t.Fatalf("newPaymentGateway() error = %v", err)
	}
	if gateway.Provider() != payments.StripeProvider {
		t.Errorf("Provider() = %q, want %q", gateway.Provider(), payments.StripeProvider)
	}
	if gateway.WebhookSignatureHeader() != payments.StripeWebhookSignatureHeader {
		t.Errorf("WebhookSignatureHeader() = %q", gateway.WebhookSignatureHeader())
	}
}

func TestNewPaymentGatewayRejectsInvalidStripeConfig(t *testing.T) {
	t.Parallel()

	gateway, err := newPaymentGateway(config.PaymentConfig{
		Provider:            payments.StripeProvider,
		StripeWebhookSecret: "whsec_example",
	})
	if err == nil {
		t.Fatal("newPaymentGateway() returned nil error")
	}
	if gateway != nil {
		t.Fatalf("newPaymentGateway() gateway = %+v, want nil", gateway)
	}
	if !strings.Contains(err.Error(), "create Stripe payment gateway") {
		t.Errorf("error = %q, want Stripe gateway construction error", err)
	}
}

func TestNewPaymentGatewayRejectsUnsupportedProvider(t *testing.T) {
	t.Parallel()

	gateway, err := newPaymentGateway(config.PaymentConfig{Provider: "unsupported"})
	if err == nil {
		t.Fatal("newPaymentGateway() returned nil error")
	}
	if gateway != nil {
		t.Fatalf("newPaymentGateway() gateway = %+v, want nil", gateway)
	}
	if !strings.Contains(err.Error(), "unsupported payment provider") {
		t.Errorf("error = %q, want unsupported-provider error", err)
	}
}

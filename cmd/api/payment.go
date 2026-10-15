package main

import (
	"fmt"

	"github.com/khangtran2403/ryoko/internal/config"
	"github.com/khangtran2403/ryoko/internal/payments"
)

type paymentGateway interface {
	payments.Gateway
	payments.WebhookVerifier
}

func newPaymentGateway(paymentConfig config.PaymentConfig) (paymentGateway, error) {
	switch paymentConfig.Provider {
	case payments.FakeProvider:
		gateway, err := payments.NewFakeGateway(
			paymentConfig.FakeCheckoutBaseURL,
			paymentConfig.FakeWebhookSecret,
		)
		if err != nil {
			return nil, fmt.Errorf("create fake payment gateway: %w", err)
		}
		return gateway, nil
	default:
		return nil, fmt.Errorf("unsupported payment provider %q", paymentConfig.Provider)
	}
}

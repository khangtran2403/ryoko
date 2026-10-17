package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

const FakeProvider = "fake"

type FakeGateway struct {
	checkoutBaseURL url.URL
	webhookSecret   []byte
}

var _ Gateway = (*FakeGateway)(nil)
var _ WebhookVerifier = (*FakeGateway)(nil)

func NewFakeGateway(checkoutBaseURL string, webhookSecret string) (*FakeGateway, error) {
	parsed, err := url.Parse(strings.TrimSpace(checkoutBaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, ErrInvalidCheckoutURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, ErrInvalidCheckoutURL
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, ErrInvalidCheckoutURL
	}
	if len([]byte(webhookSecret)) < 32 {
		return nil, ErrInvalidWebhookSecret
	}

	return &FakeGateway{
		checkoutBaseURL: *parsed,
		webhookSecret:   []byte(webhookSecret),
	}, nil
}

func (g *FakeGateway) Provider() string {
	return FakeProvider
}

func (g *FakeGateway) CreatePayment(
	_ context.Context,
	request CreatePaymentRequest,
) (PaymentSession, error) {
	if request.PaymentID <= 0 ||
		request.BookingID <= 0 ||
		request.AmountMinor <= 0 ||
		strings.TrimSpace(request.IdempotencyKey) == "" {
		return PaymentSession{}, ErrInvalidRequest
	}
	if !supportedFakeCurrency(request.Currency) {
		return PaymentSession{}, ErrInvalidCurrency
	}

	checkoutURL := g.checkoutBaseURL
	query := checkoutURL.Query()
	query.Set("booking_id", strconv.FormatInt(request.BookingID, 10))
	query.Set("payment_id", strconv.FormatInt(request.PaymentID, 10))
	checkoutURL.RawQuery = query.Encode()

	return PaymentSession{
		ProviderPaymentID: fmt.Sprintf("fake-payment-%d", request.PaymentID),
		CheckoutURL:       checkoutURL.String(),
	}, nil
}

func (g *FakeGateway) RefundPayment(_ context.Context, request RefundRequest) error {
	if request.PaymentID <= 0 ||
		strings.TrimSpace(request.ProviderPaymentID) == "" ||
		request.AmountMinor <= 0 ||
		strings.TrimSpace(request.IdempotencyKey) == "" {
		return ErrInvalidRequest
	}
	if !supportedFakeCurrency(request.Currency) {
		return ErrInvalidCurrency
	}
	return nil
}

func (g *FakeGateway) VerifyWebhook(payload []byte, signature string) (WebhookEvent, error) {
	providedSignature, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(providedSignature) != sha256.Size {
		return WebhookEvent{}, ErrInvalidWebhookSignature
	}

	mac := hmac.New(sha256.New, g.webhookSecret)
	_, _ = mac.Write(payload)
	if !hmac.Equal(providedSignature, mac.Sum(nil)) {
		return WebhookEvent{}, ErrInvalidWebhookSignature
	}

	var decoded struct {
		ID                string `json:"id"`
		Type              string `json:"type"`
		ProviderPaymentID string `json:"provider_payment_id"`
		FailureCode       string `json:"failure_code"`
		FailureMessage    string `json:"failure_message"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return WebhookEvent{}, ErrInvalidWebhookPayload
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return WebhookEvent{}, ErrInvalidWebhookPayload
	}

	decoded.ID = strings.TrimSpace(decoded.ID)
	decoded.Type = strings.TrimSpace(decoded.Type)
	decoded.ProviderPaymentID = strings.TrimSpace(decoded.ProviderPaymentID)
	decoded.FailureCode = strings.TrimSpace(decoded.FailureCode)
	decoded.FailureMessage = strings.TrimSpace(decoded.FailureMessage)
	if decoded.ID == "" || decoded.Type == "" || decoded.ProviderPaymentID == "" {
		return WebhookEvent{}, ErrInvalidWebhookPayload
	}
	if decoded.Type == PaymentFailedEvent {
		if decoded.FailureCode == "" || len(decoded.FailureCode) > 100 || len(decoded.FailureMessage) > 1000 {
			return WebhookEvent{}, ErrInvalidWebhookPayload
		}
	} else if decoded.FailureCode != "" || decoded.FailureMessage != "" {
		return WebhookEvent{}, ErrInvalidWebhookPayload
	}

	return WebhookEvent{
		Provider:          FakeProvider,
		ProviderEventID:   decoded.ID,
		Type:              decoded.Type,
		ProviderPaymentID: decoded.ProviderPaymentID,
		FailureCode:       decoded.FailureCode,
		FailureMessage:    decoded.FailureMessage,
	}, nil
}

func supportedFakeCurrency(currency string) bool {
	return currency == "USD" || currency == "VND"
}

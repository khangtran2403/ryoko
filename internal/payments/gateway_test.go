package payments

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type recordingGateway struct {
	mu sync.Mutex

	provider string

	createSession PaymentSession
	createErr     error
	createCalls   []CreatePaymentRequest
	lookupPayment ProviderPayment
	lookupErr     error
	lookupCalls   []string

	refundErr   error
	refundCalls []RefundRequest
}

var _ Gateway = (*recordingGateway)(nil)

func (g *recordingGateway) Provider() string {
	return g.provider
}

func (g *recordingGateway) CreatePayment(
	_ context.Context,
	request CreatePaymentRequest,
) (PaymentSession, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.createCalls = append(g.createCalls, request)

	return g.createSession, g.createErr
}

func (g *recordingGateway) RefundPayment(
	_ context.Context,
	request RefundRequest,
) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.refundCalls = append(g.refundCalls, request)

	return g.refundErr
}

func (g *recordingGateway) LookupPayment(
	_ context.Context,
	providerPaymentID string,
) (ProviderPayment, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.lookupCalls = append(g.lookupCalls, providerPaymentID)
	return g.lookupPayment, g.lookupErr
}

func (g *recordingGateway) recordedCreateCalls() []CreatePaymentRequest {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]CreatePaymentRequest(nil), g.createCalls...)
}

func (g *recordingGateway) recordedRefundCalls() []RefundRequest {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]RefundRequest(nil), g.refundCalls...)
}

func (g *recordingGateway) recordedLookupCalls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]string(nil), g.lookupCalls...)
}

func TestRecordingGatewayCreatePayment(t *testing.T) {
	t.Parallel()

	wantRequest := CreatePaymentRequest{
		PaymentID:      17,
		BookingID:      29,
		AmountMinor:    250_000,
		Currency:       "VND",
		IdempotencyKey: "payment-17-create",
	}
	wantSession := PaymentSession{
		ProviderPaymentID: "provider-payment-17",
		CheckoutURL:       "https://payments.example/checkout/17",
	}
	gateway := &recordingGateway{
		provider:      "fake",
		createSession: wantSession,
	}

	gotSession, err := gateway.CreatePayment(context.Background(), wantRequest)
	if err != nil {
		t.Fatalf("CreatePayment() error = %v", err)
	}
	if gotSession != wantSession {
		t.Fatalf("CreatePayment() session = %+v, want %+v", gotSession, wantSession)
	}
	if gateway.Provider() != "fake" {
		t.Errorf("Provider() = %q, want fake", gateway.Provider())
	}

	calls := gateway.recordedCreateCalls()
	if len(calls) != 1 {
		t.Fatalf("CreatePayment() call count = %d, want 1", len(calls))
	}
	if calls[0] != wantRequest {
		t.Errorf("CreatePayment() request = %+v, want %+v", calls[0], wantRequest)
	}
}

func TestRecordingGatewayReturnsConfiguredCreateError(t *testing.T) {
	t.Parallel()

	gateway := &recordingGateway{createErr: ErrGatewayUnavailable}

	_, err := gateway.CreatePayment(context.Background(), CreatePaymentRequest{})
	if !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("CreatePayment() error = %v, want ErrGatewayUnavailable", err)
	}
	if len(gateway.recordedCreateCalls()) != 1 {
		t.Fatal("CreatePayment() did not record the failed call")
	}
}

func TestRecordingGatewayRefundPayment(t *testing.T) {
	t.Parallel()

	wantRequest := RefundRequest{
		PaymentID:         17,
		ProviderPaymentID: "provider-payment-17",
		AmountMinor:       1_050,
		Currency:          "USD",
		IdempotencyKey:    "payment-17-refund",
	}
	gateway := &recordingGateway{refundErr: ErrGatewayUnavailable}

	err := gateway.RefundPayment(context.Background(), wantRequest)
	if !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("RefundPayment() error = %v, want ErrGatewayUnavailable", err)
	}

	calls := gateway.recordedRefundCalls()
	if len(calls) != 1 {
		t.Fatalf("RefundPayment() call count = %d, want 1", len(calls))
	}
	if calls[0] != wantRequest {
		t.Errorf("RefundPayment() request = %+v, want %+v", calls[0], wantRequest)
	}
}

func TestRecordingGatewaySafelyRecordsConcurrentCalls(t *testing.T) {
	t.Parallel()

	const callCount = 50

	gateway := &recordingGateway{}
	var waitGroup sync.WaitGroup
	waitGroup.Add(callCount)

	for index := 0; index < callCount; index++ {
		go func(paymentID int64) {
			defer waitGroup.Done()

			_, _ = gateway.CreatePayment(
				context.Background(),
				CreatePaymentRequest{PaymentID: paymentID},
			)
		}(int64(index + 1))
	}

	waitGroup.Wait()

	if got := len(gateway.recordedCreateCalls()); got != callCount {
		t.Fatalf("CreatePayment() call count = %d, want %d", got, callCount)
	}
}

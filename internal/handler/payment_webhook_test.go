package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/khangtran2403/ryoko/internal/payments"
)

type fakeWebhookVerifier struct {
	event     payments.WebhookEvent
	err       error
	payload   []byte
	signature string
}

func (f *fakeWebhookVerifier) VerifyWebhook(payload []byte, signature string) (payments.WebhookEvent, error) {
	f.payload = append([]byte(nil), payload...)
	f.signature = signature
	return f.event, f.err
}

type fakeWebhookProcessor struct {
	result payments.WebhookResult
	err    error
	event  payments.WebhookEvent
	called bool
}

func (f *fakeWebhookProcessor) ProcessWebhook(
	_ context.Context,
	event payments.WebhookEvent,
) (payments.WebhookResult, error) {
	f.called = true
	f.event = event
	return f.result, f.err
}

func TestPaymentWebhookHandlerProcessesVerifiedEvent(t *testing.T) {
	event := payments.WebhookEvent{
		Provider:          "fake",
		ProviderEventID:   "event-17",
		Type:              payments.PaymentSucceededEvent,
		ProviderPaymentID: "fake-payment-17",
	}
	verifier := &fakeWebhookVerifier{event: event}
	processor := &fakeWebhookProcessor{result: payments.WebhookResult{Processed: true}}
	handler := NewPaymentWebhookHandler(verifier, processor)
	payload := `{"id":"event-17"}`
	request := httptest.NewRequest(http.MethodPost, "/payments/webhooks/fake", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(FakeWebhookSignatureHeader, "signature-17")
	response := httptest.NewRecorder()

	handler.Handle(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %q", response.Code, http.StatusOK, response.Body.String())
	}
	if string(verifier.payload) != payload || verifier.signature != "signature-17" {
		t.Errorf("verifier input = {%q %q}, want {%q %q}", verifier.payload, verifier.signature, payload, "signature-17")
	}
	if !processor.called || processor.event != event {
		t.Errorf("processor call = {called:%t event:%+v}, want verified event", processor.called, processor.event)
	}
	var body PaymentWebhookResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Received || !body.Processed || body.Duplicate || body.Ignored || body.AlreadyProcessed || body.RefundQueued || body.Retryable {
		t.Errorf("response = %+v, want received and processed", body)
	}
}

func TestPaymentWebhookHandlerReturnsIdempotentDispositions(t *testing.T) {
	tests := []struct {
		name   string
		result payments.WebhookResult
		check  func(PaymentWebhookResponse) bool
	}{
		{name: "duplicate", result: payments.WebhookResult{Duplicate: true}, check: func(r PaymentWebhookResponse) bool { return r.Duplicate }},
		{name: "ignored", result: payments.WebhookResult{Ignored: true}, check: func(r PaymentWebhookResponse) bool { return r.Ignored }},
		{name: "already processed", result: payments.WebhookResult{AlreadyProcessed: true}, check: func(r PaymentWebhookResponse) bool { return r.AlreadyProcessed }},
		{name: "refund queued", result: payments.WebhookResult{Processed: true, RefundQueued: true}, check: func(r PaymentWebhookResponse) bool { return r.Processed && r.RefundQueued }},
		{name: "retryable failure", result: payments.WebhookResult{Processed: true, Retryable: true}, check: func(r PaymentWebhookResponse) bool { return r.Processed && r.Retryable }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewPaymentWebhookHandler(
				&fakeWebhookVerifier{event: payments.WebhookEvent{Provider: "fake"}},
				&fakeWebhookProcessor{result: tt.result},
			)
			response := performWebhookRequest(handler, `{}`, "signature")
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			var body PaymentWebhookResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if !body.Received || !tt.check(body) {
				t.Errorf("response = %+v, want %s disposition", body, tt.name)
			}
		})
	}
}

func TestPaymentWebhookHandlerMapsVerificationErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid signature", err: payments.ErrInvalidWebhookSignature, wantStatus: http.StatusUnauthorized},
		{name: "invalid payload", err: payments.ErrInvalidWebhookPayload, wantStatus: http.StatusBadRequest},
		{name: "unexpected verifier failure", err: errors.New("verifier failed"), wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &fakeWebhookProcessor{}
			handler := NewPaymentWebhookHandler(&fakeWebhookVerifier{err: tt.err}, processor)
			response := performWebhookRequest(handler, `{}`, "signature")
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if processor.called {
				t.Fatal("processor was called after webhook verification failed")
			}
		})
	}
}

func TestPaymentWebhookHandlerMapsProcessingErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid event", err: payments.ErrInvalidWebhookEvent, wantStatus: http.StatusBadRequest},
		{name: "payment not ready", err: payments.ErrWebhookPaymentNotFound, wantStatus: http.StatusServiceUnavailable},
		{name: "payment not processable", err: payments.ErrWebhookPaymentNotProcessable, wantStatus: http.StatusConflict},
		{name: "unexpected processing failure", err: errors.New("database failed"), wantStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewPaymentWebhookHandler(
				&fakeWebhookVerifier{event: payments.WebhookEvent{Provider: "fake"}},
				&fakeWebhookProcessor{err: tt.err},
			)
			response := performWebhookRequest(handler, `{}`, "signature")
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}

func TestPaymentWebhookHandlerEnforcesRequestBoundary(t *testing.T) {
	handler := NewPaymentWebhookHandler(&fakeWebhookVerifier{}, &fakeWebhookProcessor{})

	t.Run("content type", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/payments/webhooks/fake", strings.NewReader(`{}`))
		response := httptest.NewRecorder()
		handler.Handle(response, request)
		if response.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusUnsupportedMediaType)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		response := performWebhookRequest(handler, strings.Repeat("a", int(maxJSONRequestBodyBytes)+1), "signature")
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
		}
	})
}

func performWebhookRequest(
	handler *PaymentWebhookHandler,
	payload string,
	signature string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/payments/webhooks/fake", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(FakeWebhookSignatureHeader, signature)
	response := httptest.NewRecorder()
	handler.Handle(response, request)
	return response
}

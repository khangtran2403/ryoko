package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/khangtran2403/ryoko/internal/payments"
)

const FakeWebhookSignatureHeader = "X-Ryoko-Signature"

type paymentWebhookProcessor interface {
	ProcessWebhook(ctx context.Context, event payments.WebhookEvent) (payments.WebhookResult, error)
}

type PaymentWebhookHandler struct {
	verifier  payments.WebhookVerifier
	processor paymentWebhookProcessor
}

func NewPaymentWebhookHandler(
	verifier payments.WebhookVerifier,
	processor paymentWebhookProcessor,
) *PaymentWebhookHandler {
	if verifier == nil {
		panic("payment webhook verifier must not be nil")
	}
	if processor == nil {
		panic("payment webhook processor must not be nil")
	}
	return &PaymentWebhookHandler{verifier: verifier, processor: processor}
}

type PaymentWebhookResponse struct {
	Received         bool `json:"received"`
	Processed        bool `json:"processed"`
	Duplicate        bool `json:"duplicate"`
	Ignored          bool `json:"ignored"`
	AlreadyProcessed bool `json:"already_processed"`
}

func (h *PaymentWebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if r.ContentLength > maxJSONRequestBodyBytes {
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBodyBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "Failed to read webhook", http.StatusBadRequest)
		return
	}

	event, err := h.verifier.VerifyWebhook(payload, r.Header.Get(FakeWebhookSignatureHeader))
	switch {
	case errors.Is(err, payments.ErrInvalidWebhookSignature):
		http.Error(w, "Invalid webhook signature", http.StatusUnauthorized)
		return
	case errors.Is(err, payments.ErrInvalidWebhookPayload):
		http.Error(w, "Invalid webhook payload", http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, "Failed to verify webhook", http.StatusInternalServerError)
		return
	}

	result, err := h.processor.ProcessWebhook(r.Context(), event)
	switch {
	case errors.Is(err, payments.ErrInvalidWebhookEvent):
		http.Error(w, "Invalid webhook event", http.StatusBadRequest)
		return
	case errors.Is(err, payments.ErrWebhookPaymentNotFound):
		http.Error(w, "Webhook payment is not ready", http.StatusServiceUnavailable)
		return
	case errors.Is(err, payments.ErrWebhookPaymentNotProcessable):
		http.Error(w, "Webhook payment cannot be processed", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "Failed to process webhook", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(PaymentWebhookResponse{
		Received:         true,
		Processed:        result.Processed,
		Duplicate:        result.Duplicate,
		Ignored:          result.Ignored,
		AlreadyProcessed: result.AlreadyProcessed,
	})
}

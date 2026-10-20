package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type recordingReconciliationQueries struct {
	payments   []sqlc.Payment
	listErr    error
	listParams sqlc.ListPaymentsForReconciliationParams
	touchRows  int64
	touchErr   error
	touchedIDs []int64
}

func (q *recordingReconciliationQueries) ListPaymentsForReconciliation(
	_ context.Context,
	params sqlc.ListPaymentsForReconciliationParams,
) ([]sqlc.Payment, error) {
	q.listParams = params
	return q.payments, q.listErr
}

func (q *recordingReconciliationQueries) TouchPendingPaymentReconciliation(
	_ context.Context,
	paymentID int64,
) (int64, error) {
	q.touchedIDs = append(q.touchedIDs, paymentID)
	return q.touchRows, q.touchErr
}

type mappedReconciliationGateway struct {
	provider string
	states   map[string]ProviderPayment
	errors   map[string]error
	lookups  []string
}

func (g *mappedReconciliationGateway) Provider() string { return g.provider }

func (g *mappedReconciliationGateway) CreatePayment(
	context.Context,
	CreatePaymentRequest,
) (PaymentSession, error) {
	panic("unexpected CreatePayment call")
}

func (g *mappedReconciliationGateway) RefundPayment(context.Context, RefundRequest) error {
	panic("unexpected RefundPayment call")
}

func (g *mappedReconciliationGateway) LookupPayment(
	_ context.Context,
	providerPaymentID string,
) (ProviderPayment, error) {
	g.lookups = append(g.lookups, providerPaymentID)
	return g.states[providerPaymentID], g.errors[providerPaymentID]
}

type recordingReconciliationProcessor struct {
	events []WebhookEvent
	err    error
}

func (p *recordingReconciliationProcessor) ProcessWebhook(
	_ context.Context,
	event WebhookEvent,
) (WebhookResult, error) {
	p.events = append(p.events, event)
	return WebhookResult{Processed: p.err == nil}, p.err
}

func TestReconciliationServiceMapsProviderStatesToEvents(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	queries := &recordingReconciliationQueries{
		payments: []sqlc.Payment{
			reconciliationPayment(1, "pi_pending"),
			reconciliationPayment(2, "pi_succeeded"),
			reconciliationPayment(3, "pi_failed"),
			reconciliationPayment(4, "pi_canceled"),
		},
		touchRows: 1,
	}
	gateway := &mappedReconciliationGateway{
		provider: StripeProvider,
		states: map[string]ProviderPayment{
			"pi_pending":   {State: ProviderPaymentPending},
			"pi_succeeded": {State: ProviderPaymentSucceeded},
			"pi_failed": {
				State:          ProviderPaymentFailed,
				FailureCode:    "card_declined",
				FailureMessage: "declined by provider",
			},
			"pi_canceled": {State: ProviderPaymentCanceled},
		},
	}
	processor := &recordingReconciliationProcessor{}
	service := NewReconciliationService(queries, gateway, processor, time.Minute, 25)
	service.now = func() time.Time { return now }

	result, err := service.ReconcilePendingPayments(context.Background())
	if err != nil {
		t.Fatalf("ReconcilePendingPayments() error = %v", err)
	}
	if result != (ReconciliationBatchResult{Checked: 4, Reconciled: 3, Unchanged: 1}) {
		t.Fatalf("result = %+v", result)
	}
	if queries.listParams.Provider != StripeProvider || queries.listParams.BatchSize != 25 {
		t.Errorf("list params = %+v", queries.listParams)
	}
	wantCutoff := now.Add(-time.Minute)
	if !queries.listParams.UpdatedBefore.Valid || !queries.listParams.UpdatedBefore.Time.Equal(wantCutoff) {
		t.Errorf("updated-before = %+v, want %v", queries.listParams.UpdatedBefore, wantCutoff)
	}
	if len(queries.touchedIDs) != 4 || len(gateway.lookups) != 4 {
		t.Fatalf("touched = %v lookups = %v", queries.touchedIDs, gateway.lookups)
	}
	if len(processor.events) != 3 {
		t.Fatalf("events = %+v, want 3", processor.events)
	}
	wantTypes := []string{PaymentSucceededEvent, PaymentFailedEvent, PaymentCanceledEvent}
	for index, event := range processor.events {
		if event.Type != wantTypes[index] {
			t.Errorf("event %d type = %q, want %q", index, event.Type, wantTypes[index])
		}
		if event.ProviderEventID == "" || event.Provider != StripeProvider {
			t.Errorf("event %d = %+v", index, event)
		}
	}
	if processor.events[1].FailureCode != "card_declined" ||
		processor.events[1].FailureMessage != "declined by provider" {
		t.Errorf("failure event = %+v", processor.events[1])
	}
}

func TestReconciliationServiceContinuesAfterPaymentFailure(t *testing.T) {
	providerErr := errors.New("provider unavailable")
	queries := &recordingReconciliationQueries{
		payments: []sqlc.Payment{
			reconciliationPayment(1, "pi_error"),
			reconciliationPayment(2, "pi_succeeded"),
		},
		touchRows: 1,
	}
	gateway := &mappedReconciliationGateway{
		provider: StripeProvider,
		states: map[string]ProviderPayment{
			"pi_succeeded": {State: ProviderPaymentSucceeded},
		},
		errors: map[string]error{"pi_error": providerErr},
	}
	processor := &recordingReconciliationProcessor{}
	service := NewReconciliationService(queries, gateway, processor, time.Minute, 10)

	result, err := service.ReconcilePendingPayments(context.Background())
	if !errors.Is(err, providerErr) {
		t.Fatalf("error = %v, want provider error", err)
	}
	if result != (ReconciliationBatchResult{Checked: 2, Reconciled: 1, Failed: 1}) {
		t.Errorf("result = %+v", result)
	}
	if len(processor.events) != 1 || processor.events[0].ProviderPaymentID != "pi_succeeded" {
		t.Errorf("events = %+v", processor.events)
	}
}

func TestReconciliationServiceSkipsPaymentChangedConcurrently(t *testing.T) {
	queries := &recordingReconciliationQueries{
		payments:  []sqlc.Payment{reconciliationPayment(1, "pi_1")},
		touchRows: 0,
	}
	gateway := &mappedReconciliationGateway{provider: StripeProvider}
	processor := &recordingReconciliationProcessor{}
	service := NewReconciliationService(queries, gateway, processor, time.Minute, 10)

	result, err := service.ReconcilePendingPayments(context.Background())
	if err != nil {
		t.Fatalf("ReconcilePendingPayments() error = %v", err)
	}
	if result != (ReconciliationBatchResult{Checked: 1, Unchanged: 1}) {
		t.Errorf("result = %+v", result)
	}
	if len(gateway.lookups) != 0 || len(processor.events) != 0 {
		t.Errorf("lookups = %v events = %v", gateway.lookups, processor.events)
	}
}

func TestReconciliationEventUsesSafeFailureFallback(t *testing.T) {
	event, terminal, err := reconciliationEvent(
		StripeProvider,
		"pi_123",
		ProviderPayment{State: ProviderPaymentFailed},
	)
	if err != nil || !terminal {
		t.Fatalf("reconciliationEvent() = (%+v, %t, %v)", event, terminal, err)
	}
	if event.FailureCode != "payment_failed" || event.Type != PaymentFailedEvent {
		t.Errorf("event = %+v", event)
	}
}

func reconciliationPayment(id int64, providerPaymentID string) sqlc.Payment {
	return sqlc.Payment{
		ID:                     id,
		BookingID:              id + 100,
		Provider:               StripeProvider,
		ProviderPaymentID:      pgtype.Text{String: providerPaymentID, Valid: true},
		ProviderIdempotencyKey: "key",
		AmountMinor:            100,
		Currency:               "VND",
		Status:                 "pending",
	}
}

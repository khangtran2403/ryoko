package payments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestWebhookServiceProcessesPaymentSucceededIdempotently(t *testing.T) {
	pool := newPaymentWebhookIntegrationPool(t)
	service := NewWebhookService(pool, sqlc.New(pool))
	bookingID, paymentID, providerPaymentID := insertPaymentWebhookFixture(t, pool, 15*time.Minute)

	event := WebhookEvent{
		Provider:          "fake",
		ProviderEventID:   "evt-success-1",
		Type:              PaymentSucceededEvent,
		ProviderPaymentID: providerPaymentID,
	}
	result, err := service.ProcessWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("ProcessWebhook() error = %v", err)
	}
	if !result.Processed || result.PaymentID != paymentID || result.BookingID != bookingID {
		t.Fatalf("ProcessWebhook() result = %+v, want processed payment %d booking %d", result, paymentID, bookingID)
	}

	assertPaymentWebhookState(t, pool, bookingID, paymentID, "confirmed", "succeeded", 1, 1)

	duplicate, err := service.ProcessWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("duplicate ProcessWebhook() error = %v", err)
	}
	if !duplicate.Duplicate || duplicate.Processed {
		t.Fatalf("duplicate ProcessWebhook() result = %+v, want duplicate", duplicate)
	}
	assertPaymentWebhookState(t, pool, bookingID, paymentID, "confirmed", "succeeded", 1, 1)

	event.ProviderEventID = "evt-success-2"
	alreadyProcessed, err := service.ProcessWebhook(context.Background(), event)
	if err != nil {
		t.Fatalf("already-processed ProcessWebhook() error = %v", err)
	}
	if !alreadyProcessed.AlreadyProcessed || alreadyProcessed.PaymentID != paymentID {
		t.Fatalf("already-processed result = %+v, want already processed payment %d", alreadyProcessed, paymentID)
	}
	assertPaymentWebhookState(t, pool, bookingID, paymentID, "confirmed", "succeeded", 1, 2)
}

func TestWebhookServiceRecordsIgnoredEvent(t *testing.T) {
	pool := newPaymentWebhookIntegrationPool(t)
	service := NewWebhookService(pool, sqlc.New(pool))
	bookingID, paymentID, providerPaymentID := insertPaymentWebhookFixture(t, pool, 15*time.Minute)

	result, err := service.ProcessWebhook(context.Background(), WebhookEvent{
		Provider:          "fake",
		ProviderEventID:   "evt-ignored",
		Type:              "payment.created",
		ProviderPaymentID: providerPaymentID,
	})
	if err != nil {
		t.Fatalf("ProcessWebhook() error = %v", err)
	}
	if !result.Ignored || result.Processed {
		t.Fatalf("ProcessWebhook() result = %+v, want ignored", result)
	}
	assertPaymentWebhookState(t, pool, bookingID, paymentID, "pending_payment", "pending", 0, 1)
}

func TestWebhookServiceRollsBackUnprocessableEvents(t *testing.T) {
	pool := newPaymentWebhookIntegrationPool(t)
	service := NewWebhookService(pool, sqlc.New(pool))
	bookingID, paymentID, providerPaymentID := insertPaymentWebhookFixture(t, pool, -time.Minute)

	event := WebhookEvent{
		Provider:          "fake",
		ProviderEventID:   "evt-expired",
		Type:              PaymentSucceededEvent,
		ProviderPaymentID: providerPaymentID,
	}
	_, err := service.ProcessWebhook(context.Background(), event)
	if !errors.Is(err, ErrWebhookPaymentNotProcessable) {
		t.Fatalf("ProcessWebhook() error = %v, want ErrWebhookPaymentNotProcessable", err)
	}
	assertPaymentWebhookState(t, pool, bookingID, paymentID, "pending_payment", "pending", 0, 0)

	event.ProviderEventID = "evt-missing-payment"
	event.ProviderPaymentID = "missing-payment"
	_, err = service.ProcessWebhook(context.Background(), event)
	if !errors.Is(err, ErrWebhookPaymentNotFound) {
		t.Fatalf("missing payment error = %v, want ErrWebhookPaymentNotFound", err)
	}
	assertPaymentWebhookState(t, pool, bookingID, paymentID, "pending_payment", "pending", 0, 0)
}

func insertPaymentWebhookFixture(t *testing.T, pool *pgxpool.Pool, expiresIn time.Duration) (int64, int64, string) {
	t.Helper()
	ctx := context.Background()
	var userID, hotelID, roomTypeID int64
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, full_name) VALUES ($1, 'Webhook User') RETURNING id`, fmt.Sprintf("webhook-%d@example.com", time.Now().UnixNano())).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO hotels (name, address, city) VALUES ('Webhook Hotel', '1 Test Street', 'Test City') RETURNING id`).Scan(&hotelID); err != nil {
		t.Fatalf("insert hotel: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO room_types (hotel_id, name, price_per_night, capacity, total_rooms) VALUES ($1, 'Webhook Room', 100, 2, 1) RETURNING id`, hotelID).Scan(&roomTypeID); err != nil {
		t.Fatalf("insert room type: %v", err)
	}

	var bookingID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO bookings (
			user_id, room_type_id, check_in, check_out, rooms_count, guest_count,
			price_per_night, total_price, status, currency, payment_expires_at
		) VALUES ($1, $2, current_date + 1, current_date + 2, 1, 1, 100, 100, 'pending_payment', 'VND', $3)
		RETURNING id`, userID, roomTypeID, time.Now().Add(expiresIn)).Scan(&bookingID); err != nil {
		t.Fatalf("insert booking: %v", err)
	}

	providerPaymentID := fmt.Sprintf("fake-payment-%d", bookingID)
	var paymentID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO payments (
			booking_id, provider, provider_payment_id, provider_idempotency_key,
			amount_minor, currency, status
		) VALUES ($1, 'fake', $2, $3, 100, 'VND', 'pending')
		RETURNING id`, bookingID, providerPaymentID, fmt.Sprintf("booking-%d", bookingID)).Scan(&paymentID); err != nil {
		t.Fatalf("insert payment: %v", err)
	}
	return bookingID, paymentID, providerPaymentID
}

func assertPaymentWebhookState(t *testing.T, pool *pgxpool.Pool, bookingID, paymentID int64, wantBooking, wantPayment string, wantHistory, wantEvents int) {
	t.Helper()
	var bookingStatus, paymentStatus string
	if err := pool.QueryRow(context.Background(), `SELECT b.status, p.status FROM bookings b JOIN payments p ON p.booking_id = b.id WHERE b.id = $1 AND p.id = $2`, bookingID, paymentID).Scan(&bookingStatus, &paymentStatus); err != nil {
		t.Fatalf("read payment state: %v", err)
	}
	if bookingStatus != wantBooking || paymentStatus != wantPayment {
		t.Errorf("state = booking %q payment %q, want %q and %q", bookingStatus, paymentStatus, wantBooking, wantPayment)
	}
	var historyCount, eventCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM booking_status_history WHERE booking_id = $1`, bookingID).Scan(&historyCount); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_webhook_events`).Scan(&eventCount); err != nil {
		t.Fatalf("count webhook events: %v", err)
	}
	if historyCount != wantHistory || eventCount != wantEvents {
		t.Errorf("counts = history %d events %d, want %d and %d", historyCount, eventCount, wantHistory, wantEvents)
	}
}

func newPaymentWebhookIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL, explicitlyConfigured := os.LookupEnv("TEST_DATABASE_URL")
	if !explicitlyConfigured {
		databaseURL = "postgres://test:test@localhost:5433/testdb?sslmode=disable"
	}
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run payment webhook integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create integration pool: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		if !explicitlyConfigured {
			t.Skipf("local PostgreSQL test database is unavailable: %v", err)
		}
		t.Fatalf("ping integration database: %v", err)
	}
	schemaName := fmt.Sprintf("payment_webhook_test_%d", time.Now().UnixNano())
	schemaIdentifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		adminPool.Close()
		t.Fatalf("create integration schema: %v", err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse integration database URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create isolated integration pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
		adminPool.Close()
	})

	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find migrations: paths=%d error=%v", len(paths), err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		if _, err := pool.Exec(context.Background(), string(migration)); err != nil {
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}
	return pool
}

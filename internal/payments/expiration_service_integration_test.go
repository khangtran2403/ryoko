package payments

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestExpirationServiceExpiresBookingAndReleasesInventory(t *testing.T) {
	pool := newPaymentWebhookIntegrationPool(t)
	service := NewExpirationService(pool, sqlc.New(pool), 100)
	expiredBookingID, expiredPaymentID, _ := insertPaymentWebhookFixture(t, pool, -time.Minute)
	activeBookingID, activePaymentID, _ := insertPaymentWebhookFixture(t, pool, 15*time.Minute)
	insertReservedAvailability(t, pool, expiredBookingID)
	insertReservedAvailability(t, pool, activeBookingID)

	expired, err := service.ExpirePendingPayments(context.Background())
	if err != nil {
		t.Fatalf("ExpirePendingPayments() error = %v", err)
	}
	if expired != 1 {
		t.Fatalf("ExpirePendingPayments() = %d, want 1", expired)
	}
	assertPaymentWebhookState(t, pool, expiredBookingID, expiredPaymentID, "cancelled", "expired", 1, 0)
	assertEmailOutboxEvents(t, pool, expiredBookingID, "booking_cancelled")
	assertPaymentWebhookState(t, pool, activeBookingID, activePaymentID, "pending_payment", "pending", 0, 0)
	assertEmailOutboxEvents(t, pool, activeBookingID)

	var roomsBooked int32
	if err := pool.QueryRow(context.Background(), `
		SELECT rooms_booked
		FROM room_type_availability AS a
		JOIN bookings AS b ON b.room_type_id = a.room_type_id
		WHERE b.id = $1 AND a.date = b.check_in`, expiredBookingID).Scan(&roomsBooked); err != nil {
		t.Fatalf("read released availability: %v", err)
	}
	if roomsBooked != 0 {
		t.Errorf("rooms_booked = %d, want 0", roomsBooked)
	}

	var fromStatus, toStatus, reason string
	var actorID *int64
	if err := pool.QueryRow(context.Background(), `
		SELECT from_status, to_status, changed_by_user_id, reason
		FROM booking_status_history
		WHERE booking_id = $1`, expiredBookingID).Scan(&fromStatus, &toStatus, &actorID, &reason); err != nil {
		t.Fatalf("read expiration history: %v", err)
	}
	if fromStatus != "pending_payment" || toStatus != "cancelled" || actorID != nil || reason != "Payment window expired" {
		t.Errorf("history = {%q %q %v %q}, want system payment expiration", fromStatus, toStatus, actorID, reason)
	}

	expired, err = service.ExpirePendingPayments(context.Background())
	if err != nil || expired != 0 {
		t.Errorf("second ExpirePendingPayments() = (%d, %v), want (0, nil)", expired, err)
	}
	assertEmailOutboxEvents(t, pool, expiredBookingID, "booking_cancelled")
}

func TestExpirationServiceRollsBackWhenInventoryIsInconsistent(t *testing.T) {
	pool := newPaymentWebhookIntegrationPool(t)
	service := NewExpirationService(pool, sqlc.New(pool), 100)
	bookingID, paymentID, _ := insertPaymentWebhookFixture(t, pool, -time.Minute)

	_, err := service.ExpirePendingPayments(context.Background())
	if err == nil || !strings.Contains(err.Error(), "availability invariant violated") {
		t.Fatalf("ExpirePendingPayments() error = %v, want availability invariant error", err)
	}
	assertPaymentWebhookState(t, pool, bookingID, paymentID, "pending_payment", "pending", 0, 0)
}

func insertReservedAvailability(t *testing.T, pool *pgxpool.Pool, bookingID int64) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO room_type_availability (room_type_id, date, rooms_booked)
		SELECT b.room_type_id, day::date, b.rooms_count
		FROM bookings AS b
		CROSS JOIN LATERAL generate_series(
			b.check_in::timestamp,
			(b.check_out - 1)::timestamp,
			interval '1 day'
		) AS day
		WHERE b.id = $1`, bookingID)
	if err != nil {
		t.Fatalf("insert reserved availability: %v", err)
	}
}

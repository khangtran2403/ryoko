package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	notification "github.com/khangtran2403/ryoko/internal/notifications"
)

const MaxExpirationBatchSize int32 = 1000

var ErrInvalidExpirationBatchSize = errors.New("payment expiration batch size must be between 1 and 1000")

type ExpirationService struct {
	pool      *pgxpool.Pool
	queries   *sqlc.Queries
	batchSize int32
}

func NewExpirationService(pool *pgxpool.Pool, queries *sqlc.Queries, batchSize int32) *ExpirationService {
	if pool == nil {
		panic("payment expiration pool must not be nil")
	}
	if queries == nil {
		panic("payment expiration queries must not be nil")
	}
	if batchSize < 1 || batchSize > MaxExpirationBatchSize {
		panic(ErrInvalidExpirationBatchSize.Error())
	}
	return &ExpirationService{pool: pool, queries: queries, batchSize: batchSize}
}

func (s *ExpirationService) ExpirePendingPayments(ctx context.Context) (int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("begin payment expiration transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	bookings, err := qtx.ListExpiredPendingBookingsForUpdate(ctx, s.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list expired pending bookings: %w", err)
	}

	for _, booking := range bookings {
		if err := expirePendingPayment(ctx, qtx, booking); err != nil {
			return 0, fmt.Errorf("expire booking %d: %w", booking.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit payment expiration transaction: %w", err)
	}
	return int64(len(bookings)), nil
}

func expirePendingPayment(ctx context.Context, qtx *sqlc.Queries, booking sqlc.Booking) error {
	payment, err := qtx.GetPaymentByBookingIDForUpdate(ctx, booking.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("payment invariant violated: pending booking has no payment")
	}
	if err != nil {
		return fmt.Errorf("lock payment: %w", err)
	}
	return cancelPendingBookingAndPayment(ctx, qtx, booking, payment, "Payment window expired")
}

func cancelPendingBookingAndPayment(
	ctx context.Context,
	qtx *sqlc.Queries,
	booking sqlc.Booking,
	payment sqlc.Payment,
	reason string,
) error {
	if booking.Status != "pending_payment" {
		return fmt.Errorf("booking invariant violated: status is %q", booking.Status)
	}
	if payment.Status != "pending" {
		return fmt.Errorf("payment invariant violated: status is %q", payment.Status)
	}

	nights := int(booking.CheckOut.Time.Sub(booking.CheckIn.Time) / (24 * time.Hour))
	if nights <= 0 {
		return errors.New("booking invariant violated: invalid date range")
	}
	availability, err := qtx.LockAvailabilityRows(ctx, sqlc.LockAvailabilityRowsParams{
		RoomTypeID: booking.RoomTypeID,
		CheckIn:    booking.CheckIn,
		CheckOut:   booking.CheckOut,
	})
	if err != nil {
		return fmt.Errorf("lock availability: %w", err)
	}
	if len(availability) != nights {
		return fmt.Errorf("availability invariant violated: got %d rows for %d nights", len(availability), nights)
	}

	affected, err := qtx.DecrementAvailability(ctx, sqlc.DecrementAvailabilityParams{
		RoomsCount: booking.RoomsCount,
		RoomTypeID: booking.RoomTypeID,
		CheckIn:    booking.CheckIn,
		CheckOut:   booking.CheckOut,
	})
	if err != nil {
		return fmt.Errorf("release availability: %w", err)
	}
	if affected != int64(nights) {
		return fmt.Errorf("availability invariant violated: released %d rows for %d nights", affected, nights)
	}

	if _, err := qtx.ExpirePendingBooking(ctx, booking.ID); errors.Is(err, pgx.ErrNoRows) {
		return errors.New("booking is no longer pending payment")
	} else if err != nil {
		return fmt.Errorf("mark booking cancelled: %w", err)
	}
	if _, err := qtx.MarkPaymentExpired(ctx, payment.ID); errors.Is(err, pgx.ErrNoRows) {
		return errors.New("payment is no longer pending")
	} else if err != nil {
		return fmt.Errorf("mark payment expired: %w", err)
	}

	_, err = qtx.CreateBookingStatusHistory(ctx, sqlc.CreateBookingStatusHistoryParams{
		BookingID:  booking.ID,
		FromStatus: pgtype.Text{String: "pending_payment", Valid: true},
		ToStatus:   "cancelled",
		Reason:     pgtype.Text{String: reason, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("record expiration history: %w", err)
	}
	_, err = qtx.EnqueueBookingEmail(ctx, sqlc.EnqueueBookingEmailParams{
		BookingID: booking.ID,
		EventType: notification.BookingCancelledEvent,
	})
	if err != nil {
		return fmt.Errorf("enqueue booking cancellation email: %w", err)
	}
	return nil
}

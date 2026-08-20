package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidDates     = errors.New("invalid booking dates")
	ErrInvalidRooms     = errors.New("invalid room count")
	ErrInvalidGuests    = errors.New("invalid guest count")
	ErrRoomTypeNotFound = errors.New("room type not found")
	ErrCapacityExceeded = errors.New("room capacity exceeded")
	ErrUnavailable      = errors.New("rooms unavailable")
	ErrInvalidUser      = errors.New("invalid user")
	ErrBookingNotFound  = errors.New("booking not found")
)

type CreateInput struct {
	UserID     int64     `json:"user_id"`
	RoomTypeID int64     `json:"room_type_id"`
	CheckIn    time.Time `json:"check_in"`
	CheckOut   time.Time `json:"check_out"`
	RoomsCount int32     `json:"rooms_count"`
	GuestCount int32     `json:"guest_count"`
}
type Service struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
}

func NewService(pool *pgxpool.Pool, queries *sqlc.Queries) *Service {
	return &Service{
		pool:    pool,
		queries: queries,
	}
}

func (s *Service) CreateBooking(ctx context.Context, input CreateInput) (sqlc.Booking, error) {
	// Validate input
	checkInDate := time.Date(
		input.CheckIn.Year(),
		input.CheckIn.Month(),
		input.CheckIn.Day(),
		0, 0, 0, 0,
		time.UTC,
	)

	checkOutDate := time.Date(
		input.CheckOut.Year(),
		input.CheckOut.Month(),
		input.CheckOut.Day(),
		0, 0, 0, 0,
		time.UTC,
	)
	if !checkOutDate.After(checkInDate) {
		return sqlc.Booking{}, ErrInvalidDates
	}
	if input.RoomsCount <= 0 {
		return sqlc.Booking{}, ErrInvalidRooms
	}
	if input.GuestCount <= 0 {
		return sqlc.Booking{}, ErrInvalidGuests
	}
	if input.UserID <= 0 {
		return sqlc.Booking{}, ErrInvalidUser
	}
	if input.RoomTypeID <= 0 {
		return sqlc.Booking{}, ErrRoomTypeNotFound
	}
	nights := int(checkOutDate.Sub(checkInDate) / (24 * time.Hour))

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	roomType, err := qtx.GetRoomTypeForBooking(ctx, input.RoomTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Booking{}, ErrRoomTypeNotFound
	}
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("get room type: %w", err)
	}

	maximunGuests := int64(roomType.Capacity) * int64(input.RoomsCount)
	if int64(input.GuestCount) > maximunGuests {
		return sqlc.Booking{}, ErrCapacityExceeded
	}
	if input.RoomsCount > roomType.TotalRooms {
		return sqlc.Booking{}, ErrUnavailable
	}
	checkIn := pgtype.Date{
		Time:  checkInDate,
		Valid: true,
	}

	checkOut := pgtype.Date{
		Time:  checkOutDate,
		Valid: true,
	}
	err = qtx.EnsureAvailabilityRows(ctx, sqlc.EnsureAvailabilityRowsParams{
		CheckIn:    checkIn,
		CheckOut:   checkOut,
		RoomTypeID: input.RoomTypeID,
	})
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("availability rows: %w", err)
	}
	rows, err := qtx.LockAvailabilityRows(ctx, sqlc.LockAvailabilityRowsParams{
		RoomTypeID: input.RoomTypeID,
		CheckIn:    checkIn,
		CheckOut:   checkOut,
	})
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf(
			"lock availability rows: %w",
			err,
		)
	}
	if len(rows) != nights {
		return sqlc.Booking{}, fmt.Errorf("availability invariant violated: got %d rows for %d nights",
			len(rows),
			nights)
	}
	for _, row := range rows {
		if row.RoomsBooked > roomType.TotalRooms-input.RoomsCount {
			return sqlc.Booking{}, ErrUnavailable
		}
	}
	affected, err := qtx.IncrementAvailability(ctx, sqlc.IncrementAvailabilityParams{
		RoomsCount: input.RoomsCount,
		RoomTypeID: input.RoomTypeID,
		CheckIn:    checkIn,
		CheckOut:   checkOut,
		TotalRooms: roomType.TotalRooms,
	})
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("increment availability: %w", err)
	}
	if affected != int64(nights) {
		return sqlc.Booking{}, ErrUnavailable
	}
	createBooking, err := qtx.CreateBooking(ctx, sqlc.CreateBookingParams{
		UserID:        input.UserID,
		RoomTypeID:    input.RoomTypeID,
		CheckIn:       checkIn,
		CheckOut:      checkOut,
		RoomsCount:    input.RoomsCount,
		GuestCount:    input.GuestCount,
		PricePerNight: roomType.PricePerNight,
	})
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("create booking: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sqlc.Booking{}, fmt.Errorf(
			"commit booking transaction: %w",
			err,
		)
	}
	return createBooking, nil
}
func (s *Service) GetBookingByUserID(ctx context.Context, bookingID int64, userID int64) (sqlc.Booking, error) {
	if bookingID <= 0 {
		return sqlc.Booking{}, ErrBookingNotFound
	}
	if userID <= 0 {
		return sqlc.Booking{}, ErrInvalidUser
	}

	getBooking, err := s.queries.GetBookingByIDForUser(ctx, sqlc.GetBookingByIDForUserParams{
		BookingID: bookingID,
		UserID:    userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Booking{}, ErrBookingNotFound
	}
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("get booking for user: %w", err)
	}
	return getBooking, nil
}
func (s *Service) ListBookingsByUser(
	ctx context.Context,
	userID int64,
) ([]sqlc.Booking, error) {
	if userID <= 0 {
		return nil, ErrInvalidUser
	}

	bookings, err := s.queries.ListBookingsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list bookings by user: %w", err)
	}

	return bookings, nil
}

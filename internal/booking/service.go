package booking

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidDates                 = errors.New("invalid booking dates")
	ErrInvalidRooms                 = errors.New("invalid room count")
	ErrInvalidGuests                = errors.New("invalid guest count")
	ErrRoomTypeNotFound             = errors.New("room type not found")
	ErrCapacityExceeded             = errors.New("room capacity exceeded")
	ErrUnavailable                  = errors.New("rooms unavailable")
	ErrInvalidUser                  = errors.New("invalid user")
	ErrBookingNotFound              = errors.New("booking not found")
	ErrBookingNotCancellable        = errors.New("booking cannot be cancelled")
	ErrInvalidHotelID               = errors.New("hotel ID must be positive")
	ErrCheckInInPast                = errors.New("check-in date cannot be in the past")
	ErrInvalidCity                  = errors.New("city must not be empty")
	ErrInvalidPage                  = errors.New("page must be a positive integer")
	ErrInvalidPageSize              = errors.New("page_size must be between 1 and 100")
	ErrInvalidSort                  = errors.New("sort must be price_asc or price_desc")
	ErrIdempotencyKeyConflict       = errors.New("idempotency key hash conflict")
	ErrInvalidIdempotencyKey        = errors.New("idempotency key is missing or invalid")
	ErrBookingStatusHistoryNotFound = errors.New("booking status history not found")
	ErrInvalidCancellationReason    = errors.New("cancellation reason must be between 1 and 500 characters")
)

const (
	DefaultHotelSearchPage      int32  = 1
	DefaultHotelSearchPageSize  int32  = 20
	MaxHotelSearchPageSize      int32  = 100
	HotelSearchSortPriceAsc     string = "price_asc"
	HotelSearchSortPriceDesc    string = "price_desc"
	DefaultBookingPage          int32  = 1
	DefaultBookingPageSize      int32  = 20
	MaxBookingPageSize          int32  = 100
	MaxCancellationReasonLength        = 500
)

type CreateInput struct {
	UserID         int64     `json:"user_id"`
	RoomTypeID     int64     `json:"room_type_id"`
	CheckIn        time.Time `json:"check_in"`
	CheckOut       time.Time `json:"check_out"`
	RoomsCount     int32     `json:"rooms_count"`
	GuestCount     int32     `json:"guest_count"`
	IdempotencyKey string    `json:"idempotency_key"`
}
type AvailabilityInput struct {
	HotelID    int64
	CheckIn    time.Time
	CheckOut   time.Time
	RoomsCount int32
	GuestCount int32
}
type HotelSearchInput struct {
	City       string
	CheckIn    time.Time
	CheckOut   time.Time
	RoomsCount int32
	GuestCount int32
	Page       int32
	PageSize   int32
	Sort       string
}

type HotelSearchPagination struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	HasMore  bool  `json:"has_more"`
}

type HotelSearchResult struct {
	Hotels     []sqlc.SearchAvailableHotelsRow `json:"hotels"`
	Pagination HotelSearchPagination           `json:"pagination"`
}
type ListBookingsInput struct {
	UserID   int64
	Page     int32
	PageSize int32
}

type BookingPagination struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	HasMore  bool  `json:"has_more"`
}

type ListBookingsResult struct {
	Bookings   []sqlc.Booking    `json:"bookings"`
	Pagination BookingPagination `json:"pagination"`
}
type AdminCancellationInput struct {
	BookingID int64
	AdminID   int64
	Reason    string
}

type cancellationInput struct {
	bookingID int64
	actorID   int64
	ownerID   *int64
	reason    string
}
type Service struct {
	pool    *pgxpool.Pool
	queries *sqlc.Queries
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, queries *sqlc.Queries) *Service {
	return &Service{
		pool:    pool,
		queries: queries,
		now:     time.Now,
	}
}

func (s *Service) CreateBooking(ctx context.Context, input CreateInput) (sqlc.Booking, error) {
	// Validate input
	checkInDate, checkOutDate, err := s.validateStayDates(input.CheckIn, input.CheckOut)
	var checkIn, checkOut pgtype.Date
	var roomType sqlc.GetRoomTypeForBookingRow
	if err != nil {
		return sqlc.Booking{}, err
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
	if strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 255 {
		return sqlc.Booking{}, ErrInvalidIdempotencyKey
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)

	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s:%d:%d", input.UserID, input.RoomTypeID, checkInDate.Format("2006-01-02"), checkOutDate.Format("2006-01-02"), input.RoomsCount, input.GuestCount)))
	requestHash := sum[:]
	claim, err := qtx.ClaimBookingIdempotencyKey(ctx, sqlc.ClaimBookingIdempotencyKeyParams{
		UserID:         input.UserID,
		IdempotencyKey: input.IdempotencyKey,
		RequestHash:    requestHash,
	})
	switch {
	case err != nil:
		return sqlc.Booking{}, fmt.Errorf("claim idempotency key: %w", err)
	case claim == 0:
		claimExist, err := qtx.GetBookingIdempotencyKey(ctx, sqlc.GetBookingIdempotencyKeyParams{
			UserID:         input.UserID,
			IdempotencyKey: input.IdempotencyKey,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Booking{}, fmt.Errorf("idempotency key not found: %w", err)
		}
		if err != nil {
			return sqlc.Booking{}, fmt.Errorf("get idempotency key: %w", err)
		}
		if !bytes.Equal(requestHash, claimExist.RequestHash) {
			return sqlc.Booking{}, ErrIdempotencyKeyConflict
		}
		if !claimExist.BookingID.Valid {
			return sqlc.Booking{}, errors.New("idempotency invariant violated: booking ID is missing")
		}
		existing, err := qtx.GetBookingByIDForUser(
			ctx,
			sqlc.GetBookingByIDForUserParams{
				BookingID: claimExist.BookingID.Int64,
				UserID:    input.UserID,
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.Booking{}, fmt.Errorf("booking not found for idempotency key: %w", err)
		}
		if err != nil {
			return sqlc.Booking{}, fmt.Errorf("get booking for idempotency key: %w", err)
		}
		return existing, nil

	case claim == 1:

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
		checkIn = pgtype.Date{
			Time:  checkInDate,
			Valid: true,
		}

		checkOut = pgtype.Date{
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
	bookingID := pgtype.Int8{Int64: createBooking.ID, Valid: true}
	if bookingID.Valid {
		attachment, err := qtx.AttachBookingToIdempotencyKey(ctx, sqlc.AttachBookingToIdempotencyKeyParams{
			UserID:         input.UserID,
			IdempotencyKey: input.IdempotencyKey,
			RequestHash:    requestHash,
			BookingID:      bookingID,
		})
		if err != nil {
			return sqlc.Booking{}, fmt.Errorf("attach idempotency key: %w", err)
		}
		if attachment != 1 {
			return sqlc.Booking{}, fmt.Errorf("failed to attach booking to idempotency key")
		}
	}
	_, err = qtx.CreateBookingStatusHistory(ctx, sqlc.CreateBookingStatusHistoryParams{
		BookingID:       createBooking.ID,
		FromStatus:      pgtype.Text{},
		ToStatus:        "confirmed",
		ChangedByUserID: pgtype.Int8{Int64: input.UserID, Valid: true},
		Reason:          pgtype.Text{String: "Booking created", Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Booking{}, ErrBookingStatusHistoryNotFound
	}
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("create booking status history: %w", err)
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
	input ListBookingsInput,
) (ListBookingsResult, error) {
	if input.UserID <= 0 {
		return ListBookingsResult{}, ErrInvalidUser
	}
	if input.Page <= 0 {
		return ListBookingsResult{}, ErrInvalidPage
	}
	if input.PageSize <= 0 || input.PageSize > MaxBookingPageSize {
		return ListBookingsResult{}, ErrInvalidPageSize
	}
	resultOffset := int64(input.Page-1) * int64(input.PageSize)

	bookings, err := s.queries.ListBookingsByUser(ctx, sqlc.ListBookingsByUserParams{
		UserID:       input.UserID,
		ResultLimit:  int64(input.PageSize) + 1,
		ResultOffset: resultOffset,
	})
	if err != nil {
		return ListBookingsResult{}, fmt.Errorf("list bookings by user: %w", err)
	}
	hasMore := len(bookings) > int(input.PageSize)
	if hasMore {
		bookings = bookings[:input.PageSize]
	}
	if bookings == nil {
		bookings = []sqlc.Booking{}
	}

	return ListBookingsResult{
		Bookings: bookings,
		Pagination: BookingPagination{
			Page:     input.Page,
			PageSize: input.PageSize,
			HasMore:  hasMore,
		},
	}, nil
}
func (s *Service) ListBookingsHistoryByUser(ctx context.Context, bookingID int64, userID int64) ([]sqlc.BookingStatusHistory, error) {
	if bookingID <= 0 {
		return nil, ErrBookingNotFound
	}
	if userID <= 0 {
		return nil, ErrInvalidUser
	}
	getBooking, err := s.queries.GetBookingByIDForUser(ctx, sqlc.GetBookingByIDForUserParams{
		BookingID: bookingID,
		UserID:    userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrBookingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get booking: %w", err)
	}
	history, err := s.queries.ListBookingStatusHistoryForUser(ctx, sqlc.ListBookingStatusHistoryForUserParams{
		BookingID: getBooking.ID,
		UserID:    getBooking.UserID,
	})
	if err != nil {
		return nil, fmt.Errorf("list booking history: %w", err)
	}
	return history, nil
}
func (s *Service) cancelBooking(ctx context.Context, input cancellationInput) (sqlc.Booking, error) {
	if input.bookingID <= 0 {
		return sqlc.Booking{}, ErrBookingNotFound
	}
	if input.actorID <= 0 {
		return sqlc.Booking{}, ErrInvalidUser
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	var lockBooking sqlc.Booking
	if input.ownerID != nil {
		lockBooking, err = qtx.GetBookingForCancellation(ctx, sqlc.GetBookingForCancellationParams{
			BookingID: input.bookingID,
			UserID:    *input.ownerID,
		})
	} else {
		lockBooking, err = qtx.GetBookingForAdminCancellation(ctx, input.bookingID)

	}

	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Booking{}, ErrBookingNotFound
	}
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("get booking: %w ", err)
	}
	if lockBooking.Status != "confirmed" {
		return sqlc.Booking{}, ErrBookingNotCancellable
	}
	checkIn := lockBooking.CheckIn
	checkOut := lockBooking.CheckOut
	roomsCount := lockBooking.RoomsCount
	roomTypeID := lockBooking.RoomTypeID
	nights := int(checkOut.Time.Sub(checkIn.Time) / (24 * time.Hour))
	if nights <= 0 {
		return sqlc.Booking{}, fmt.Errorf("invalid stored booking dates")
	}
	today := dateOnlyUTC(s.now())
	checkInDate := time.Date(
		checkIn.Time.Year(),
		checkIn.Time.Month(),
		checkIn.Time.Day(),
		0, 0, 0, 0,
		time.UTC,
	)

	if !checkInDate.After(today) {
		return sqlc.Booking{}, ErrBookingNotCancellable
	}
	rows, err := qtx.LockAvailabilityRows(ctx, sqlc.LockAvailabilityRowsParams{
		RoomTypeID: roomTypeID,
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
	affected, err := qtx.DecrementAvailability(ctx, sqlc.DecrementAvailabilityParams{
		RoomsCount: roomsCount,
		RoomTypeID: roomTypeID,
		CheckIn:    checkIn,
		CheckOut:   checkOut,
	})
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("decrement availability: %w", err)
	}
	if affected != int64(nights) {
		return sqlc.Booking{}, ErrUnavailable
	}
	var cancel sqlc.Booking
	if input.ownerID != nil {
		cancel, err = qtx.CancelBooking(ctx, sqlc.CancelBookingParams{
			BookingID: input.bookingID,
			UserID:    *input.ownerID,
		})
	} else {
		cancel, err = qtx.CancelBookingForAdmin(ctx, input.bookingID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Booking{}, ErrBookingNotCancellable
	}
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("cancel booking: %w", err)
	}
	_, err = qtx.CreateBookingStatusHistory(ctx, sqlc.CreateBookingStatusHistoryParams{
		BookingID:       cancel.ID,
		FromStatus:      pgtype.Text{String: "confirmed", Valid: true},
		ToStatus:        "cancelled",
		ChangedByUserID: pgtype.Int8{Int64: input.actorID, Valid: true},
		Reason:          pgtype.Text{String: input.reason, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.Booking{}, ErrBookingStatusHistoryNotFound
	}
	if err != nil {
		return sqlc.Booking{}, fmt.Errorf("create booking status history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return sqlc.Booking{}, fmt.Errorf(
			"commit booking transaction: %w",
			err,
		)
	}
	return cancel, nil
}
func (s *Service) CompletePastBookings(ctx context.Context, now time.Time) (int64, error) {
	now = now.UTC()

	today := pgtype.Date{
		Time: time.Date(
			now.Year(),
			now.Month(),
			now.Day(),
			0, 0, 0, 0,
			time.UTC,
		),
		Valid: true,
	}

	affected, err := s.queries.CompletePastBookings(ctx, today)
	if err != nil {
		return 0, fmt.Errorf("complete past bookings: %w", err)
	}

	return affected, nil
}
func (s *Service) CancelBooking(ctx context.Context, bookingID int64, userID int64) (sqlc.Booking, error) {
	if bookingID <= 0 {
		return sqlc.Booking{}, ErrBookingNotFound
	}
	if userID <= 0 {
		return sqlc.Booking{}, ErrInvalidUser
	}

	return s.cancelBooking(ctx, cancellationInput{
		bookingID: bookingID,
		actorID:   userID,
		ownerID:   &userID,
		reason:    "Booking cancelled by user",
	})
}
func (s *Service) CancelBookingAsAdmin(ctx context.Context, input AdminCancellationInput) (sqlc.Booking, error) {
	if input.BookingID <= 0 {
		return sqlc.Booking{}, ErrBookingNotFound
	}
	if input.AdminID <= 0 {
		return sqlc.Booking{}, ErrInvalidUser
	}

	reason := strings.TrimSpace(input.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > MaxCancellationReasonLength {
		return sqlc.Booking{}, ErrInvalidCancellationReason
	}

	return s.cancelBooking(ctx, cancellationInput{
		bookingID: input.BookingID,
		actorID:   input.AdminID,
		ownerID:   nil,
		reason:    reason,
	})
}
func (s *Service) ListAvailableRoomTypes(ctx context.Context, input AvailabilityInput) ([]sqlc.ListAvailableRoomTypesRow, error) {
	if input.HotelID <= 0 {
		return nil, ErrInvalidHotelID
	}
	checkInDate, checkOutDate, err := s.validateStayDates(input.CheckIn, input.CheckOut)
	if err != nil {
		return nil, err
	}
	if input.RoomsCount <= 0 {
		return nil, ErrInvalidRooms
	}
	if input.GuestCount <= 0 {
		return nil, ErrInvalidGuests
	}
	search, err := s.queries.ListAvailableRoomTypes(ctx, sqlc.ListAvailableRoomTypesParams{
		CheckIn:    pgtype.Date{Time: checkInDate, Valid: true},
		CheckOut:   pgtype.Date{Time: checkOutDate, Valid: true},
		HotelID:    input.HotelID,
		RoomsCount: input.RoomsCount,
		GuestCount: input.GuestCount,
	})
	if err != nil {
		return nil, fmt.Errorf("search available room types: %w", err)
	}
	if search == nil {
		search = []sqlc.ListAvailableRoomTypesRow{}
	}
	return search, nil
}
func (s *Service) SearchAvailableHotels(ctx context.Context, input HotelSearchInput) (HotelSearchResult, error) {
	city := strings.TrimSpace(input.City)
	if city == "" {
		return HotelSearchResult{}, ErrInvalidCity
	}
	checkInDate, checkOutDate, err := s.validateStayDates(input.CheckIn, input.CheckOut)
	if err != nil {
		return HotelSearchResult{}, err
	}
	if input.RoomsCount <= 0 {
		return HotelSearchResult{}, ErrInvalidRooms
	}
	if input.GuestCount <= 0 {
		return HotelSearchResult{}, ErrInvalidGuests
	}
	if input.Page <= 0 {
		return HotelSearchResult{}, ErrInvalidPage
	}
	if input.PageSize <= 0 || input.PageSize > MaxHotelSearchPageSize {
		return HotelSearchResult{}, ErrInvalidPageSize
	}
	if input.Sort != HotelSearchSortPriceAsc && input.Sort != HotelSearchSortPriceDesc {
		return HotelSearchResult{}, ErrInvalidSort
	}

	resultOffset := int64(input.Page-1) * int64(input.PageSize)
	search, err := s.queries.SearchAvailableHotels(ctx, sqlc.SearchAvailableHotelsParams{
		City:         city,
		CheckIn:      pgtype.Date{Time: checkInDate, Valid: true},
		CheckOut:     pgtype.Date{Time: checkOutDate, Valid: true},
		RoomsCount:   input.RoomsCount,
		GuestCount:   input.GuestCount,
		Sort:         input.Sort,
		ResultLimit:  int64(input.PageSize) + 1,
		ResultOffset: resultOffset,
	})
	if err != nil {
		return HotelSearchResult{}, fmt.Errorf("search available hotels: %w", err)
	}

	hasMore := len(search) > int(input.PageSize)
	if hasMore {
		search = search[:input.PageSize]
	}
	if search == nil {
		search = []sqlc.SearchAvailableHotelsRow{}
	}

	return HotelSearchResult{
		Hotels: search,
		Pagination: HotelSearchPagination{
			Page:     input.Page,
			PageSize: input.PageSize,
			HasMore:  hasMore,
		},
	}, nil
}
func dateOnlyUTC(value time.Time) time.Time {
	return time.Date(
		value.Year(),
		value.Month(),
		value.Day(),
		0, 0, 0, 0,
		time.UTC,
	)
}

func (s *Service) validateStayDates(
	checkIn time.Time,
	checkOut time.Time,
) (time.Time, time.Time, error) {
	checkInDate := dateOnlyUTC(checkIn)
	checkOutDate := dateOnlyUTC(checkOut)
	today := dateOnlyUTC(s.now())

	if checkInDate.Before(today) {
		return time.Time{}, time.Time{}, ErrCheckInInPast
	}

	if !checkOutDate.After(checkInDate) {
		return time.Time{}, time.Time{}, ErrInvalidDates
	}

	return checkInDate, checkOutDate, nil
}

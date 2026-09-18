package admin_booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var (
	ErrInvalidHotelID   = errors.New("hotel ID must be positive")
	ErrInvalidDateRange = errors.New("check in from must not be after check in to.")
	ErrInvalidPage      = errors.New("page must be a positive integer")
	ErrInvalidPageSize  = errors.New("page_size must be between 1 and 100")
	ErrInvalidStatus    = errors.New("status must be one of 'completed', 'cancelled', or 'confirmed'")
)

const (
	DefaultBookingPage     int32 = 1
	DefaultBookingPageSize int32 = 20
	MaxBookingPageSize     int32 = 100
)

type ListBookingsForAdminInput struct {
	HotelID     *int64
	Status      *string
	CheckInFrom *time.Time
	CheckInTo   *time.Time
	Page        int32
	PageSize    int32
}

type BookingPagination struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	HasMore  bool  `json:"has_more"`
}

type ListBookingsForAdminResult struct {
	Bookings   []sqlc.ListBookingsForAdminRow `json:"bookings"`
	Pagination BookingPagination              `json:"pagination"`
}
type Service struct {
	queries *sqlc.Queries
}

func NewService(queries *sqlc.Queries) *Service {
	return &Service{
		queries: queries,
	}
}

func (s *Service) ListBookingsForAdmin(ctx context.Context, input ListBookingsForAdminInput) (ListBookingsForAdminResult, error) {
	var hotelID pgtype.Int8
	var status pgtype.Text
	var checkIn, checkOut pgtype.Date
	if input.HotelID != nil && *input.HotelID <= 0 {
		return ListBookingsForAdminResult{}, ErrInvalidHotelID
	}
	if input.HotelID != nil {

		hotelID = pgtype.Int8{
			Int64: *input.HotelID,
			Valid: true,
		}
	}
	if input.Status != nil {
		if *input.Status != "completed" && *input.Status != "cancelled" && *input.Status != "confirmed" {
			return ListBookingsForAdminResult{}, ErrInvalidStatus
		}
		status = pgtype.Text{
			String: *input.Status,
			Valid:  true,
		}
	}
	if input.CheckInFrom != nil && input.CheckInTo != nil && input.CheckInFrom.After(*input.CheckInTo) {
		return ListBookingsForAdminResult{}, ErrInvalidDateRange
	}
	if input.CheckInFrom != nil {
		checkIn = pgtype.Date{
			Time:  *input.CheckInFrom,
			Valid: true,
		}
	}
	if input.CheckInTo != nil {
		checkOut = pgtype.Date{
			Time:  *input.CheckInTo,
			Valid: true,
		}
	}

	if input.Page <= 0 {
		return ListBookingsForAdminResult{}, ErrInvalidPage
	}
	if input.PageSize <= 0 || input.PageSize > MaxBookingPageSize {
		return ListBookingsForAdminResult{}, ErrInvalidPageSize
	}
	resultOffset := int64(input.Page-1) * int64(input.PageSize)

	bookings, err := s.queries.ListBookingsForAdmin(ctx, sqlc.ListBookingsForAdminParams{
		HotelID:      hotelID,
		Status:       status,
		CheckInFrom:  checkIn,
		CheckInTo:    checkOut,
		ResultLimit:  int64(input.PageSize) + 1,
		ResultOffset: resultOffset,
	})
	if err != nil {
		return ListBookingsForAdminResult{}, fmt.Errorf("list bookings for admin: %w", err)
	}
	hasMore := len(bookings) > int(input.PageSize)
	if hasMore {
		bookings = bookings[:input.PageSize]
	}
	if bookings == nil {
		bookings = []sqlc.ListBookingsForAdminRow{}
	}

	return ListBookingsForAdminResult{
		Bookings: bookings,
		Pagination: BookingPagination{
			Page:     input.Page,
			PageSize: input.PageSize,
			HasMore:  hasMore,
		},
	}, nil
}

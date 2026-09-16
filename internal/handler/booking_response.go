package handler

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type BookingResponse struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	RoomTypeID     int64     `json:"room_type_id"`
	CheckIn        string    `json:"check_in"`
	CheckOut       string    `json:"check_out"`
	NumberOfNights int       `json:"number_of_nights"`
	RoomsCount     int32     `json:"rooms_count"`
	GuestCount     int32     `json:"guest_count"`
	PricePerNight  string    `json:"price_per_night"`
	TotalPrice     string    `json:"total_price"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type ListBookingsResponse struct {
	Bookings   []BookingResponse         `json:"bookings"`
	Pagination booking.BookingPagination `json:"pagination"`
}

func bookingResponseFromModel(model sqlc.Booking) (BookingResponse, error) {
	if !model.CheckIn.Valid || !model.CheckOut.Valid {
		return BookingResponse{}, fmt.Errorf("invalid stored booking: booking dates are missing")
	}

	numberOfNights := int(model.CheckOut.Time.Sub(model.CheckIn.Time) / (24 * time.Hour))
	if numberOfNights <= 0 {
		return BookingResponse{}, fmt.Errorf("invalid stored booking: booking date range is invalid")
	}

	pricePerNight, err := numericString(model.PricePerNight)
	if err != nil {
		return BookingResponse{}, fmt.Errorf("format price per night: %w", err)
	}
	totalPrice, err := numericString(model.TotalPrice)
	if err != nil {
		return BookingResponse{}, fmt.Errorf("format total price: %w", err)
	}

	return BookingResponse{
		ID:             model.ID,
		UserID:         model.UserID,
		RoomTypeID:     model.RoomTypeID,
		CheckIn:        model.CheckIn.Time.Format(time.DateOnly),
		CheckOut:       model.CheckOut.Time.Format(time.DateOnly),
		NumberOfNights: numberOfNights,
		RoomsCount:     model.RoomsCount,
		GuestCount:     model.GuestCount,
		PricePerNight:  pricePerNight,
		TotalPrice:     totalPrice,
		Status:         model.Status,
		CreatedAt:      model.CreatedAt.Time,
		UpdatedAt:      model.UpdatedAt.Time,
	}, nil
}

func listBookingsResponseFromResult(result booking.ListBookingsResult) (ListBookingsResponse, error) {
	bookings := make([]BookingResponse, 0, len(result.Bookings))
	for _, model := range result.Bookings {
		response, err := bookingResponseFromModel(model)
		if err != nil {
			return ListBookingsResponse{}, fmt.Errorf("format booking %d: %w", model.ID, err)
		}
		bookings = append(bookings, response)
	}

	return ListBookingsResponse{
		Bookings:   bookings,
		Pagination: result.Pagination,
	}, nil
}

func numericString(value pgtype.Numeric) (string, error) {
	driverValue, err := value.Value()
	if err != nil {
		return "", err
	}
	formatted, ok := driverValue.(string)
	if !ok {
		return "", fmt.Errorf("invalid stored booking: numeric value is missing")
	}
	return formatted, nil
}

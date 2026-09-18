package handler

import (
	"fmt"
	"time"

	"github.com/khangtran2403/ryoko/internal/admin_booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type AdminBookingResponse struct {
	BookingID      int64     `json:"booking_id"`
	UserID         int64     `json:"user_id"`
	CustomerName   string    `json:"customer_name"`
	CustomerEmail  string    `json:"customer_email"`
	RoomTypeID     int64     `json:"room_type_id"`
	RoomTypeName   string    `json:"room_type_name"`
	HotelID        int64     `json:"hotel_id"`
	HotelName      string    `json:"hotel_name"`
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

type ListBookingsAdminResponse struct {
	Bookings   []AdminBookingResponse          `json:"bookings"`
	Pagination admin_booking.BookingPagination `json:"pagination"`
}

func adminbookingResponseFromModel(model sqlc.ListBookingsForAdminRow) (AdminBookingResponse, error) {
	if !model.CheckIn.Valid || !model.CheckOut.Valid {
		return AdminBookingResponse{}, fmt.Errorf("invalid stored booking: booking dates are missing")
	}

	if model.NumberOfNights <= 0 {
		return AdminBookingResponse{}, fmt.Errorf("invalid stored booking: booking date range is invalid")
	}

	pricePerNight, err := numericString(model.PricePerNight)
	if err != nil {
		return AdminBookingResponse{}, fmt.Errorf("format price per night: %w", err)
	}
	totalPrice, err := numericString(model.TotalPrice)
	if err != nil {
		return AdminBookingResponse{}, fmt.Errorf("format total price: %w", err)
	}
	if model.CustomerName == "" {
		return AdminBookingResponse{}, fmt.Errorf("invalid stored booking: customer name is missing")
	}
	if model.CustomerEmail == "" {
		return AdminBookingResponse{}, fmt.Errorf("invalid stored booking: customer email is missing")
	}
	return AdminBookingResponse{
		BookingID:      model.BookingID,
		UserID:         model.UserID,
		CustomerName:   model.CustomerName,
		CustomerEmail:  model.CustomerEmail,
		RoomTypeID:     model.RoomTypeID,
		RoomTypeName:   model.RoomTypeName,
		HotelID:        model.HotelID,
		HotelName:      model.HotelName,
		CheckIn:        model.CheckIn.Time.Format(time.DateOnly),
		CheckOut:       model.CheckOut.Time.Format(time.DateOnly),
		NumberOfNights: int(model.NumberOfNights),
		RoomsCount:     model.RoomsCount,
		GuestCount:     model.GuestCount,
		PricePerNight:  pricePerNight,
		TotalPrice:     totalPrice,
		Status:         model.Status,
		CreatedAt:      model.CreatedAt.Time,
		UpdatedAt:      model.UpdatedAt.Time,
	}, nil
}

func listBookingsAdminResponseFromResult(result admin_booking.ListBookingsForAdminResult) (ListBookingsAdminResponse, error) {
	bookings := make([]AdminBookingResponse, 0, len(result.Bookings))
	for _, model := range result.Bookings {
		response, err := adminbookingResponseFromModel(model)
		if err != nil {
			return ListBookingsAdminResponse{}, fmt.Errorf("format booking %d: %w", model.BookingID, err)
		}
		bookings = append(bookings, response)
	}

	return ListBookingsAdminResponse{
		Bookings:   bookings,
		Pagination: result.Pagination,
	}, nil
}

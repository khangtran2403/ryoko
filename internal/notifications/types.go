package notification

import "context"

const (
	BookingConfirmedEvent = "booking_confirmed"
	BookingCancelledEvent = "booking_cancelled"
	PaymentRefundedEvent  = "payment_refunded"
)

type BookingEmailPayload struct {
	BookingID     int64  `json:"booking_id"`
	GuestName     string `json:"guest_name"`
	HotelName     string `json:"hotel_name"`
	RoomTypeName  string `json:"room_type_name"`
	CheckIn       string `json:"check_in"`
	CheckOut      string `json:"check_out"`
	RoomsCount    int32  `json:"rooms_count"`
	GuestCount    int32  `json:"guest_count"`
	PricePerNight string `json:"price_per_night"`
	TotalPrice    string `json:"total_price"`
	Currency      string `json:"currency"`
}

type BookingEmail struct {
	To        string
	EventType string
	Payload   BookingEmailPayload
}

// EmailSender is defined by the notification package so the outbox service
// does not depend on SMTP or any other delivery provider.
type EmailSender interface {
	SendBookingEmail(ctx context.Context, email BookingEmail) error
}

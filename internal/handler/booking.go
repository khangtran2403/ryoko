package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type bookingService interface {
	CreateBooking(ctx context.Context, input booking.CreateInput) (sqlc.Booking, error)
	ListBookingsByUser(ctx context.Context, userID int64) ([]sqlc.Booking, error)
	GetBookingByUserID(ctx context.Context, bookingID int64, userID int64) (sqlc.Booking, error)
	CancelBooking(ctx context.Context, bookingID int64, userID int64) (sqlc.Booking, error)
}

type BookingHandler struct {
	service bookingService
}

func NewBookingHandler(service bookingService) *BookingHandler {
	return &BookingHandler{
		service: service,
	}
}

type CreateBookingRequest struct {
	CheckIn    string `json:"check_in"`
	CheckOut   string `json:"check_out"`
	RoomsCount int32  `json:"rooms_count"`
	GuestCount int32  `json:"guest_count"`
}

func (h *BookingHandler) CreateBooking(w http.ResponseWriter, r *http.Request) {
	var req CreateBookingRequest
	roomTypeID := r.PathValue("roomTypeID")
	convID, err := strconv.ParseInt(roomTypeID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid room type ID", http.StatusBadRequest)
		return
	}
	if convID <= 0 {
		http.Error(w, "Room type ID must be positive", http.StatusBadRequest)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	checkIn, err := time.Parse(time.DateOnly, req.CheckIn)
	if err != nil {
		http.Error(w, "check_in must use YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	checkOut, err := time.Parse(time.DateOnly, req.CheckOut)
	if err != nil {
		http.Error(w, "check_out must use YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	created, err := h.service.CreateBooking(
		r.Context(),
		booking.CreateInput{
			UserID:     principal.UserID,
			RoomTypeID: convID,
			CheckIn:    checkIn,
			CheckOut:   checkOut,
			RoomsCount: req.RoomsCount,
			GuestCount: req.GuestCount,
		},
	)
	switch {
	case errors.Is(err, booking.ErrInvalidDates),
		errors.Is(err, booking.ErrInvalidRooms),
		errors.Is(err, booking.ErrInvalidGuests),
		errors.Is(err, booking.ErrCapacityExceeded):
		http.Error(w, err.Error(), http.StatusBadRequest)

	case errors.Is(err, booking.ErrRoomTypeNotFound):
		http.Error(w, "Room type not found", http.StatusNotFound)

	case errors.Is(err, booking.ErrUnavailable):
		http.Error(w, "Requested rooms are unavailable", http.StatusConflict)

	case errors.Is(err, booking.ErrInvalidUser):
		http.Error(w, "Unauthorized", http.StatusUnauthorized)

	case err != nil:
		http.Error(w, "Failed to create booking", http.StatusInternalServerError)

	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)
	}
}
func (h *BookingHandler) GetBookingByUserID(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	getID := r.PathValue("bookingID")
	if getID == "" {
		http.Error(w, "booking ID is required", http.StatusBadRequest)
		return
	}
	convID, err := strconv.ParseInt(getID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	getBooking, err := h.service.GetBookingByUserID(r.Context(), convID, principal.UserID)
	if errors.Is(err, booking.ErrBookingNotFound) {
		http.Error(w, "Booking not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "get booking failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(getBooking)
}
func (h *BookingHandler) ListBookingsByUser(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	listBooking, err := h.service.ListBookingsByUser(r.Context(), principal.UserID)
	if err != nil {
		http.Error(w, "list booking failed", http.StatusInternalServerError)
		return
	}
	if listBooking == nil {
		listBooking = []sqlc.Booking{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(listBooking)
}
func (h *BookingHandler) CancelBooking(w http.ResponseWriter, r *http.Request) {
	getID := r.PathValue("bookingID")
	if getID == "" {
		http.Error(w, "booking ID is required", http.StatusBadRequest)
		return
	}
	convID, err := strconv.ParseInt(getID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	cancelBooking, err := h.service.CancelBooking(r.Context(), convID, principal.UserID)
	switch {
	case errors.Is(err, booking.ErrBookingNotFound):
		http.Error(w, "Booking not found", http.StatusNotFound)
		return
	case errors.Is(err, booking.ErrBookingNotCancellable):
		http.Error(w, "Booking cannot be cancelled", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "cancel booking failed", http.StatusInternalServerError)
		return

	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(cancelBooking)
	}
}

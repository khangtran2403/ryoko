package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type bookingService interface {
	CreateBooking(ctx context.Context, input booking.CreateInput) (sqlc.Booking, error)
	ListBookingsByUser(ctx context.Context, input booking.ListBookingsInput) (booking.ListBookingsResult, error)
	GetBookingByUserID(ctx context.Context, bookingID int64, userID int64) (sqlc.Booking, error)
	CancelBooking(ctx context.Context, bookingID int64, userID int64) (sqlc.Booking, error)
	ListAvailableRoomTypes(ctx context.Context, input booking.AvailabilityInput) ([]sqlc.ListAvailableRoomTypesRow, error)
	SearchAvailableHotels(ctx context.Context, input booking.HotelSearchInput) (booking.HotelSearchResult, error)
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
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
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
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		http.Error(w, "Idempotency-Key header is required", http.StatusBadRequest)
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
			UserID:         principal.UserID,
			RoomTypeID:     convID,
			CheckIn:        checkIn,
			CheckOut:       checkOut,
			RoomsCount:     req.RoomsCount,
			GuestCount:     req.GuestCount,
			IdempotencyKey: idempotencyKey,
		},
	)
	switch {
	case errors.Is(err, booking.ErrInvalidDates),
		errors.Is(err, booking.ErrInvalidRooms),
		errors.Is(err, booking.ErrInvalidGuests),
		errors.Is(err, booking.ErrCheckInInPast),
		errors.Is(err, booking.ErrCapacityExceeded),
		errors.Is(err, booking.ErrInvalidIdempotencyKey):
		http.Error(w, err.Error(), http.StatusBadRequest)

	case errors.Is(err, booking.ErrRoomTypeNotFound):
		http.Error(w, "Room type not found", http.StatusNotFound)

	case errors.Is(err, booking.ErrUnavailable):
		http.Error(w, "Requested rooms are unavailable", http.StatusConflict)
	case errors.Is(err, booking.ErrIdempotencyKeyConflict):
		http.Error(w, "Idempotency key hash conflict", http.StatusConflict)

	case errors.Is(err, booking.ErrInvalidUser):
		http.Error(w, "Unauthorized", http.StatusUnauthorized)

	case err != nil:
		http.Error(w, "Failed to create booking", http.StatusInternalServerError)

	default:
		response, err := bookingResponseFromModel(created)
		if err != nil {
			http.Error(w, "Failed to format booking", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(response)
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
	response, err := bookingResponseFromModel(getBooking)
	if err != nil {
		http.Error(w, "Failed to format booking", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
func (h *BookingHandler) ListBookingsByUser(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var err error
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	page := int64(booking.DefaultBookingPage)
	if rawPage := query.Get("page"); rawPage != "" {
		page, err = strconv.ParseInt(rawPage, 10, 32)
		if err != nil || page <= 0 {
			http.Error(w, "page must be a positive integer", http.StatusBadRequest)
			return
		}
	}

	pageSize := int64(booking.DefaultBookingPageSize)
	if rawPageSize := query.Get("page_size"); rawPageSize != "" {
		pageSize, err = strconv.ParseInt(rawPageSize, 10, 32)
		if err != nil || pageSize <= 0 || pageSize > int64(booking.MaxBookingPageSize) {
			http.Error(w, "page_size must be between 1 and 100", http.StatusBadRequest)
			return
		}
	}
	listBooking, err := h.service.ListBookingsByUser(r.Context(), booking.ListBookingsInput{
		UserID:   principal.UserID,
		Page:     int32(page),
		PageSize: int32(pageSize),
	})
	if errors.Is(err, booking.ErrInvalidUser) {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	if errors.Is(err, booking.ErrInvalidPage) || errors.Is(err, booking.ErrInvalidPageSize) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "list booking failed", http.StatusInternalServerError)
		return
	}
	response, err := listBookingsResponseFromResult(listBooking)
	if err != nil {
		http.Error(w, "Failed to format bookings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
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
		response, err := bookingResponseFromModel(cancelBooking)
		if err != nil {
			http.Error(w, "Failed to format booking", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}
func (h *BookingHandler) ListAvailableRoomTypes(w http.ResponseWriter, r *http.Request) {
	hotelID, err := strconv.ParseInt(
		r.PathValue("hotelID"),
		10,
		64,
	)
	if err != nil || hotelID <= 0 {
		http.Error(w, "invalid hotel ID", http.StatusBadRequest)
		return
	}
	query := r.URL.Query()

	checkIn, err := time.Parse(
		time.DateOnly,
		query.Get("check_in"),
	)
	if err != nil {
		http.Error(
			w,
			"check_in must use YYYY-MM-DD",
			http.StatusBadRequest,
		)
		return
	}

	checkOut, err := time.Parse(
		time.DateOnly,
		query.Get("check_out"),
	)
	if err != nil {
		http.Error(
			w,
			"check_out must use YYYY-MM-DD",
			http.StatusBadRequest,
		)
		return
	}

	roomsCount, err := strconv.ParseInt(
		query.Get("rooms_count"),
		10,
		32,
	)
	if err != nil || roomsCount <= 0 {
		http.Error(
			w,
			"rooms_count must be a positive integer",
			http.StatusBadRequest,
		)
		return
	}

	guestCount, err := strconv.ParseInt(
		query.Get("guest_count"),
		10,
		32,
	)
	if err != nil || guestCount <= 0 {
		http.Error(
			w,
			"guest_count must be a positive integer",
			http.StatusBadRequest,
		)
		return
	}
	search, err := h.service.ListAvailableRoomTypes(r.Context(), booking.AvailabilityInput{
		HotelID:    hotelID,
		CheckIn:    checkIn,
		CheckOut:   checkOut,
		RoomsCount: int32(roomsCount),
		GuestCount: int32(guestCount),
	})
	switch {
	case errors.Is(err, booking.ErrInvalidHotelID),
		errors.Is(err, booking.ErrInvalidDates),
		errors.Is(err, booking.ErrInvalidRooms),
		errors.Is(err, booking.ErrCheckInInPast),
		errors.Is(err, booking.ErrInvalidGuests):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return

	case err != nil:
		http.Error(
			w,
			"search available room types failed",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(search)
}
func (h *BookingHandler) SearchAvailableHotels(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	city := strings.TrimSpace(query.Get("city"))
	if city == "" {
		http.Error(w, "city must not be empty", http.StatusBadRequest)
		return
	}
	checkIn, err := time.Parse(time.DateOnly, query.Get("check_in"))
	if err != nil {
		http.Error(
			w,
			"check_in must use YYYY-MM-DD",
			http.StatusBadRequest,
		)
		return
	}

	checkOut, err := time.Parse(time.DateOnly, query.Get("check_out"))
	if err != nil {
		http.Error(
			w,
			"check_out must use YYYY-MM-DD",
			http.StatusBadRequest,
		)
		return
	}

	roomsCount, err := strconv.ParseInt(query.Get("rooms_count"), 10, 32)
	if err != nil || roomsCount <= 0 {
		http.Error(
			w,
			"rooms_count must be a positive integer",
			http.StatusBadRequest,
		)
		return
	}

	guestCount, err := strconv.ParseInt(query.Get("guest_count"), 10, 32)
	if err != nil || guestCount <= 0 {
		http.Error(
			w,
			"guest_count must be a positive integer",
			http.StatusBadRequest,
		)
		return
	}

	page := int64(booking.DefaultHotelSearchPage)
	if rawPage := query.Get("page"); rawPage != "" {
		page, err = strconv.ParseInt(rawPage, 10, 32)
		if err != nil || page <= 0 {
			http.Error(w, "page must be a positive integer", http.StatusBadRequest)
			return
		}
	}

	pageSize := int64(booking.DefaultHotelSearchPageSize)
	if rawPageSize := query.Get("page_size"); rawPageSize != "" {
		pageSize, err = strconv.ParseInt(rawPageSize, 10, 32)
		if err != nil || pageSize <= 0 || pageSize > int64(booking.MaxHotelSearchPageSize) {
			http.Error(w, "page_size must be between 1 and 100", http.StatusBadRequest)
			return
		}
	}

	sort := query.Get("sort")
	if sort == "" {
		sort = booking.HotelSearchSortPriceAsc
	}
	if sort != booking.HotelSearchSortPriceAsc && sort != booking.HotelSearchSortPriceDesc {
		http.Error(w, "sort must be price_asc or price_desc", http.StatusBadRequest)
		return
	}

	search, err := h.service.SearchAvailableHotels(r.Context(), booking.HotelSearchInput{
		City:       city,
		CheckIn:    checkIn,
		CheckOut:   checkOut,
		RoomsCount: int32(roomsCount),
		GuestCount: int32(guestCount),
		Page:       int32(page),
		PageSize:   int32(pageSize),
		Sort:       sort,
	})
	switch {
	case errors.Is(err, booking.ErrInvalidCity),
		errors.Is(err, booking.ErrInvalidDates),
		errors.Is(err, booking.ErrInvalidRooms),
		errors.Is(err, booking.ErrCheckInInPast),
		errors.Is(err, booking.ErrInvalidGuests),
		errors.Is(err, booking.ErrInvalidPage),
		errors.Is(err, booking.ErrInvalidPageSize),
		errors.Is(err, booking.ErrInvalidSort):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return

	case err != nil:
		http.Error(
			w,
			"search available hotels failed",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(search)
}

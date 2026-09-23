package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/khangtran2403/ryoko/internal/admin_booking"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type adminBookingService interface {
	ListBookingsForAdmin(ctx context.Context, input admin_booking.ListBookingsForAdminInput) (admin_booking.ListBookingsForAdminResult, error)
	ListBookingHistoryForAdmin(ctx context.Context, bookingID int64) ([]sqlc.BookingStatusHistory, error)
}
type adminBookingCancellationService interface {
	CancelBookingAsAdmin(ctx context.Context, input booking.AdminCancellationInput) (sqlc.Booking, error)
}
type AdminCancelBookingRequest struct {
	Reason string `json:"reason"`
}
type AdminBookingHandler struct {
	adminBookingService             adminBookingService
	adminBookingCancellationService adminBookingCancellationService
}

func NewAdminBookingHandler(adminBookingService adminBookingService, adminBookingCancellationService adminBookingCancellationService) *AdminBookingHandler {
	return &AdminBookingHandler{
		adminBookingService:             adminBookingService,
		adminBookingCancellationService: adminBookingCancellationService,
	}
}

func (h *AdminBookingHandler) ListBookingsForAdmin(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var err error
	var hotelID *int64
	var t_in, t_out *time.Time
	var status *string
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if principal.Role != auth.RoleAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if hotelIDStr := query.Get("hotel_id"); hotelIDStr != "" {
		hotelID = new(int64)
		*hotelID, err = strconv.ParseInt(hotelIDStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid hotel_id", http.StatusBadRequest)
			return
		}
	}
	if rawstatus := query.Get("status"); rawstatus != "" {
		status = &rawstatus
	}
	checkInFrom := query.Get("check_in_from")
	checkInTo := query.Get("check_in_to")
	if checkInFrom != "" {
		t_in = new(time.Time)
		*t_in, err = time.Parse("2006-01-02", checkInFrom)
		if err != nil {
			http.Error(w, "invalid check_in_from date format", http.StatusBadRequest)
			return
		}
	}
	if checkInTo != "" {
		t_out = new(time.Time)
		*t_out, err = time.Parse("2006-01-02", checkInTo)
		if err != nil {
			http.Error(w, "invalid check_in_to date format", http.StatusBadRequest)
			return
		}
	}
	page := int64(admin_booking.DefaultBookingPage)
	if rawPage := query.Get("page"); rawPage != "" {
		page, err = strconv.ParseInt(rawPage, 10, 32)
		if err != nil || page <= 0 {
			http.Error(w, "page must be a positive integer", http.StatusBadRequest)
			return
		}
	}

	pageSize := int64(admin_booking.DefaultBookingPageSize)
	if rawPageSize := query.Get("page_size"); rawPageSize != "" {
		pageSize, err = strconv.ParseInt(rawPageSize, 10, 32)
		if err != nil || pageSize <= 0 || pageSize > int64(admin_booking.MaxBookingPageSize) {
			http.Error(w, "page_size must be between 1 and 100", http.StatusBadRequest)
			return
		}
	}
	listBooking, err := h.adminBookingService.ListBookingsForAdmin(r.Context(), admin_booking.ListBookingsForAdminInput{
		HotelID:     hotelID,
		Status:      status,
		CheckInFrom: t_in,
		CheckInTo:   t_out,
		Page:        int32(page),
		PageSize:    int32(pageSize),
	})
	if errors.Is(err, admin_booking.ErrInvalidHotelID) || errors.Is(err, admin_booking.ErrInvalidStatus) || errors.Is(err, admin_booking.ErrInvalidDateRange) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, admin_booking.ErrInvalidPage) || errors.Is(err, admin_booking.ErrInvalidPageSize) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "list booking failed", http.StatusInternalServerError)
		return
	}
	response, err := listBookingsAdminResponseFromResult(listBooking)
	if err != nil {
		http.Error(w, "Failed to format bookings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
func (h *AdminBookingHandler) ListBookingHistoryForAdmin(w http.ResponseWriter, r *http.Request) {
	bookingIDStr := r.PathValue("bookingID")
	bookingID, err := strconv.ParseInt(bookingIDStr, 10, 64)
	if err != nil || bookingID <= 0 {
		http.Error(w, "invalid booking ID", http.StatusBadRequest)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if principal.Role != auth.RoleAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	history, err := h.adminBookingService.ListBookingHistoryForAdmin(r.Context(), bookingID)
	if errors.Is(err, admin_booking.ErrBookingNotFound) {
		http.Error(w, "Booking not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to list booking status history", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(history)
}
func (h *AdminBookingHandler) AdminCancellation(w http.ResponseWriter, r *http.Request) {
	var req AdminCancelBookingRequest
	getbookingID := r.PathValue("bookingID")
	if getbookingID == "" {
		http.Error(w, "booking ID is required", http.StatusBadRequest)
		return
	}
	bookingID, err := strconv.ParseInt(getbookingID, 10, 64)
	if err != nil || bookingID <= 0 {
		http.Error(w, "invalid booking ID", http.StatusBadRequest)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if principal.Role != auth.RoleAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	cancelled, err := h.adminBookingCancellationService.CancelBookingAsAdmin(
		r.Context(),
		booking.AdminCancellationInput{
			BookingID: bookingID,
			AdminID:   principal.UserID,
			Reason:    req.Reason,
		},
	)
	switch {
	case errors.Is(err, booking.ErrInvalidCancellationReason):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, booking.ErrBookingNotFound):
		http.Error(w, "Booking not found", http.StatusNotFound)
		return
	case errors.Is(err, booking.ErrBookingNotCancellable):
		http.Error(w, "Booking cannot be canceled", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "Failed to cancel booking", http.StatusInternalServerError)
		return
	default:
		response, err := bookingResponseFromModel(cancelled)
		if err != nil {
			http.Error(w, "Failed to format bookings", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}

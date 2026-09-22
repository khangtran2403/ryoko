package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type fakeBookingCreator struct {
	called             bool
	input              booking.CreateInput
	booking            sqlc.Booking
	err                error
	getCalled          bool
	bookingID          int64
	userID             int64
	getBooking         sqlc.Booking
	getErr             error
	listCalled         bool
	listInput          booking.ListBookingsInput
	listResult         booking.ListBookingsResult
	listErr            error
	cancelCalled       bool
	cancelBooking      sqlc.Booking
	cancelErr          error
	availabilityCalled bool
	availabilityInput  booking.AvailabilityInput
	availabilityResult []sqlc.ListAvailableRoomTypesRow
	availabilityErr    error
	hotelSearchCalled  bool
	hotelSearchInput   booking.HotelSearchInput
	hotelSearchResult  booking.HotelSearchResult
	hotelSearchErr     error
	historyCalled      bool
	historyBookingID   int64
	historyUserID      int64
	historyResult      []sqlc.BookingStatusHistory
	historyErr         error
}

func (f *fakeBookingCreator) CreateBooking(_ context.Context, input booking.CreateInput) (sqlc.Booking, error) {
	f.called = true
	f.input = input
	return f.booking, f.err
}

func (f *fakeBookingCreator) GetBookingByUserID(
	_ context.Context,
	bookingID int64,
	userID int64,
) (sqlc.Booking, error) {
	f.getCalled = true
	f.bookingID = bookingID
	f.userID = userID
	return f.getBooking, f.getErr
}

func (f *fakeBookingCreator) ListBookingsByUser(
	_ context.Context,
	input booking.ListBookingsInput,
) (booking.ListBookingsResult, error) {
	f.listCalled = true
	f.listInput = input
	return f.listResult, f.listErr
}

func (f *fakeBookingCreator) CancelBooking(
	_ context.Context,
	bookingID int64,
	userID int64,
) (sqlc.Booking, error) {
	f.cancelCalled = true
	f.bookingID = bookingID
	f.userID = userID
	return f.cancelBooking, f.cancelErr
}

func (f *fakeBookingCreator) ListBookingsHistoryByUser(
	_ context.Context,
	bookingID int64,
	userID int64,
) ([]sqlc.BookingStatusHistory, error) {
	f.historyCalled = true
	f.historyBookingID = bookingID
	f.historyUserID = userID
	return f.historyResult, f.historyErr
}

func TestBookingHandlerCreateBooking(t *testing.T) {
	service := &fakeBookingCreator{
		booking: bookingTestModel(99, 42, 7, "confirmed"),
	}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	body := `{
		"check_in":"2026-09-10",
		"check_out":"2026-09-13",
		"rooms_count":2,
		"guest_count":3,
		"user_id":999,
		"status":"cancelled"
	}`
	recorder := performBookingRequest(mux, token, "/room-types/7/bookings", body)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	if !service.called {
		t.Fatal("booking service was not called")
	}
	if service.input.UserID != 42 {
		t.Errorf("UserID = %d, want authenticated user ID 42", service.input.UserID)
	}
	if service.input.RoomTypeID != 7 {
		t.Errorf("RoomTypeID = %d, want 7", service.input.RoomTypeID)
	}
	if service.input.RoomsCount != 2 {
		t.Errorf("RoomsCount = %d, want 2", service.input.RoomsCount)
	}
	if service.input.GuestCount != 3 {
		t.Errorf("GuestCount = %d, want 3", service.input.GuestCount)
	}
	if service.input.IdempotencyKey != "test-idempotency-key" {
		t.Errorf("IdempotencyKey = %q, want test-idempotency-key", service.input.IdempotencyKey)
	}
	assertDate(t, service.input.CheckIn, "2026-09-10")
	assertDate(t, service.input.CheckOut, "2026-09-13")

	var response BookingResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 99 {
		t.Errorf("response booking ID = %d, want 99", response.ID)
	}
	if response.NumberOfNights != 3 || response.PricePerNight != "100.00" || response.TotalPrice != "300.00" {
		t.Errorf("response price breakdown = %+v", response)
	}
}

func TestBookingHandlerCreateBookingRejectsUnauthenticatedRequest(t *testing.T) {
	service := &fakeBookingCreator{}
	mux, _ := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingRequest(mux, "", "/room-types/7/bookings", validBookingBody())

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if recorder.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", recorder.Header().Get("WWW-Authenticate"))
	}
	if service.called {
		t.Fatal("booking service was called for an unauthenticated request")
	}
}

func TestBookingHandlerCreateBookingRejectsMissingPrincipal(t *testing.T) {
	service := &fakeBookingCreator{}
	handler := NewBookingHandler(service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/room-types/7/bookings", strings.NewReader(validBookingBody()))
	request.SetPathValue("roomTypeID", "7")

	handler.CreateBooking(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if recorder.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", recorder.Header().Get("WWW-Authenticate"))
	}
	if service.called {
		t.Fatal("booking service was called without a principal in the request context")
	}
}

func TestBookingHandlerCreateBookingRequiresIdempotencyKey(t *testing.T) {
	service := &fakeBookingCreator{}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingRequestWithKey(
		mux,
		token,
		"/room-types/7/bookings",
		validBookingBody(),
		"",
	)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if service.called {
		t.Fatal("booking service was called without an idempotency key")
	}
}

func TestBookingHandlerCreateBookingRejectsInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{
			name: "invalid room type ID",
			path: "/room-types/not-a-number/bookings",
			body: validBookingBody(),
		},
		{
			name: "non-positive room type ID",
			path: "/room-types/0/bookings",
			body: validBookingBody(),
		},
		{
			name: "malformed JSON",
			path: "/room-types/7/bookings",
			body: `{"check_in":`,
		},
		{
			name: "invalid check-in date",
			path: "/room-types/7/bookings",
			body: `{"check_in":"10-09-2026","check_out":"2026-09-13","rooms_count":1,"guest_count":2}`,
		},
		{
			name: "invalid check-out date",
			path: "/room-types/7/bookings",
			body: `{"check_in":"2026-09-10","check_out":"13-09-2026","rooms_count":1,"guest_count":2}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{}
			mux, token := newAuthenticatedBookingMux(t, service, 42)

			recorder := performBookingRequest(mux, token, tt.path, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.called {
				t.Fatal("booking service was called for an invalid request")
			}
		})
	}
}

func TestBookingHandlerCreateBookingMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid dates", err: booking.ErrInvalidDates, wantStatus: http.StatusBadRequest},
		{name: "check-in in past", err: booking.ErrCheckInInPast, wantStatus: http.StatusBadRequest},
		{name: "invalid rooms", err: booking.ErrInvalidRooms, wantStatus: http.StatusBadRequest},
		{name: "invalid guests", err: booking.ErrInvalidGuests, wantStatus: http.StatusBadRequest},
		{name: "capacity exceeded", err: booking.ErrCapacityExceeded, wantStatus: http.StatusBadRequest},
		{name: "room type not found", err: booking.ErrRoomTypeNotFound, wantStatus: http.StatusNotFound},
		{name: "unavailable", err: booking.ErrUnavailable, wantStatus: http.StatusConflict},
		{name: "idempotency conflict", err: booking.ErrIdempotencyKeyConflict, wantStatus: http.StatusConflict},
		{name: "invalid idempotency key", err: booking.ErrInvalidIdempotencyKey, wantStatus: http.StatusBadRequest},
		{name: "invalid user", err: booking.ErrInvalidUser, wantStatus: http.StatusUnauthorized},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{err: tt.err}
			mux, token := newAuthenticatedBookingMux(t, service, 42)

			recorder := performBookingRequest(mux, token, "/room-types/7/bookings", validBookingBody())

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.called {
				t.Fatal("booking service was not called")
			}
		})
	}
}

func TestBookingHandlerGetBookingByUserID(t *testing.T) {
	service := &fakeBookingCreator{
		getBooking: bookingTestModel(88, 42, 7, "confirmed"),
	}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings/88")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	if !service.getCalled {
		t.Fatal("booking service was not called")
	}
	if service.bookingID != 88 {
		t.Errorf("booking ID = %d, want 88", service.bookingID)
	}
	if service.userID != 42 {
		t.Errorf("user ID = %d, want authenticated user ID 42", service.userID)
	}

	var response BookingResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 88 {
		t.Errorf("response booking ID = %d, want 88", response.ID)
	}
}

func TestBookingHandlerGetBookingByUserIDRejectsInvalidID(t *testing.T) {
	service := &fakeBookingCreator{}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings/not-a-number")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if service.getCalled {
		t.Fatal("booking service was called for an invalid booking ID")
	}
}

func TestBookingHandlerGetBookingByUserIDMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "booking not found",
			err:        booking.ErrBookingNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unexpected error",
			err:        errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{getErr: tt.err}
			mux, token := newAuthenticatedBookingMux(t, service, 42)

			recorder := performBookingGET(mux, token, "/me/bookings/88")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.getCalled {
				t.Fatal("booking service was not called")
			}
		})
	}
}

func TestBookingHandlerListBookingsByUser(t *testing.T) {
	service := &fakeBookingCreator{
		listResult: booking.ListBookingsResult{
			Bookings: []sqlc.Booking{
				bookingTestModel(91, 42, 7, "confirmed"),
				bookingTestModel(90, 42, 8, "completed"),
			},
			Pagination: booking.BookingPagination{
				Page:     2,
				PageSize: 2,
				HasMore:  true,
			},
		},
	}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings?page=2&page_size=2")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	if !service.listCalled {
		t.Fatal("booking service was not called")
	}
	if service.listInput.UserID != 42 ||
		service.listInput.Page != 2 ||
		service.listInput.PageSize != 2 {
		t.Errorf("list input = %+v, want user=42 page=2 page_size=2", service.listInput)
	}

	var response ListBookingsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Bookings) != 2 {
		t.Fatalf("response length = %d, want 2", len(response.Bookings))
	}
	if response.Bookings[0].ID != 91 || response.Bookings[1].ID != 90 {
		t.Errorf("response booking IDs = [%d, %d], want [91, 90]", response.Bookings[0].ID, response.Bookings[1].ID)
	}
	if response.Pagination.Page != 2 ||
		response.Pagination.PageSize != 2 ||
		!response.Pagination.HasMore {
		t.Errorf("pagination = %+v", response.Pagination)
	}
}

func TestBookingHandlerListBookingsByUserReturnsEmptyArray(t *testing.T) {
	service := &fakeBookingCreator{listResult: booking.ListBookingsResult{
		Bookings: []sqlc.Booking{},
		Pagination: booking.BookingPagination{
			Page:     booking.DefaultBookingPage,
			PageSize: booking.DefaultBookingPageSize,
		},
	}}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if service.listInput.Page != booking.DefaultBookingPage ||
		service.listInput.PageSize != booking.DefaultBookingPageSize {
		t.Errorf("default pagination input = %+v", service.listInput)
	}
	var response ListBookingsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Bookings == nil || len(response.Bookings) != 0 {
		t.Errorf("bookings = %+v, want initialized empty array", response.Bookings)
	}
}

func TestBookingHandlerListBookingsByUserHandlesServiceError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid user", err: booking.ErrInvalidUser, wantStatus: http.StatusBadRequest},
		{name: "invalid page", err: booking.ErrInvalidPage, wantStatus: http.StatusBadRequest},
		{name: "invalid page size", err: booking.ErrInvalidPageSize, wantStatus: http.StatusBadRequest},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{listErr: tt.err}
			mux, token := newAuthenticatedBookingMux(t, service, 42)
			recorder := performBookingGET(mux, token, "/me/bookings")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.listCalled {
				t.Fatal("booking service was not called")
			}
		})
	}
}

func TestBookingHandlerListBookingsByUserRejectsInvalidPagination(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "non-numeric page", path: "/me/bookings?page=first"},
		{name: "non-positive page", path: "/me/bookings?page=0"},
		{name: "page overflow", path: "/me/bookings?page=2147483648"},
		{name: "non-numeric page size", path: "/me/bookings?page_size=many"},
		{name: "non-positive page size", path: "/me/bookings?page_size=0"},
		{name: "page size above maximum", path: "/me/bookings?page_size=101"},
		{name: "page size overflow", path: "/me/bookings?page_size=2147483648"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{}
			mux, token := newAuthenticatedBookingMux(t, service, 42)
			recorder := performBookingGET(mux, token, tt.path)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.listCalled {
				t.Fatal("booking service was called for invalid pagination")
			}
		})
	}
}

func TestBookingHandlerReadEndpointsRejectUnauthenticatedRequests(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "get booking", path: "/me/bookings/88"},
		{name: "list bookings", path: "/me/bookings"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{}
			mux, _ := newAuthenticatedBookingMux(t, service, 42)

			recorder := performBookingGET(mux, "", tt.path)

			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
			}
			if recorder.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want Bearer", recorder.Header().Get("WWW-Authenticate"))
			}
			if service.getCalled || service.listCalled {
				t.Fatal("booking service was called for an unauthenticated request")
			}
		})
	}
}

func TestBookingHandlerCancelBooking(t *testing.T) {
	service := &fakeBookingCreator{
		cancelBooking: bookingTestModel(88, 42, 7, "cancelled"),
	}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingRequest(mux, token, "/me/bookings/88/cancel", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	if !service.cancelCalled {
		t.Fatal("booking service was not called")
	}
	if service.bookingID != 88 {
		t.Errorf("booking ID = %d, want 88", service.bookingID)
	}
	if service.userID != 42 {
		t.Errorf("user ID = %d, want authenticated user ID 42", service.userID)
	}

	var response BookingResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 88 || response.Status != "cancelled" {
		t.Errorf("response = {ID:%d Status:%q}, want {ID:88 Status:cancelled}", response.ID, response.Status)
	}
}

func TestBookingHandlerCancelBookingRejectsInvalidID(t *testing.T) {
	service := &fakeBookingCreator{}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingRequest(mux, token, "/me/bookings/not-a-number/cancel", "")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if service.cancelCalled {
		t.Fatal("booking service was called for an invalid booking ID")
	}
}
func (f *fakeBookingCreator) ListAvailableRoomTypes(
	_ context.Context,
	input booking.AvailabilityInput,
) ([]sqlc.ListAvailableRoomTypesRow, error) {
	f.availabilityCalled = true
	f.availabilityInput = input
	return f.availabilityResult, f.availabilityErr
}

func (f *fakeBookingCreator) SearchAvailableHotels(
	_ context.Context,
	input booking.HotelSearchInput,
) (booking.HotelSearchResult, error) {
	f.hotelSearchCalled = true
	f.hotelSearchInput = input
	return f.hotelSearchResult, f.hotelSearchErr
}

func TestBookingHandlerListAvailableRoomTypes(t *testing.T) {
	service := &fakeBookingCreator{availabilityResult: []sqlc.ListAvailableRoomTypesRow{
		{ID: 7, HotelID: 12, Name: "Standard", TotalRooms: 5, RoomsAvailable: 2},
	}}
	mux, _ := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(
		mux,
		"",
		"/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=2&guest_count=3",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.availabilityCalled {
		t.Fatal("availability service was not called")
	}
	if service.availabilityInput.HotelID != 12 ||
		service.availabilityInput.RoomsCount != 2 ||
		service.availabilityInput.GuestCount != 3 {
		t.Errorf("availability input = %+v", service.availabilityInput)
	}
	assertDate(t, service.availabilityInput.CheckIn, "2030-01-10")
	assertDate(t, service.availabilityInput.CheckOut, "2030-01-13")
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}

	var response []sqlc.ListAvailableRoomTypesRow
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 1 || response[0].ID != 7 || response[0].RoomsAvailable != 2 {
		t.Errorf("response = %+v", response)
	}
}

func TestBookingHandlerListAvailableRoomTypesRejectsInvalidQuery(t *testing.T) {
	validQuery := "?check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2"
	tests := []struct {
		name string
		path string
	}{
		{name: "invalid hotel ID", path: "/hotels/nope/available-room-types" + validQuery},
		{name: "non-positive hotel ID", path: "/hotels/0/available-room-types" + validQuery},
		{name: "missing check-in", path: "/hotels/12/available-room-types?check_out=2030-01-13&rooms_count=1&guest_count=2"},
		{name: "invalid check-in", path: "/hotels/12/available-room-types?check_in=10-01-2030&check_out=2030-01-13&rooms_count=1&guest_count=2"},
		{name: "missing check-out", path: "/hotels/12/available-room-types?check_in=2030-01-10&rooms_count=1&guest_count=2"},
		{name: "invalid check-out", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=13-01-2030&rooms_count=1&guest_count=2"},
		{name: "missing rooms", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&guest_count=2"},
		{name: "non-numeric rooms", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=many&guest_count=2"},
		{name: "non-positive rooms", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=0&guest_count=2"},
		{name: "rooms overflow", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=2147483648&guest_count=2"},
		{name: "missing guests", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=1"},
		{name: "non-numeric guests", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=many"},
		{name: "non-positive guests", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=-1"},
		{name: "guests overflow", path: "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2147483648"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{}
			mux, _ := newAuthenticatedBookingMux(t, service, 42)
			recorder := performBookingGET(mux, "", tt.path)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.availabilityCalled {
				t.Fatal("availability service was called for invalid query")
			}
		})
	}
}

func TestBookingHandlerListAvailableRoomTypesMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid hotel", err: booking.ErrInvalidHotelID, wantStatus: http.StatusBadRequest},
		{name: "invalid dates", err: booking.ErrInvalidDates, wantStatus: http.StatusBadRequest},
		{name: "check-in in past", err: booking.ErrCheckInInPast, wantStatus: http.StatusBadRequest},
		{name: "invalid rooms", err: booking.ErrInvalidRooms, wantStatus: http.StatusBadRequest},
		{name: "invalid guests", err: booking.ErrInvalidGuests, wantStatus: http.StatusBadRequest},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}
	path := "/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{availabilityErr: tt.err}
			mux, _ := newAuthenticatedBookingMux(t, service, 42)
			recorder := performBookingGET(mux, "", path)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.availabilityCalled {
				t.Fatal("availability service was not called")
			}
		})
	}
}

func TestBookingHandlerListAvailableRoomTypesReturnsEmptyArray(t *testing.T) {
	service := &fakeBookingCreator{availabilityResult: []sqlc.ListAvailableRoomTypesRow{}}
	mux, _ := newAuthenticatedBookingMux(t, service, 42)
	recorder := performBookingGET(
		mux,
		"",
		"/hotels/12/available-room-types?check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestBookingHandlerSearchAvailableHotels(t *testing.T) {
	service := &fakeBookingCreator{
		hotelSearchResult: booking.HotelSearchResult{
			Hotels: []sqlc.SearchAvailableHotelsRow{
				{
					ID:                     12,
					Name:                   "Riverside Hotel",
					City:                   "Da Nang",
					AvailableRoomTypeCount: 2,
				},
			},
			Pagination: booking.HotelSearchPagination{
				Page:     2,
				PageSize: 1,
				HasMore:  true,
			},
		},
	}
	mux, _ := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(
		mux,
		"",
		"/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=2&guest_count=3&page=2&page_size=1&sort=price_desc",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.hotelSearchCalled {
		t.Fatal("hotel search service was not called")
	}
	if service.hotelSearchInput.City != "Da Nang" ||
		service.hotelSearchInput.RoomsCount != 2 ||
		service.hotelSearchInput.GuestCount != 3 ||
		service.hotelSearchInput.Page != 2 ||
		service.hotelSearchInput.PageSize != 1 ||
		service.hotelSearchInput.Sort != booking.HotelSearchSortPriceDesc {
		t.Errorf("hotel search input = %+v", service.hotelSearchInput)
	}
	assertDate(t, service.hotelSearchInput.CheckIn, "2030-01-10")
	assertDate(t, service.hotelSearchInput.CheckOut, "2030-01-13")
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}

	var response booking.HotelSearchResult
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Hotels) != 1 ||
		response.Hotels[0].ID != 12 ||
		response.Hotels[0].AvailableRoomTypeCount != 2 {
		t.Errorf("response = %+v", response)
	}
	if response.Pagination.Page != 2 ||
		response.Pagination.PageSize != 1 ||
		!response.Pagination.HasMore {
		t.Errorf("response pagination = %+v", response.Pagination)
	}
}

func TestBookingHandlerSearchAvailableHotelsRejectsInvalidQuery(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "missing city", path: "/hotels/search?check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2"},
		{name: "whitespace-only city", path: "/hotels/search?city=+++&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2"},
		{name: "missing check-in", path: "/hotels/search?city=Da+Nang&check_out=2030-01-13&rooms_count=1&guest_count=2"},
		{name: "invalid check-in", path: "/hotels/search?city=Da+Nang&check_in=10-01-2030&check_out=2030-01-13&rooms_count=1&guest_count=2"},
		{name: "missing check-out", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&rooms_count=1&guest_count=2"},
		{name: "invalid check-out", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=13-01-2030&rooms_count=1&guest_count=2"},
		{name: "missing rooms", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&guest_count=2"},
		{name: "non-numeric rooms", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=many&guest_count=2"},
		{name: "non-positive rooms", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=0&guest_count=2"},
		{name: "rooms overflow", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=2147483648&guest_count=2"},
		{name: "missing guests", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1"},
		{name: "non-numeric guests", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=many"},
		{name: "non-positive guests", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=-1"},
		{name: "guests overflow", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2147483648"},
		{name: "non-numeric page", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2&page=first"},
		{name: "non-positive page", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2&page=0"},
		{name: "page overflow", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2&page=2147483648"},
		{name: "non-numeric page size", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2&page_size=many"},
		{name: "non-positive page size", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2&page_size=0"},
		{name: "page size above maximum", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2&page_size=101"},
		{name: "page size overflow", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2&page_size=2147483648"},
		{name: "invalid sort", path: "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2&sort=newest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{}
			mux, _ := newAuthenticatedBookingMux(t, service, 42)
			recorder := performBookingGET(mux, "", tt.path)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.hotelSearchCalled {
				t.Fatal("hotel search service was called for invalid query")
			}
		})
	}
}

func TestBookingHandlerSearchAvailableHotelsMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid city", err: booking.ErrInvalidCity, wantStatus: http.StatusBadRequest},
		{name: "invalid dates", err: booking.ErrInvalidDates, wantStatus: http.StatusBadRequest},
		{name: "check-in in past", err: booking.ErrCheckInInPast, wantStatus: http.StatusBadRequest},
		{name: "invalid rooms", err: booking.ErrInvalidRooms, wantStatus: http.StatusBadRequest},
		{name: "invalid guests", err: booking.ErrInvalidGuests, wantStatus: http.StatusBadRequest},
		{name: "invalid page", err: booking.ErrInvalidPage, wantStatus: http.StatusBadRequest},
		{name: "invalid page size", err: booking.ErrInvalidPageSize, wantStatus: http.StatusBadRequest},
		{name: "invalid sort", err: booking.ErrInvalidSort, wantStatus: http.StatusBadRequest},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}
	path := "/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{hotelSearchErr: tt.err}
			mux, _ := newAuthenticatedBookingMux(t, service, 42)
			recorder := performBookingGET(mux, "", path)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.hotelSearchCalled {
				t.Fatal("hotel search service was not called")
			}
		})
	}
}

func TestBookingHandlerSearchAvailableHotelsReturnsEmptyArray(t *testing.T) {
	service := &fakeBookingCreator{
		hotelSearchResult: booking.HotelSearchResult{
			Hotels: []sqlc.SearchAvailableHotelsRow{},
			Pagination: booking.HotelSearchPagination{
				Page:     booking.DefaultHotelSearchPage,
				PageSize: booking.DefaultHotelSearchPageSize,
			},
		},
	}
	mux, _ := newAuthenticatedBookingMux(t, service, 42)
	recorder := performBookingGET(
		mux,
		"",
		"/hotels/search?city=Da+Nang&check_in=2030-01-10&check_out=2030-01-13&rooms_count=1&guest_count=2",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if service.hotelSearchInput.Page != booking.DefaultHotelSearchPage ||
		service.hotelSearchInput.PageSize != booking.DefaultHotelSearchPageSize ||
		service.hotelSearchInput.Sort != booking.HotelSearchSortPriceAsc {
		t.Errorf("default hotel search input = %+v", service.hotelSearchInput)
	}

	var response booking.HotelSearchResult
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Hotels == nil || len(response.Hotels) != 0 {
		t.Errorf("hotels = %+v, want initialized empty array", response.Hotels)
	}
	if response.Pagination.HasMore {
		t.Error("pagination has_more = true, want false")
	}
}
func TestBookingHandlerCancelBookingMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{
			name:       "booking not found",
			err:        booking.ErrBookingNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "booking not cancellable",
			err:        booking.ErrBookingNotCancellable,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "unexpected error",
			err:        errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{cancelErr: tt.err}
			mux, token := newAuthenticatedBookingMux(t, service, 42)

			recorder := performBookingRequest(mux, token, "/me/bookings/88/cancel", "")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.cancelCalled {
				t.Fatal("booking service was not called")
			}
		})
	}
}

func TestBookingHandlerCancelBookingRejectsUnauthenticatedRequest(t *testing.T) {
	service := &fakeBookingCreator{}
	mux, _ := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingRequest(mux, "", "/me/bookings/88/cancel", "")

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if recorder.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", recorder.Header().Get("WWW-Authenticate"))
	}
	if service.cancelCalled {
		t.Fatal("booking service was called for an unauthenticated request")
	}
}

func TestBookingHandlerListsBookingStatusHistory(t *testing.T) {
	service := &fakeBookingCreator{
		historyResult: []sqlc.BookingStatusHistory{
			bookingStatusHistoryTestModel(1, 88, "confirmed"),
			bookingStatusHistoryTestModel(2, 88, "cancelled"),
		},
	}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings/88/history")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	if !service.historyCalled {
		t.Fatal("booking history service was not called")
	}
	if service.historyBookingID != 88 || service.historyUserID != 42 {
		t.Errorf("service IDs = booking %d, user %d; want booking 88, user 42", service.historyBookingID, service.historyUserID)
	}

	var response []struct {
		ID        int64  `json:"id"`
		BookingID int64  `json:"booking_id"`
		ToStatus  string `json:"to_status"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 2 || response[0].ToStatus != "confirmed" || response[1].ToStatus != "cancelled" {
		t.Errorf("response = %+v, want confirmed then cancelled", response)
	}
}

func TestBookingHandlerBookingStatusHistoryMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "booking not found", err: booking.ErrBookingNotFound, wantStatus: http.StatusNotFound},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeBookingCreator{historyErr: tt.err}
			mux, token := newAuthenticatedBookingMux(t, service, 42)

			recorder := performBookingGET(mux, token, "/me/bookings/88/history")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.historyCalled {
				t.Fatal("booking history service was not called")
			}
		})
	}
}

func TestBookingHandlerBookingStatusHistoryRejectsInvalidID(t *testing.T) {
	service := &fakeBookingCreator{}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings/not-a-number/history")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if service.historyCalled {
		t.Fatal("booking history service was called for an invalid booking ID")
	}
}

func TestBookingHandlerBookingStatusHistoryRejectsUnauthenticatedRequest(t *testing.T) {
	service := &fakeBookingCreator{}
	mux, _ := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, "", "/me/bookings/88/history")

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if recorder.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", recorder.Header().Get("WWW-Authenticate"))
	}
	if service.historyCalled {
		t.Fatal("booking history service was called without authentication")
	}
}

func newAuthenticatedBookingMux(t *testing.T, service bookingService, userID int64) (*http.ServeMux, string) {
	t.Helper()

	tokenManager, err := auth.NewTokenManager(
		"test-secret-that-is-at-least-32-bytes-long",
		"ryoko-test",
		"ryoko-test-api",
		15*time.Minute,
	)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}
	token, err := tokenManager.GenerateToken(userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("generate access token: %v", err)
	}

	bookingHandler := NewBookingHandler(service)
	authMiddleware := middleware.NewAuthMiddleware(tokenManager)
	mux := http.NewServeMux()
	mux.Handle(
		"POST /room-types/{roomTypeID}/bookings",
		authMiddleware.Authenticate(http.HandlerFunc(bookingHandler.CreateBooking)),
	)
	mux.Handle(
		"GET /me/bookings/{bookingID}",
		authMiddleware.Authenticate(http.HandlerFunc(bookingHandler.GetBookingByUserID)),
	)
	mux.Handle(
		"GET /me/bookings",
		authMiddleware.Authenticate(http.HandlerFunc(bookingHandler.ListBookingsByUser)),
	)
	mux.Handle(
		"POST /me/bookings/{bookingID}/cancel",
		authMiddleware.Authenticate(http.HandlerFunc(bookingHandler.CancelBooking)),
	)
	mux.Handle(
		"GET /me/bookings/{bookingID}/history",
		authMiddleware.Authenticate(http.HandlerFunc(bookingHandler.ListBookingStatusHistoryForUser)),
	)
	mux.HandleFunc(
		"GET /hotels/{hotelID}/available-room-types",
		bookingHandler.ListAvailableRoomTypes,
	)
	mux.HandleFunc(
		"GET /hotels/search",
		bookingHandler.SearchAvailableHotels,
	)

	return mux, token
}

func performBookingRequest(handler http.Handler, token, path, body string) *httptest.ResponseRecorder {
	return performBookingRequestWithKey(handler, token, path, body, "test-idempotency-key")
}

func performBookingRequestWithKey(
	handler http.Handler,
	token string,
	path string,
	body string,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func performBookingGET(handler http.Handler, token, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func validBookingBody() string {
	return `{"check_in":"2026-09-10","check_out":"2026-09-13","rooms_count":1,"guest_count":2}`
}

func assertDate(t *testing.T, got time.Time, want string) {
	t.Helper()
	wantDate, err := time.Parse(time.DateOnly, want)
	if err != nil {
		t.Fatalf("parse expected date: %v", err)
	}
	if !got.Equal(wantDate) {
		t.Errorf("date = %s, want %s", got.Format(time.DateOnly), want)
	}
}

func bookingTestModel(id, userID, roomTypeID int64, status string) sqlc.Booking {
	var pricePerNight pgtype.Numeric
	if err := pricePerNight.Scan("100.00"); err != nil {
		panic(err)
	}
	var totalPrice pgtype.Numeric
	if err := totalPrice.Scan("300.00"); err != nil {
		panic(err)
	}

	return sqlc.Booking{
		ID:            id,
		UserID:        userID,
		RoomTypeID:    roomTypeID,
		CheckIn:       pgtype.Date{Time: time.Date(2026, time.September, 10, 0, 0, 0, 0, time.UTC), Valid: true},
		CheckOut:      pgtype.Date{Time: time.Date(2026, time.September, 13, 0, 0, 0, 0, time.UTC), Valid: true},
		RoomsCount:    1,
		GuestCount:    2,
		PricePerNight: pricePerNight,
		TotalPrice:    totalPrice,
		Status:        status,
		CreatedAt:     pgtype.Timestamptz{Time: time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC), Valid: true},
		UpdatedAt:     pgtype.Timestamptz{Time: time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC), Valid: true},
	}
}

func bookingStatusHistoryTestModel(id, bookingID int64, toStatus string) sqlc.BookingStatusHistory {
	fromStatus := pgtype.Text{}
	if toStatus != "confirmed" {
		fromStatus = pgtype.Text{String: "confirmed", Valid: true}
	}
	return sqlc.BookingStatusHistory{
		ID:              id,
		BookingID:       bookingID,
		FromStatus:      fromStatus,
		ToStatus:        toStatus,
		ChangedByUserID: pgtype.Int8{Int64: 42, Valid: true},
		Reason:          pgtype.Text{String: "status changed", Valid: true},
		CreatedAt:       pgtype.Timestamptz{Time: time.Date(2030, time.January, int(id), 10, 0, 0, 0, time.UTC), Valid: true},
	}
}

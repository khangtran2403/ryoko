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

	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type fakeBookingCreator struct {
	called        bool
	input         booking.CreateInput
	booking       sqlc.Booking
	err           error
	getCalled     bool
	bookingID     int64
	userID        int64
	getBooking    sqlc.Booking
	getErr        error
	listCalled    bool
	listBookings  []sqlc.Booking
	listErr       error
	cancelCalled  bool
	cancelBooking sqlc.Booking
	cancelErr     error
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
	userID int64,
) ([]sqlc.Booking, error) {
	f.listCalled = true
	f.userID = userID
	return f.listBookings, f.listErr
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

func TestBookingHandlerCreateBooking(t *testing.T) {
	service := &fakeBookingCreator{
		booking: sqlc.Booking{
			ID:         99,
			UserID:     42,
			RoomTypeID: 7,
			RoomsCount: 2,
			GuestCount: 3,
			Status:     "confirmed",
		},
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
	assertDate(t, service.input.CheckIn, "2026-09-10")
	assertDate(t, service.input.CheckOut, "2026-09-13")

	var response sqlc.Booking
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 99 {
		t.Errorf("response booking ID = %d, want 99", response.ID)
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
		{name: "invalid rooms", err: booking.ErrInvalidRooms, wantStatus: http.StatusBadRequest},
		{name: "invalid guests", err: booking.ErrInvalidGuests, wantStatus: http.StatusBadRequest},
		{name: "capacity exceeded", err: booking.ErrCapacityExceeded, wantStatus: http.StatusBadRequest},
		{name: "room type not found", err: booking.ErrRoomTypeNotFound, wantStatus: http.StatusNotFound},
		{name: "unavailable", err: booking.ErrUnavailable, wantStatus: http.StatusConflict},
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
		getBooking: sqlc.Booking{
			ID:         88,
			UserID:     42,
			RoomTypeID: 7,
			RoomsCount: 1,
			GuestCount: 2,
			Status:     "confirmed",
		},
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

	var response sqlc.Booking
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
		listBookings: []sqlc.Booking{
			{ID: 91, UserID: 42, RoomTypeID: 7, Status: "confirmed"},
			{ID: 90, UserID: 42, RoomTypeID: 8, Status: "completed"},
		},
	}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	if !service.listCalled {
		t.Fatal("booking service was not called")
	}
	if service.userID != 42 {
		t.Errorf("user ID = %d, want authenticated user ID 42", service.userID)
	}

	var response []sqlc.Booking
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 2 {
		t.Fatalf("response length = %d, want 2", len(response))
	}
	if response[0].ID != 91 || response[1].ID != 90 {
		t.Errorf("response booking IDs = [%d, %d], want [91, 90]", response[0].ID, response[1].ID)
	}
}

func TestBookingHandlerListBookingsByUserReturnsEmptyArray(t *testing.T) {
	service := &fakeBookingCreator{listBookings: []sqlc.Booking{}}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestBookingHandlerListBookingsByUserHandlesServiceError(t *testing.T) {
	service := &fakeBookingCreator{listErr: errors.New("database unavailable")}
	mux, token := newAuthenticatedBookingMux(t, service, 42)

	recorder := performBookingGET(mux, token, "/me/bookings")

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
	}
	if !service.listCalled {
		t.Fatal("booking service was not called")
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
		cancelBooking: sqlc.Booking{
			ID:         88,
			UserID:     42,
			RoomTypeID: 7,
			RoomsCount: 1,
			GuestCount: 2,
			Status:     "cancelled",
		},
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

	var response sqlc.Booking
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

	return mux, token
}

func performBookingRequest(handler http.Handler, token, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
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

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
	"github.com/khangtran2403/ryoko/internal/admin_booking"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/booking"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type fakeAdminBookingService struct {
	called           bool
	input            admin_booking.ListBookingsForAdminInput
	result           admin_booking.ListBookingsForAdminResult
	err              error
	historyCalled    bool
	historyBookingID int64
	historyResult    []sqlc.BookingStatusHistory
	historyErr       error
	cancelCalled     bool
	cancelInput      booking.AdminCancellationInput
	cancelResult     sqlc.Booking
	cancelErr        error
}

func (f *fakeAdminBookingService) ListBookingsForAdmin(
	_ context.Context,
	input admin_booking.ListBookingsForAdminInput,
) (admin_booking.ListBookingsForAdminResult, error) {
	f.called = true
	f.input = input
	return f.result, f.err
}

func (f *fakeAdminBookingService) ListBookingHistoryForAdmin(
	_ context.Context,
	bookingID int64,
) ([]sqlc.BookingStatusHistory, error) {
	f.historyCalled = true
	f.historyBookingID = bookingID
	return f.historyResult, f.historyErr
}

func (f *fakeAdminBookingService) CancelBookingAsAdmin(
	_ context.Context,
	input booking.AdminCancellationInput,
) (sqlc.Booking, error) {
	f.cancelCalled = true
	f.cancelInput = input
	return f.cancelResult, f.cancelErr
}

func TestAdminBookingHandlerListsFilteredBookings(t *testing.T) {
	service := &fakeAdminBookingService{
		result: admin_booking.ListBookingsForAdminResult{
			Bookings: []sqlc.ListBookingsForAdminRow{adminBookingTestRow()},
			Pagination: admin_booking.BookingPagination{
				Page:     2,
				PageSize: 1,
				HasMore:  true,
			},
		},
	}
	mux, adminToken, _ := newAdminBookingMux(t, service)
	path := "/admin/bookings?hotel_id=12&status=confirmed&check_in_from=2030-01-10&check_in_to=2030-01-20&page=2&page_size=1"

	recorder := performAdminBookingRequest(mux, adminToken, path)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.called {
		t.Fatal("admin booking service was not called")
	}
	if service.input.HotelID == nil || *service.input.HotelID != 12 {
		t.Errorf("HotelID = %v, want 12", service.input.HotelID)
	}
	if service.input.Status == nil || *service.input.Status != "confirmed" {
		t.Errorf("Status = %v, want confirmed", service.input.Status)
	}
	assertAdminBookingDate(t, service.input.CheckInFrom, "2030-01-10")
	assertAdminBookingDate(t, service.input.CheckInTo, "2030-01-20")
	if service.input.Page != 2 || service.input.PageSize != 1 {
		t.Errorf("pagination input = %+v, want page=2 page_size=1", service.input)
	}

	var response ListBookingsAdminResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Bookings) != 1 {
		t.Fatalf("bookings length = %d, want 1", len(response.Bookings))
	}
	got := response.Bookings[0]
	if got.BookingID != 91 || got.CheckIn != "2030-01-10" || got.CheckOut != "2030-01-13" {
		t.Errorf("booking response = %+v", got)
	}
	if got.NumberOfNights != 3 || got.PricePerNight != "100.00" || got.TotalPrice != "300.00" {
		t.Errorf("price breakdown = %+v", got)
	}
	if response.Pagination.Page != 2 || response.Pagination.PageSize != 1 || !response.Pagination.HasMore {
		t.Errorf("pagination response = %+v", response.Pagination)
	}
}

func TestAdminBookingHandlerUsesDefaultsAndNilFilters(t *testing.T) {
	service := &fakeAdminBookingService{
		result: admin_booking.ListBookingsForAdminResult{
			Bookings: []sqlc.ListBookingsForAdminRow{},
			Pagination: admin_booking.BookingPagination{
				Page:     admin_booking.DefaultBookingPage,
				PageSize: admin_booking.DefaultBookingPageSize,
			},
		},
	}
	mux, adminToken, _ := newAdminBookingMux(t, service)

	recorder := performAdminBookingRequest(mux, adminToken, "/admin/bookings")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if service.input.HotelID != nil || service.input.Status != nil ||
		service.input.CheckInFrom != nil || service.input.CheckInTo != nil {
		t.Errorf("optional filters = %+v, want all nil", service.input)
	}
	if service.input.Page != admin_booking.DefaultBookingPage ||
		service.input.PageSize != admin_booking.DefaultBookingPageSize {
		t.Errorf("default pagination input = %+v", service.input)
	}

	var response ListBookingsAdminResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Bookings == nil || len(response.Bookings) != 0 {
		t.Errorf("bookings = %+v, want initialized empty array", response.Bookings)
	}
}

func TestAdminBookingHandlerAcceptsIndependentDateFilters(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantFrom string
		wantTo   string
	}{
		{name: "from only", path: "/admin/bookings?check_in_from=2030-01-10", wantFrom: "2030-01-10"},
		{name: "to only", path: "/admin/bookings?check_in_to=2030-01-20", wantTo: "2030-01-20"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAdminBookingService{}
			mux, adminToken, _ := newAdminBookingMux(t, service)

			recorder := performAdminBookingRequest(mux, adminToken, tt.path)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			if tt.wantFrom == "" && service.input.CheckInFrom != nil {
				t.Errorf("CheckInFrom = %v, want nil", service.input.CheckInFrom)
			}
			if tt.wantFrom != "" {
				assertAdminBookingDate(t, service.input.CheckInFrom, tt.wantFrom)
			}
			if tt.wantTo == "" && service.input.CheckInTo != nil {
				t.Errorf("CheckInTo = %v, want nil", service.input.CheckInTo)
			}
			if tt.wantTo != "" {
				assertAdminBookingDate(t, service.input.CheckInTo, tt.wantTo)
			}
		})
	}
}

func TestAdminBookingHandlerRejectsInvalidQuery(t *testing.T) {
	tests := []string{
		"/admin/bookings?hotel_id=abc",
		"/admin/bookings?check_in_from=10-01-2030",
		"/admin/bookings?check_in_to=20-01-2030",
		"/admin/bookings?page=zero",
		"/admin/bookings?page=0",
		"/admin/bookings?page_size=zero",
		"/admin/bookings?page_size=0",
		"/admin/bookings?page_size=101",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			service := &fakeAdminBookingService{}
			mux, adminToken, _ := newAdminBookingMux(t, service)

			recorder := performAdminBookingRequest(mux, adminToken, path)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.called {
				t.Fatal("service was called for an invalid query")
			}
		})
	}
}

func TestAdminBookingHandlerMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid hotel", err: admin_booking.ErrInvalidHotelID, wantStatus: http.StatusBadRequest},
		{name: "invalid status", err: admin_booking.ErrInvalidStatus, wantStatus: http.StatusBadRequest},
		{name: "invalid date range", err: admin_booking.ErrInvalidDateRange, wantStatus: http.StatusBadRequest},
		{name: "invalid page", err: admin_booking.ErrInvalidPage, wantStatus: http.StatusBadRequest},
		{name: "invalid page size", err: admin_booking.ErrInvalidPageSize, wantStatus: http.StatusBadRequest},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAdminBookingService{err: tt.err}
			mux, adminToken, _ := newAdminBookingMux(t, service)

			recorder := performAdminBookingRequest(mux, adminToken, "/admin/bookings")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.called {
				t.Fatal("service was not called")
			}
		})
	}
}

func TestAdminBookingRouteRequiresAdmin(t *testing.T) {
	tests := []struct {
		name       string
		useToken   func(adminToken, customerToken string) string
		wantStatus int
	}{
		{name: "missing token", useToken: func(_, _ string) string { return "" }, wantStatus: http.StatusUnauthorized},
		{name: "customer token", useToken: func(_, customerToken string) string { return customerToken }, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAdminBookingService{}
			mux, adminToken, customerToken := newAdminBookingMux(t, service)

			recorder := performAdminBookingRequest(
				mux,
				tt.useToken(adminToken, customerToken),
				"/admin/bookings",
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if service.called {
				t.Fatal("service was called without admin authorization")
			}
		})
	}
}

func TestAdminBookingHandlerListsBookingHistory(t *testing.T) {
	service := &fakeAdminBookingService{
		historyResult: []sqlc.BookingStatusHistory{
			bookingStatusHistoryTestModel(1, 91, "confirmed"),
			bookingStatusHistoryTestModel(2, 91, "completed"),
		},
	}
	mux, adminToken, _ := newAdminBookingMux(t, service)

	recorder := performAdminBookingRequest(mux, adminToken, "/admin/bookings/91/history")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	if !service.historyCalled || service.historyBookingID != 91 {
		t.Errorf("history call = %v, booking ID = %d; want called with 91", service.historyCalled, service.historyBookingID)
	}

	var response []struct {
		BookingID int64  `json:"booking_id"`
		ToStatus  string `json:"to_status"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 2 || response[1].ToStatus != "completed" {
		t.Errorf("response = %+v, want two entries ending in completed", response)
	}
}

func TestAdminBookingHandlerBookingHistoryMapsErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid booking ID", path: "/admin/bookings/not-a-number/history", wantStatus: http.StatusBadRequest},
		{name: "booking not found", path: "/admin/bookings/91/history", err: admin_booking.ErrBookingNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "unexpected error", path: "/admin/bookings/91/history", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAdminBookingService{historyErr: tt.err}
			mux, adminToken, _ := newAdminBookingMux(t, service)

			recorder := performAdminBookingRequest(mux, adminToken, tt.path)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.historyCalled != tt.wantCalled {
				t.Errorf("history service called = %v, want %v", service.historyCalled, tt.wantCalled)
			}
		})
	}
}

func TestAdminBookingHistoryRouteRequiresAdmin(t *testing.T) {
	tests := []struct {
		name       string
		useToken   func(adminToken, customerToken string) string
		wantStatus int
	}{
		{name: "missing token", useToken: func(_, _ string) string { return "" }, wantStatus: http.StatusUnauthorized},
		{name: "customer token", useToken: func(_, customerToken string) string { return customerToken }, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAdminBookingService{}
			mux, adminToken, customerToken := newAdminBookingMux(t, service)

			recorder := performAdminBookingRequest(
				mux,
				tt.useToken(adminToken, customerToken),
				"/admin/bookings/91/history",
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if service.historyCalled {
				t.Fatal("history service was called without admin authorization")
			}
		})
	}
}

func TestAdminBookingHandlerCancelsBooking(t *testing.T) {
	service := &fakeAdminBookingService{
		cancelResult: bookingTestModel(91, 42, 7, "cancelled"),
	}
	mux, adminToken, _ := newAdminBookingMux(t, service)

	recorder := performAdminBookingCancellationRequest(
		mux,
		adminToken,
		"/admin/bookings/91/cancel",
		`{"reason":"Emergency maintenance"}`,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.cancelCalled {
		t.Fatal("admin cancellation service was not called")
	}
	if service.cancelInput.BookingID != 91 || service.cancelInput.AdminID != 1 || service.cancelInput.Reason != "Emergency maintenance" {
		t.Errorf("cancellation input = %+v, want booking 91, admin 1, supplied reason", service.cancelInput)
	}

	var response BookingResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 91 || response.Status != "cancelled" {
		t.Errorf("response = {ID:%d Status:%q}, want {ID:91 Status:cancelled}", response.ID, response.Status)
	}
}

func TestAdminBookingHandlerCancellationRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "invalid booking ID", path: "/admin/bookings/not-a-number/cancel", body: `{"reason":"Maintenance"}`},
		{name: "missing body", path: "/admin/bookings/91/cancel", body: ""},
		{name: "malformed JSON", path: "/admin/bookings/91/cancel", body: `{"reason":`},
		{name: "unknown field", path: "/admin/bookings/91/cancel", body: `{"reason":"Maintenance","unexpected":true}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAdminBookingService{}
			mux, adminToken, _ := newAdminBookingMux(t, service)

			recorder := performAdminBookingCancellationRequest(mux, adminToken, tt.path, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.cancelCalled {
				t.Fatal("cancellation service was called for an invalid request")
			}
		})
	}
}

func TestAdminBookingHandlerCancellationMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid reason", err: booking.ErrInvalidCancellationReason, wantStatus: http.StatusBadRequest},
		{name: "booking not found", err: booking.ErrBookingNotFound, wantStatus: http.StatusNotFound},
		{name: "booking not cancellable", err: booking.ErrBookingNotCancellable, wantStatus: http.StatusConflict},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAdminBookingService{cancelErr: tt.err}
			mux, adminToken, _ := newAdminBookingMux(t, service)

			recorder := performAdminBookingCancellationRequest(
				mux,
				adminToken,
				"/admin/bookings/91/cancel",
				`{"reason":"Maintenance"}`,
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.cancelCalled {
				t.Fatal("cancellation service was not called")
			}
		})
	}
}

func TestAdminBookingCancellationRouteRequiresAdmin(t *testing.T) {
	tests := []struct {
		name       string
		useToken   func(adminToken, customerToken string) string
		wantStatus int
	}{
		{name: "missing token", useToken: func(_, _ string) string { return "" }, wantStatus: http.StatusUnauthorized},
		{name: "customer token", useToken: func(_, customerToken string) string { return customerToken }, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAdminBookingService{}
			mux, adminToken, customerToken := newAdminBookingMux(t, service)

			recorder := performAdminBookingCancellationRequest(
				mux,
				tt.useToken(adminToken, customerToken),
				"/admin/bookings/91/cancel",
				`{"reason":"Maintenance"}`,
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if service.cancelCalled {
				t.Fatal("cancellation service was called without admin authorization")
			}
		})
	}
}

func newAdminBookingMux(
	t *testing.T,
	service *fakeAdminBookingService,
) (*http.ServeMux, string, string) {
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
	adminToken, err := tokenManager.GenerateToken(1, auth.RoleAdmin)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	customerToken, err := tokenManager.GenerateToken(2, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("generate customer token: %v", err)
	}

	handler := NewAdminBookingHandler(service, service)
	authMiddleware := middleware.NewAuthMiddleware(tokenManager)
	mux := http.NewServeMux()
	mux.Handle(
		"GET /admin/bookings",
		authMiddleware.Authenticate(
			middleware.RequireRole(auth.RoleAdmin, http.HandlerFunc(handler.ListBookingsForAdmin)),
		),
	)
	mux.Handle(
		"GET /admin/bookings/{bookingID}/history",
		authMiddleware.Authenticate(
			middleware.RequireRole(auth.RoleAdmin, http.HandlerFunc(handler.ListBookingHistoryForAdmin)),
		),
	)
	mux.Handle(
		"POST /admin/bookings/{bookingID}/cancel",
		authMiddleware.Authenticate(
			middleware.RequireRole(auth.RoleAdmin, http.HandlerFunc(handler.AdminCancellation)),
		),
	)
	return mux, adminToken, customerToken
}

func performAdminBookingRequest(handler http.Handler, token, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func performAdminBookingCancellationRequest(
	handler http.Handler,
	token string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func assertAdminBookingDate(t *testing.T, value *time.Time, want string) {
	t.Helper()
	if value == nil {
		t.Fatalf("date = nil, want %s", want)
	}
	if got := value.Format(time.DateOnly); got != want {
		t.Errorf("date = %s, want %s", got, want)
	}
}

func adminBookingTestRow() sqlc.ListBookingsForAdminRow {
	var pricePerNight pgtype.Numeric
	if err := pricePerNight.Scan("100.00"); err != nil {
		panic(err)
	}
	var totalPrice pgtype.Numeric
	if err := totalPrice.Scan("300.00"); err != nil {
		panic(err)
	}
	return sqlc.ListBookingsForAdminRow{
		BookingID:      91,
		UserID:         42,
		CustomerName:   "Booking Customer",
		CustomerEmail:  "customer@example.com",
		RoomTypeID:     7,
		RoomTypeName:   "Standard",
		HotelID:        12,
		HotelName:      "Test Hotel",
		CheckIn:        pgtype.Date{Time: time.Date(2030, time.January, 10, 0, 0, 0, 0, time.UTC), Valid: true},
		CheckOut:       pgtype.Date{Time: time.Date(2030, time.January, 13, 0, 0, 0, 0, time.UTC), Valid: true},
		NumberOfNights: 3,
		RoomsCount:     1,
		GuestCount:     2,
		PricePerNight:  pricePerNight,
		TotalPrice:     totalPrice,
		Status:         "confirmed",
		CreatedAt:      pgtype.Timestamptz{Time: time.Date(2029, time.December, 1, 8, 0, 0, 0, time.UTC), Valid: true},
		UpdatedAt:      pgtype.Timestamptz{Time: time.Date(2029, time.December, 1, 8, 0, 0, 0, time.UTC), Valid: true},
	}
}

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
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
	"github.com/khangtran2403/ryoko/internal/review"
)

type fakeReviewService struct {
	createCalled bool
	createInput  review.CreateReview
	createResult sqlc.Review
	createErr    error
	getCalled    bool
	getReviewID  int64
	getResult    sqlc.GetReviewByIDRow
	getErr       error
	listCalled   bool
	listHotelID  int64
	listResult   []sqlc.ListReviewsByHotelRow
	listErr      error
	updateCalled bool
	updateInput  review.UpdateReviewInput
	updateResult sqlc.Review
	updateErr    error
	deleteCalled bool
	deleteID     int64
	deleteUserID int64
	deleteResult int64
	deleteErr    error
}

func (f *fakeReviewService) UpdateReviewByUser(
	_ context.Context,
	input review.UpdateReviewInput,
) (sqlc.Review, error) {
	f.updateCalled = true
	f.updateInput = input
	return f.updateResult, f.updateErr
}

func (f *fakeReviewService) DeleteReview(
	_ context.Context,
	reviewID int64,
	userID int64,
) (int64, error) {
	f.deleteCalled = true
	f.deleteID = reviewID
	f.deleteUserID = userID
	return f.deleteResult, f.deleteErr
}

func (f *fakeReviewService) CreateReview(
	_ context.Context,
	input review.CreateReview,
) (sqlc.Review, error) {
	f.createCalled = true
	f.createInput = input
	return f.createResult, f.createErr
}

func (f *fakeReviewService) GetReviewByID(
	_ context.Context,
	reviewID int64,
) (sqlc.GetReviewByIDRow, error) {
	f.getCalled = true
	f.getReviewID = reviewID
	return f.getResult, f.getErr
}

func (f *fakeReviewService) ListReviewByHotel(
	_ context.Context,
	hotelID int64,
) ([]sqlc.ListReviewsByHotelRow, error) {
	f.listCalled = true
	f.listHotelID = hotelID
	return f.listResult, f.listErr
}

func TestReviewHandlerCreateReview(t *testing.T) {
	service := &fakeReviewService{
		createResult: sqlc.Review{
			ID:        91,
			BookingID: 77,
			Rating:    5,
			Comment:   pgtype.Text{String: "Great stay", Valid: true},
		},
	}
	mux, token := newReviewTestMux(t, service, 42)
	body := `{"rating":5,"comment":"Great stay","booking_id":999,"user_id":999}`

	recorder := performReviewRequest(mux, http.MethodPost, token, "/me/bookings/77/review", body)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	if !service.createCalled {
		t.Fatal("review service was not called")
	}
	if service.createInput.UserID != 42 {
		t.Errorf("UserID = %d, want authenticated user ID 42", service.createInput.UserID)
	}
	if service.createInput.BookingID != 77 {
		t.Errorf("BookingID = %d, want path booking ID 77", service.createInput.BookingID)
	}
	if service.createInput.Rating != 5 {
		t.Errorf("Rating = %d, want 5", service.createInput.Rating)
	}
	if service.createInput.Comment == nil || *service.createInput.Comment != "Great stay" {
		t.Errorf("Comment = %v, want Great stay", service.createInput.Comment)
	}

	var response sqlc.Review
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 91 {
		t.Errorf("response review ID = %d, want 91", response.ID)
	}
}

func TestReviewHandlerCreateReviewRejectsInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "invalid booking ID", path: "/me/bookings/nope/review", body: `{"rating":5}`},
		{name: "non-positive booking ID", path: "/me/bookings/0/review", body: `{"rating":5}`},
		{name: "malformed JSON", path: "/me/bookings/77/review", body: `{"rating":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeReviewService{}
			mux, token := newReviewTestMux(t, service, 42)

			recorder := performReviewRequest(mux, http.MethodPost, token, tt.path, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.createCalled {
				t.Fatal("review service was called for an invalid request")
			}
		})
	}
}

func TestReviewHandlerCreateReviewMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid rating", err: review.ErrInvalidRating, wantStatus: http.StatusBadRequest},
		{name: "blank comment", err: review.ErrBlankComment, wantStatus: http.StatusBadRequest},
		{name: "booking not reviewable", err: review.ErrBookingNotReviewable, wantStatus: http.StatusConflict},
		{name: "review already exists", err: review.ErrReviewAlreadyExists, wantStatus: http.StatusConflict},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeReviewService{createErr: tt.err}
			mux, token := newReviewTestMux(t, service, 42)

			recorder := performReviewRequest(
				mux,
				http.MethodPost,
				token,
				"/me/bookings/77/review",
				`{"rating":5}`,
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.createCalled {
				t.Fatal("review service was not called")
			}
		})
	}
}

func TestReviewHandlerCreateReviewRejectsUnauthenticatedRequest(t *testing.T) {
	service := &fakeReviewService{}
	mux, _ := newReviewTestMux(t, service, 42)

	recorder := performReviewRequest(
		mux,
		http.MethodPost,
		"",
		"/me/bookings/77/review",
		`{"rating":5}`,
	)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if recorder.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("WWW-Authenticate = %q, want Bearer", recorder.Header().Get("WWW-Authenticate"))
	}
	if service.createCalled {
		t.Fatal("review service was called for an unauthenticated request")
	}
}

func TestReviewHandlerGetReviewByID(t *testing.T) {
	service := &fakeReviewService{
		getResult: sqlc.GetReviewByIDRow{
			ID:           91,
			Rating:       5,
			ReviewerName: "Khang Tran",
			RoomTypeName: "Deluxe",
			HotelID:      12,
		},
	}
	mux, _ := newReviewTestMux(t, service, 42)

	recorder := performReviewRequest(mux, http.MethodGet, "", "/reviews/91", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.getCalled || service.getReviewID != 91 {
		t.Errorf("get call = {called:%v reviewID:%d}, want {true 91}", service.getCalled, service.getReviewID)
	}
	var response sqlc.GetReviewByIDRow
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 91 || response.ReviewerName != "Khang Tran" {
		t.Errorf("unexpected response: %+v", response)
	}
}

func TestReviewHandlerGetReviewByIDHandlesInvalidIDAndErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid ID", path: "/reviews/nope", wantStatus: http.StatusBadRequest},
		{name: "not found", path: "/reviews/91", err: review.ErrReviewNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "unexpected error", path: "/reviews/91", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeReviewService{getErr: tt.err}
			mux, _ := newReviewTestMux(t, service, 42)

			recorder := performReviewRequest(mux, http.MethodGet, "", tt.path, "")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.getCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.getCalled, tt.wantCalled)
			}
		})
	}
}

func TestReviewHandlerListReviewByHotel(t *testing.T) {
	service := &fakeReviewService{
		listResult: []sqlc.ListReviewsByHotelRow{
			{ID: 92, Rating: 4, ReviewerName: "Guest Two", RoomTypeName: "Suite"},
			{ID: 91, Rating: 5, ReviewerName: "Guest One", RoomTypeName: "Deluxe"},
		},
	}
	mux, _ := newReviewTestMux(t, service, 42)

	recorder := performReviewRequest(mux, http.MethodGet, "", "/hotels/12/reviews", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.listCalled || service.listHotelID != 12 {
		t.Errorf("list call = {called:%v hotelID:%d}, want {true 12}", service.listCalled, service.listHotelID)
	}
	var response []sqlc.ListReviewsByHotelRow
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 2 || response[0].ID != 92 || response[1].ID != 91 {
		t.Errorf("unexpected response: %+v", response)
	}
}

func TestReviewHandlerListReviewByHotelReturnsEmptyArray(t *testing.T) {
	service := &fakeReviewService{listResult: []sqlc.ListReviewsByHotelRow{}}
	mux, _ := newReviewTestMux(t, service, 42)

	recorder := performReviewRequest(mux, http.MethodGet, "", "/hotels/12/reviews", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestReviewHandlerListReviewByHotelHandlesInvalidIDAndErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid ID", path: "/hotels/nope/reviews", wantStatus: http.StatusBadRequest},
		{name: "not found", path: "/hotels/12/reviews", err: review.ErrReviewNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "unexpected error", path: "/hotels/12/reviews", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeReviewService{listErr: tt.err}
			mux, _ := newReviewTestMux(t, service, 42)

			recorder := performReviewRequest(mux, http.MethodGet, "", tt.path, "")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.listCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.listCalled, tt.wantCalled)
			}
		})
	}
}

func TestReviewHandlerUpdateReview(t *testing.T) {
	service := &fakeReviewService{updateResult: sqlc.Review{
		ID:        91,
		BookingID: 77,
		Rating:    4,
		Comment:   pgtype.Text{String: "Updated review", Valid: true},
	}}
	mux, token := newReviewTestMux(t, service, 42)

	recorder := performReviewRequest(
		mux,
		http.MethodPut,
		token,
		"/me/reviews/91",
		`{"rating":4,"comment":"Updated review"}`,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.updateCalled {
		t.Fatal("review service was not called")
	}
	if service.updateInput.ReviewID != 91 || service.updateInput.UserID != 42 {
		t.Errorf("identity input = {review:%d user:%d}, want {91 42}", service.updateInput.ReviewID, service.updateInput.UserID)
	}
	if service.updateInput.Rating != 4 || service.updateInput.Comment == nil || *service.updateInput.Comment != "Updated review" {
		t.Errorf("update input = %+v", service.updateInput)
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
}

func TestReviewHandlerUpdateReviewRejectsInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "invalid review ID", path: "/me/reviews/nope", body: `{"rating":4}`},
		{name: "non-positive review ID", path: "/me/reviews/0", body: `{"rating":4}`},
		{name: "malformed JSON", path: "/me/reviews/91", body: `{"rating":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeReviewService{}
			mux, token := newReviewTestMux(t, service, 42)
			recorder := performReviewRequest(mux, http.MethodPut, token, tt.path, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.updateCalled {
				t.Fatal("review service was called for an invalid request")
			}
		})
	}
}

func TestReviewHandlerUpdateReviewMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid rating", err: review.ErrInvalidRating, wantStatus: http.StatusBadRequest},
		{name: "blank comment", err: review.ErrBlankComment, wantStatus: http.StatusBadRequest},
		{name: "not editable", err: review.ErrReviewNotEditable, wantStatus: http.StatusConflict},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeReviewService{updateErr: tt.err}
			mux, token := newReviewTestMux(t, service, 42)
			recorder := performReviewRequest(mux, http.MethodPut, token, "/me/reviews/91", `{"rating":4}`)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestReviewHandlerDeleteReview(t *testing.T) {
	service := &fakeReviewService{deleteResult: 91}
	mux, token := newReviewTestMux(t, service, 42)

	recorder := performReviewRequest(mux, http.MethodDelete, token, "/me/reviews/91", "")

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", recorder.Body.String())
	}
	if !service.deleteCalled || service.deleteID != 91 || service.deleteUserID != 42 {
		t.Errorf("delete call = {called:%v review:%d user:%d}, want {true 91 42}", service.deleteCalled, service.deleteID, service.deleteUserID)
	}
}

func TestReviewHandlerDeleteReviewHandlesInvalidIDAndErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantBody   string
		wantCalled bool
	}{
		{name: "invalid ID", path: "/me/reviews/nope", wantStatus: http.StatusBadRequest},
		{name: "non-positive ID", path: "/me/reviews/0", wantStatus: http.StatusBadRequest},
		{name: "not found", path: "/me/reviews/91", err: review.ErrReviewNotFound, wantStatus: http.StatusNotFound, wantBody: "review not found\n", wantCalled: true},
		{name: "unexpected", path: "/me/reviews/91", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeReviewService{deleteErr: tt.err}
			mux, token := newReviewTestMux(t, service, 42)
			recorder := performReviewRequest(mux, http.MethodDelete, token, tt.path, "")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.wantBody != "" && recorder.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", recorder.Body.String(), tt.wantBody)
			}
			if service.deleteCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.deleteCalled, tt.wantCalled)
			}
		})
	}
}

func TestReviewHandlerMutationsRejectUnauthenticatedRequests(t *testing.T) {
	service := &fakeReviewService{}
	mux, _ := newReviewTestMux(t, service, 42)

	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		recorder := performReviewRequest(mux, method, "", "/me/reviews/91", `{"rating":4}`)
		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d, want %d", method, recorder.Code, http.StatusUnauthorized)
		}
	}
	if service.updateCalled || service.deleteCalled {
		t.Fatal("review service was called for an unauthenticated request")
	}
}

func newReviewTestMux(t *testing.T, service reviewService, userID int64) (*http.ServeMux, string) {
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

	handler := NewReviewHandler(service)
	authMiddleware := middleware.NewAuthMiddleware(tokenManager)
	mux := http.NewServeMux()
	mux.Handle(
		"POST /me/bookings/{bookingID}/review",
		authMiddleware.Authenticate(http.HandlerFunc(handler.CreateReview)),
	)
	mux.HandleFunc("GET /reviews/{reviewID}", handler.GetReviewByID)
	mux.HandleFunc("GET /hotels/{hotelID}/reviews", handler.ListReviewByHotel)
	mux.Handle(
		"PUT /me/reviews/{reviewID}",
		authMiddleware.Authenticate(http.HandlerFunc(handler.UpdateReviewByUser)),
	)
	mux.Handle(
		"DELETE /me/reviews/{reviewID}",
		authMiddleware.Authenticate(http.HandlerFunc(handler.DeleteReview)),
	)

	return mux, token
}

func performReviewRequest(
	handler http.Handler,
	method string,
	token string,
	path string,
	body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

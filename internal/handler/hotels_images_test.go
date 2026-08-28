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
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	hotelimages "github.com/khangtran2403/ryoko/internal/hotel_images"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type fakeHotelImageService struct {
	createCalled bool
	createHotel  int64
	createURL    string
	createResult sqlc.HotelImage
	createErr    error
	listCalled   bool
	listHotel    int64
	listResult   []sqlc.HotelImage
	listErr      error
	setCalled    bool
	setHotel     int64
	setImage     int64
	setResult    sqlc.HotelImage
	setErr       error
	deleteCalled bool
	deleteHotel  int64
	deleteImage  int64
	deleteErr    error
}

func (f *fakeHotelImageService) CreateHotelImage(
	_ context.Context,
	hotelID int64,
	imageURL string,
) (sqlc.HotelImage, error) {
	f.createCalled = true
	f.createHotel = hotelID
	f.createURL = imageURL
	return f.createResult, f.createErr
}

func (f *fakeHotelImageService) ListHotelImages(
	_ context.Context,
	hotelID int64,
) ([]sqlc.HotelImage, error) {
	f.listCalled = true
	f.listHotel = hotelID
	return f.listResult, f.listErr
}

func (f *fakeHotelImageService) SetPrimaryHotelImage(
	_ context.Context,
	hotelID int64,
	imageID int64,
) (sqlc.HotelImage, error) {
	f.setCalled = true
	f.setHotel = hotelID
	f.setImage = imageID
	return f.setResult, f.setErr
}

func (f *fakeHotelImageService) DeleteHotelImage(
	_ context.Context,
	hotelID int64,
	imageID int64,
) error {
	f.deleteCalled = true
	f.deleteHotel = hotelID
	f.deleteImage = imageID
	return f.deleteErr
}

func TestHotelImageHandlerCreate(t *testing.T) {
	service := &fakeHotelImageService{createResult: sqlc.HotelImage{
		ID:       91,
		HotelID:  12,
		ImageUrl: "https://example.com/hotel.jpg",
	}}
	mux, tokens := newHotelImageTestMux(t, service)

	recorder := performHotelImageRequest(
		mux,
		http.MethodPost,
		tokens.admin,
		"/hotels/12/images",
		`{"image_url":"https://example.com/hotel.jpg"}`,
	)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if !service.createCalled || service.createHotel != 12 {
		t.Errorf("create call = {called:%v hotel:%d}, want {true 12}", service.createCalled, service.createHotel)
	}
	if service.createURL != "https://example.com/hotel.jpg" {
		t.Errorf("image URL = %q, want JSON body URL", service.createURL)
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
	var response sqlc.HotelImage
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 91 || response.HotelID != 12 {
		t.Errorf("response = %+v", response)
	}
}

func TestHotelImageHandlerCreateRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "invalid hotel ID", path: "/hotels/nope/images", body: `{"image_url":"https://example.com/a.jpg"}`},
		{name: "non-positive hotel ID", path: "/hotels/0/images", body: `{"image_url":"https://example.com/a.jpg"}`},
		{name: "malformed JSON", path: "/hotels/12/images", body: `{"image_url":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeHotelImageService{}
			mux, tokens := newHotelImageTestMux(t, service)
			recorder := performHotelImageRequest(mux, http.MethodPost, tokens.admin, tt.path, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.createCalled {
				t.Fatal("service called for invalid request")
			}
		})
	}
}

func TestHotelImageHandlerCreateMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "blank URL", err: hotelimages.ErrInvalidImageURL, wantStatus: http.StatusBadRequest},
		{name: "hotel missing", err: hotelimages.ErrHotelNotFound, wantStatus: http.StatusNotFound},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeHotelImageService{createErr: tt.err}
			mux, tokens := newHotelImageTestMux(t, service)
			recorder := performHotelImageRequest(
				mux,
				http.MethodPost,
				tokens.admin,
				"/hotels/12/images",
				`{"image_url":"https://example.com/a.jpg"}`,
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestHotelImageHandlerList(t *testing.T) {
	service := &fakeHotelImageService{listResult: []sqlc.HotelImage{
		{ID: 91, HotelID: 12, ImageUrl: "https://example.com/primary.jpg", IsPrimary: true},
		{ID: 92, HotelID: 12, ImageUrl: "https://example.com/other.jpg"},
	}}
	mux, _ := newHotelImageTestMux(t, service)

	recorder := performHotelImageRequest(mux, http.MethodGet, "", "/hotels/12/images", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.listCalled || service.listHotel != 12 {
		t.Errorf("list call = {called:%v hotel:%d}, want {true 12}", service.listCalled, service.listHotel)
	}
	var response []sqlc.HotelImage
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 2 || response[0].ID != 91 || response[1].ID != 92 {
		t.Errorf("response = %+v", response)
	}
}

func TestHotelImageHandlerListReturnsEmptyArray(t *testing.T) {
	service := &fakeHotelImageService{listResult: []sqlc.HotelImage{}}
	mux, _ := newHotelImageTestMux(t, service)
	recorder := performHotelImageRequest(mux, http.MethodGet, "", "/hotels/12/images", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestHotelImageHandlerListHandlesInvalidIDAndError(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid ID", path: "/hotels/nope/images", wantStatus: http.StatusBadRequest},
		{name: "non-positive ID", path: "/hotels/0/images", wantStatus: http.StatusBadRequest},
		{name: "unexpected", path: "/hotels/12/images", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeHotelImageService{listErr: tt.err}
			mux, _ := newHotelImageTestMux(t, service)
			recorder := performHotelImageRequest(mux, http.MethodGet, "", tt.path, "")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.listCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.listCalled, tt.wantCalled)
			}
		})
	}
}

func TestHotelImageHandlerSetPrimary(t *testing.T) {
	service := &fakeHotelImageService{setResult: sqlc.HotelImage{
		ID: 91, HotelID: 12, ImageUrl: "https://example.com/primary.jpg", IsPrimary: true,
	}}
	mux, tokens := newHotelImageTestMux(t, service)

	recorder := performHotelImageRequest(
		mux,
		http.MethodPut,
		tokens.admin,
		"/hotels/12/images/91/primary",
		"",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.setCalled || service.setHotel != 12 || service.setImage != 91 {
		t.Errorf("set call = {called:%v hotel:%d image:%d}, want {true 12 91}", service.setCalled, service.setHotel, service.setImage)
	}
	var response sqlc.HotelImage
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.IsPrimary || response.ID != 91 {
		t.Errorf("response = %+v", response)
	}
}

func TestHotelImageHandlerSetPrimaryHandlesInvalidIDsAndErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid hotel ID", path: "/hotels/nope/images/91/primary", wantStatus: http.StatusBadRequest},
		{name: "non-positive hotel ID", path: "/hotels/0/images/91/primary", wantStatus: http.StatusBadRequest},
		{name: "invalid image ID", path: "/hotels/12/images/nope/primary", wantStatus: http.StatusBadRequest},
		{name: "non-positive image ID", path: "/hotels/12/images/0/primary", wantStatus: http.StatusBadRequest},
		{name: "hotel missing", path: "/hotels/12/images/91/primary", err: hotelimages.ErrHotelNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "image missing", path: "/hotels/12/images/91/primary", err: hotelimages.ErrImageNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "unexpected", path: "/hotels/12/images/91/primary", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeHotelImageService{setErr: tt.err}
			mux, tokens := newHotelImageTestMux(t, service)
			recorder := performHotelImageRequest(mux, http.MethodPut, tokens.admin, tt.path, "")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.setCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.setCalled, tt.wantCalled)
			}
		})
	}
}

func TestHotelImageHandlerDelete(t *testing.T) {
	service := &fakeHotelImageService{}
	mux, tokens := newHotelImageTestMux(t, service)
	recorder := performHotelImageRequest(
		mux,
		http.MethodDelete,
		tokens.admin,
		"/hotels/12/images/91",
		"",
	)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", recorder.Body.String())
	}
	if !service.deleteCalled || service.deleteHotel != 12 || service.deleteImage != 91 {
		t.Errorf("delete call = {called:%v hotel:%d image:%d}, want {true 12 91}", service.deleteCalled, service.deleteHotel, service.deleteImage)
	}
}

func TestHotelImageHandlerDeleteHandlesInvalidIDsAndErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid hotel ID", path: "/hotels/nope/images/91", wantStatus: http.StatusBadRequest},
		{name: "non-positive hotel ID", path: "/hotels/0/images/91", wantStatus: http.StatusBadRequest},
		{name: "invalid image ID", path: "/hotels/12/images/nope", wantStatus: http.StatusBadRequest},
		{name: "non-positive image ID", path: "/hotels/12/images/0", wantStatus: http.StatusBadRequest},
		{name: "image missing", path: "/hotels/12/images/91", err: hotelimages.ErrImageNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "unexpected", path: "/hotels/12/images/91", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeHotelImageService{deleteErr: tt.err}
			mux, tokens := newHotelImageTestMux(t, service)
			recorder := performHotelImageRequest(mux, http.MethodDelete, tokens.admin, tt.path, "")

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.deleteCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.deleteCalled, tt.wantCalled)
			}
		})
	}
}

func TestHotelImageMutationRoutesRequireAdmin(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "create", method: http.MethodPost, path: "/hotels/12/images", body: `{"image_url":"https://example.com/a.jpg"}`},
		{name: "set primary", method: http.MethodPut, path: "/hotels/12/images/91/primary"},
		{name: "delete", method: http.MethodDelete, path: "/hotels/12/images/91"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeHotelImageService{}
			mux, tokens := newHotelImageTestMux(t, service)

			unauthenticated := performHotelImageRequest(mux, tt.method, "", tt.path, tt.body)
			if unauthenticated.Code != http.StatusUnauthorized {
				t.Errorf("unauthenticated status = %d, want %d", unauthenticated.Code, http.StatusUnauthorized)
			}
			customer := performHotelImageRequest(mux, tt.method, tokens.customer, tt.path, tt.body)
			if customer.Code != http.StatusForbidden {
				t.Errorf("customer status = %d, want %d", customer.Code, http.StatusForbidden)
			}
			if service.createCalled || service.setCalled || service.deleteCalled {
				t.Fatal("service called without admin role")
			}
		})
	}
}

type hotelImageTestTokens struct {
	admin    string
	customer string
}

func newHotelImageTestMux(
	t *testing.T,
	service hotelImageService,
) (*http.ServeMux, hotelImageTestTokens) {
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

	handler := NewHotelImageHandler(service)
	authMiddleware := middleware.NewAuthMiddleware(tokenManager)
	adminOnly := func(next http.HandlerFunc) http.Handler {
		return authMiddleware.Authenticate(
			middleware.RequireRole(auth.RoleAdmin, next),
		)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /hotels/{hotelID}/images", adminOnly(handler.CreateHotelImage))
	mux.HandleFunc("GET /hotels/{hotelID}/images", handler.ListHotelImages)
	mux.Handle("PUT /hotels/{hotelID}/images/{imageID}/primary", adminOnly(handler.SetPrimaryHotelImage))
	mux.Handle("DELETE /hotels/{hotelID}/images/{imageID}", adminOnly(handler.DeleteHotelImage))

	return mux, hotelImageTestTokens{admin: adminToken, customer: customerToken}
}

func performHotelImageRequest(
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

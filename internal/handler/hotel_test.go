package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/hotel"
)

type fakeHotelDetailsService struct {
	called  bool
	hotelID int64
	result  hotel.HotelResponse
	err     error
}

func (f *fakeHotelDetailsService) GetHotelDetailsByID(
	_ context.Context,
	hotelID int64,
) (hotel.HotelResponse, error) {
	f.called = true
	f.hotelID = hotelID
	return f.result, f.err
}

func TestHotelHandlerGetByID(t *testing.T) {
	service := &fakeHotelDetailsService{
		result: hotel.HotelResponse{
			ID:        12,
			Name:      "Riverside Hotel",
			Address:   "1 River Street",
			City:      "Da Nang",
			Images:    []sqlc.HotelImage{},
			Amenities: []sqlc.Amenity{},
			RoomTypes: []sqlc.RoomType{},
			ReviewSummary: sqlc.GetHotelReviewSummaryRow{
				ReviewCount: 3,
			},
		},
	}
	handler := NewHotelHandler(nil, service)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hotels/{id}", handler.GetByID)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/hotels/12", nil)

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.called {
		t.Fatal("hotel service was not called")
	}
	if service.hotelID != 12 {
		t.Errorf("hotel ID = %d, want 12", service.hotelID)
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}

	var response hotel.HotelResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 12 || response.Name != "Riverside Hotel" {
		t.Errorf("response hotel = %+v", response)
	}
	if response.ReviewSummary.ReviewCount != 3 {
		t.Errorf("review count = %d, want 3", response.ReviewSummary.ReviewCount)
	}
}

func TestHotelHandlerGetByIDRejectsInvalidPathID(t *testing.T) {
	service := &fakeHotelDetailsService{}
	handler := NewHotelHandler(nil, service)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hotels/{id}", handler.GetByID)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/hotels/not-a-number", nil)

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if service.called {
		t.Fatal("hotel service was called for an invalid path ID")
	}
}

func TestHotelHandlerGetByIDRejectsMissingPathID(t *testing.T) {
	service := &fakeHotelDetailsService{}
	handler := NewHotelHandler(nil, service)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/hotels/", nil)

	handler.GetByID(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if service.called {
		t.Fatal("hotel service was called without a path ID")
	}
}

func TestHotelHandlerGetByIDMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid hotel ID", err: hotel.ErrInvalidHotelID, wantStatus: http.StatusBadRequest},
		{name: "hotel not found", err: hotel.ErrHotelNotFound, wantStatus: http.StatusNotFound},
		{name: "wrapped hotel not found", err: errors.Join(errors.New("lookup failed"), hotel.ErrHotelNotFound), wantStatus: http.StatusNotFound},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeHotelDetailsService{err: tt.err}
			handler := NewHotelHandler(nil, service)
			mux := http.NewServeMux()
			mux.HandleFunc("GET /hotels/{id}", handler.GetByID)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/hotels/12", nil)

			mux.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.called {
				t.Fatal("hotel service was not called")
			}
		})
	}
}

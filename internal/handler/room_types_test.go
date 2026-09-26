package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/roomtype"
)

type fakeRoomTypeService struct {
	called bool
	input  roomtype.UpdateRoomTypeRequest
	result sqlc.RoomType
	err    error
}

func (f *fakeRoomTypeService) UpdateRoomType(
	_ context.Context,
	input roomtype.UpdateRoomTypeRequest,
) (sqlc.RoomType, error) {
	f.called = true
	f.input = input
	return f.result, f.err
}

func TestRoomTypeHandlerUpdatesThroughService(t *testing.T) {
	service := &fakeRoomTypeService{
		result: sqlc.RoomType{
			ID:         7,
			Name:       "Deluxe King",
			Capacity:   3,
			TotalRooms: 8,
		},
	}
	handler := NewRoomTypeHandler(nil, service)
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /room-types/{id}", handler.UpdateRoomType)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/room-types/7",
		strings.NewReader(`{"name":"Deluxe King","description":"River view","price_per_night":"1750000.00","capacity":3,"total_rooms":8}`),
	)

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.called {
		t.Fatal("UpdateRoomType service was not called")
	}
	if service.input.ID != 7 || service.input.Name != "Deluxe King" || service.input.Description.String != "River view" || service.input.Capacity != 3 || service.input.TotalRooms != 8 {
		t.Errorf("service input = %+v", service.input)
	}
	price, err := service.input.PricePerNight.Value()
	if err != nil {
		t.Fatalf("read input price: %v", err)
	}
	if fmt.Sprint(price) != "1750000.00" {
		t.Errorf("price = %v, want 1750000.00", price)
	}
	if !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q, want application/json", recorder.Header().Get("Content-Type"))
	}
}

func TestRoomTypeHandlerUpdateRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "invalid path ID", path: "/room-types/not-a-number", body: validRoomTypeUpdateBody()},
		{name: "malformed JSON", path: "/room-types/7", body: `{"name":`},
		{name: "blank name", path: "/room-types/7", body: `{"name":" ","price_per_night":"100.00","capacity":2,"total_rooms":5}`},
		{name: "invalid price", path: "/room-types/7", body: `{"name":"Standard","price_per_night":"free","capacity":2,"total_rooms":5}`},
		{name: "non-positive capacity", path: "/room-types/7", body: `{"name":"Standard","price_per_night":"100.00","capacity":0,"total_rooms":5}`},
		{name: "non-positive total rooms", path: "/room-types/7", body: `{"name":"Standard","price_per_night":"100.00","capacity":2,"total_rooms":0}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeRoomTypeService{}
			handler := NewRoomTypeHandler(nil, service)
			mux := http.NewServeMux()
			mux.HandleFunc("PUT /room-types/{id}", handler.UpdateRoomType)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, tt.path, strings.NewReader(tt.body))

			mux.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.called {
				t.Fatal("service was called for invalid request")
			}
		})
	}
}

func TestRoomTypeHandlerUpdateMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid ID", err: roomtype.ErrInvalidID, wantStatus: http.StatusBadRequest},
		{name: "invalid total rooms", err: roomtype.ErrInvalidTotalRooms, wantStatus: http.StatusBadRequest},
		{name: "not found", err: roomtype.ErrRoomTypeNotFound, wantStatus: http.StatusNotFound},
		{name: "inventory conflict", err: roomtype.ErrInventoryConflict, wantStatus: http.StatusConflict},
		{name: "wrapped inventory conflict", err: fmt.Errorf("update: %w", roomtype.ErrInventoryConflict), wantStatus: http.StatusConflict},
		{name: "unexpected error", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeRoomTypeService{err: tt.err}
			handler := NewRoomTypeHandler(nil, service)
			mux := http.NewServeMux()
			mux.HandleFunc("PUT /room-types/{id}", handler.UpdateRoomType)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/room-types/7", strings.NewReader(validRoomTypeUpdateBody()))

			mux.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.called {
				t.Fatal("service was not called")
			}
		})
	}
}

func validRoomTypeUpdateBody() string {
	return `{"name":"Standard Updated","description":"Updated","price_per_night":"1200000.00","capacity":2,"total_rooms":5}`
}

func TestRoomTypeUpdateDescriptionCanBeNullEquivalent(t *testing.T) {
	service := &fakeRoomTypeService{}
	handler := NewRoomTypeHandler(nil, service)
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /room-types/{id}", handler.UpdateRoomType)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/room-types/7",
		strings.NewReader(`{"name":"Standard","description":"","price_per_night":"100.00","capacity":2,"total_rooms":5}`),
	)

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if service.input.Description != (pgtype.Text{}) {
		t.Errorf("description = %+v, want invalid pgtype.Text for SQL NULL", service.input.Description)
	}
}

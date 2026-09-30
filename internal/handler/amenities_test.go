package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/khangtran2403/ryoko/internal/amenities"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type fakeAmenityService struct {
	createCalled bool
	createName   string
	createResult sqlc.Amenity
	createErr    error
	updateCalled bool
	updateID     int64
	updateName   string
	updateResult sqlc.Amenity
	updateErr    error
	deleteCalled bool
	deleteID     int64
	deleteErr    error

	listCalled bool
	listResult []sqlc.Amenity
	listErr    error

	addCalled  bool
	addHotelID int64
	addID      int64
	addResult  sqlc.HotelAmenity
	addErr     error

	removeCalled  bool
	removeHotelID int64
	removeID      int64
	removeErr     error

	listByHotelCalled bool
	listByHotelID     int64
	listByHotelResult []sqlc.Amenity
	listByHotelErr    error
}

func (f *fakeAmenityService) CreateAmenity(_ context.Context, name string) (sqlc.Amenity, error) {
	f.createCalled = true
	f.createName = name
	return f.createResult, f.createErr
}

func (f *fakeAmenityService) UpdateAmenity(
	_ context.Context,
	amenityID int64,
	name string,
) (sqlc.Amenity, error) {
	f.updateCalled = true
	f.updateID = amenityID
	f.updateName = name
	return f.updateResult, f.updateErr
}

func (f *fakeAmenityService) DeleteAmenity(_ context.Context, amenityID int64) error {
	f.deleteCalled = true
	f.deleteID = amenityID
	return f.deleteErr
}

func (f *fakeAmenityService) ListAmenities(context.Context) ([]sqlc.Amenity, error) {
	f.listCalled = true
	return f.listResult, f.listErr
}

func (f *fakeAmenityService) AddAmenityToHotel(
	_ context.Context,
	hotelID int64,
	amenityID int64,
) (sqlc.HotelAmenity, error) {
	f.addCalled = true
	f.addHotelID = hotelID
	f.addID = amenityID
	return f.addResult, f.addErr
}

func (f *fakeAmenityService) RemoveAmenityFromHotel(
	_ context.Context,
	hotelID int64,
	amenityID int64,
) error {
	f.removeCalled = true
	f.removeHotelID = hotelID
	f.removeID = amenityID
	return f.removeErr
}

func (f *fakeAmenityService) ListAmenitiesByHotel(
	_ context.Context,
	hotelID int64,
) ([]sqlc.Amenity, error) {
	f.listByHotelCalled = true
	f.listByHotelID = hotelID
	return f.listByHotelResult, f.listByHotelErr
}

func TestAmenityHandlerCreate(t *testing.T) {
	service := &fakeAmenityService{createResult: sqlc.Amenity{ID: 7, Name: "Pool"}}
	handler := NewAmenityHandler(service)
	recorder := performAmenityRequest(handler.CreateAmenity, http.MethodPost, "/amenities", `{"name":"  Pool  "}`, nil)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if !service.createCalled || service.createName != "Pool" {
		t.Errorf("create call = {called:%v name:%q}, want {true Pool}", service.createCalled, service.createName)
	}
	var response sqlc.Amenity
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 7 || response.Name != "Pool" {
		t.Errorf("response = %+v", response)
	}
}

func TestAmenityHandlerCreateRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"name":`},
		{name: "blank name", body: `{"name":"  "}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAmenityService{}
			handler := NewAmenityHandler(service)
			recorder := performAmenityRequest(handler.CreateAmenity, http.MethodPost, "/amenities", tt.body, nil)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if service.createCalled {
				t.Fatal("service called for invalid request")
			}
		})
	}
}

func TestAmenityHandlerCreateMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid name", err: amenities.ErrInvalidAmenityName, wantStatus: http.StatusBadRequest},
		{name: "name conflict", err: amenities.ErrAmenityNameConflict, wantStatus: http.StatusConflict},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAmenityService{createErr: tt.err}
			handler := NewAmenityHandler(service)
			recorder := performAmenityRequest(handler.CreateAmenity, http.MethodPost, "/amenities", `{"name":"Pool"}`, nil)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestAmenityHandlerList(t *testing.T) {
	service := &fakeAmenityService{listResult: []sqlc.Amenity{{ID: 1, Name: "Pool"}}}
	handler := NewAmenityHandler(service)
	recorder := performAmenityRequest(handler.ListAmenities, http.MethodGet, "/amenities", "", nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !service.listCalled {
		t.Fatal("ListAmenities service was not called")
	}
	var response []sqlc.Amenity
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response) != 1 || response[0].Name != "Pool" {
		t.Errorf("response = %+v", response)
	}
}

func TestAmenityHandlerListMapsUnexpectedError(t *testing.T) {
	service := &fakeAmenityService{listErr: errors.New("database unavailable")}
	handler := NewAmenityHandler(service)
	recorder := performAmenityRequest(handler.ListAmenities, http.MethodGet, "/amenities", "", nil)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestAmenityHandlerAddToHotel(t *testing.T) {
	service := &fakeAmenityService{addResult: sqlc.HotelAmenity{HotelID: 12, AmenityID: 7}}
	handler := NewAmenityHandler(service)
	recorder := performAmenityRequest(
		handler.AddAmenityToHotel,
		http.MethodPost,
		"/hotels/12/amenities",
		`{"amenity_id":7}`,
		map[string]string{"hotelID": "12"},
	)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	if !service.addCalled || service.addHotelID != 12 || service.addID != 7 {
		t.Errorf("add call = {called:%v hotel:%d amenity:%d}", service.addCalled, service.addHotelID, service.addID)
	}
}

func TestAmenityHandlerAddToHotelErrors(t *testing.T) {
	tests := []struct {
		name       string
		hotelID    string
		body       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid hotel ID", hotelID: "nope", body: `{"amenity_id":7}`, wantStatus: http.StatusBadRequest},
		{name: "non-positive hotel ID", hotelID: "0", body: `{"amenity_id":7}`, wantStatus: http.StatusBadRequest},
		{name: "malformed JSON", hotelID: "12", body: `{"amenity_id":`, wantStatus: http.StatusBadRequest},
		{name: "non-positive amenity ID", hotelID: "12", body: `{"amenity_id":0}`, wantStatus: http.StatusBadRequest},
		{name: "resource missing", hotelID: "12", body: `{"amenity_id":7}`, err: amenities.ErrHotelOrAmenityNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "already added", hotelID: "12", body: `{"amenity_id":7}`, err: amenities.ErrAmenityAlreadyAdded, wantStatus: http.StatusConflict, wantCalled: true},
		{name: "unexpected", hotelID: "12", body: `{"amenity_id":7}`, err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAmenityService{addErr: tt.err}
			handler := NewAmenityHandler(service)
			recorder := performAmenityRequest(
				handler.AddAmenityToHotel,
				http.MethodPost,
				"/hotels/"+tt.hotelID+"/amenities",
				tt.body,
				map[string]string{"hotelID": tt.hotelID},
			)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.addCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.addCalled, tt.wantCalled)
			}
		})
	}
}

func TestAmenityHandlerRemoveFromHotel(t *testing.T) {
	service := &fakeAmenityService{}
	handler := NewAmenityHandler(service)
	recorder := performAmenityRequest(
		handler.RemoveAmenitiesFromHotel,
		http.MethodDelete,
		"/hotels/12/amenities/7",
		"",
		map[string]string{"hotelID": "12", "amenityID": "7"},
	)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if !service.removeCalled || service.removeHotelID != 12 || service.removeID != 7 {
		t.Errorf("remove call = {called:%v hotel:%d amenity:%d}", service.removeCalled, service.removeHotelID, service.removeID)
	}
}

func TestAmenityHandlerRemoveFromHotelErrors(t *testing.T) {
	tests := []struct {
		name       string
		hotelID    string
		amenityID  string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid hotel ID", hotelID: "nope", amenityID: "7", wantStatus: http.StatusBadRequest},
		{name: "non-positive hotel ID", hotelID: "0", amenityID: "7", wantStatus: http.StatusBadRequest},
		{name: "invalid amenity ID", hotelID: "12", amenityID: "nope", wantStatus: http.StatusBadRequest},
		{name: "non-positive amenity ID", hotelID: "12", amenityID: "0", wantStatus: http.StatusBadRequest},
		{name: "resource missing", hotelID: "12", amenityID: "7", err: amenities.ErrHotelOrAmenityNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "unexpected", hotelID: "12", amenityID: "7", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAmenityService{removeErr: tt.err}
			handler := NewAmenityHandler(service)
			recorder := performAmenityRequest(
				handler.RemoveAmenitiesFromHotel,
				http.MethodDelete,
				"/hotels/"+tt.hotelID+"/amenities/"+tt.amenityID,
				"",
				map[string]string{"hotelID": tt.hotelID, "amenityID": tt.amenityID},
			)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.removeCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.removeCalled, tt.wantCalled)
			}
		})
	}
}

func TestAmenityHandlerListByHotel(t *testing.T) {
	service := &fakeAmenityService{listByHotelResult: []sqlc.Amenity{{ID: 7, Name: "Pool"}}}
	handler := NewAmenityHandler(service)
	recorder := performAmenityRequest(
		handler.ListAmenitiesByHotel,
		http.MethodGet,
		"/hotels/12/amenities",
		"",
		map[string]string{"hotelID": "12"},
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !service.listByHotelCalled || service.listByHotelID != 12 {
		t.Errorf("list-by-hotel call = {called:%v hotel:%d}", service.listByHotelCalled, service.listByHotelID)
	}
}

func TestAmenityHandlerListByHotelErrors(t *testing.T) {
	tests := []struct {
		name       string
		hotelID    string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid hotel ID", hotelID: "nope", wantStatus: http.StatusBadRequest},
		{name: "non-positive hotel ID", hotelID: "0", wantStatus: http.StatusBadRequest},
		{name: "hotel missing", hotelID: "12", err: amenities.ErrHotelNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "unexpected", hotelID: "12", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAmenityService{listByHotelErr: tt.err}
			handler := NewAmenityHandler(service)
			recorder := performAmenityRequest(
				handler.ListAmenitiesByHotel,
				http.MethodGet,
				"/hotels/"+tt.hotelID+"/amenities",
				"",
				map[string]string{"hotelID": tt.hotelID},
			)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.listByHotelCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.listByHotelCalled, tt.wantCalled)
			}
		})
	}
}

func TestAmenityHandlerUpdate(t *testing.T) {
	service := &fakeAmenityService{updateResult: sqlc.Amenity{ID: 7, Name: "Rooftop Pool"}}
	handler := NewAmenityHandler(service)
	recorder := performAmenityRequest(
		handler.UpdateAmenity,
		http.MethodPut,
		"/amenities/7",
		`{"name":"  Rooftop Pool  "}`,
		map[string]string{"amenityID": "7"},
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.updateCalled || service.updateID != 7 || service.updateName != "Rooftop Pool" {
		t.Errorf("update call = {called:%v id:%d name:%q}", service.updateCalled, service.updateID, service.updateName)
	}
	var response sqlc.Amenity
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != 7 || response.Name != "Rooftop Pool" {
		t.Errorf("response = %+v", response)
	}
}

func TestAmenityHandlerUpdateErrors(t *testing.T) {
	tests := []struct {
		name       string
		amenityID  string
		body       string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid ID", amenityID: "nope", body: `{"name":"Pool"}`, wantStatus: http.StatusBadRequest},
		{name: "non-positive ID", amenityID: "0", body: `{"name":"Pool"}`, wantStatus: http.StatusBadRequest},
		{name: "malformed JSON", amenityID: "7", body: `{"name":`, wantStatus: http.StatusBadRequest},
		{name: "blank name", amenityID: "7", body: `{"name":"  "}`, wantStatus: http.StatusBadRequest},
		{name: "amenity missing", amenityID: "7", body: `{"name":"Pool"}`, err: amenities.ErrAmenityNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "name conflict", amenityID: "7", body: `{"name":"Pool"}`, err: amenities.ErrAmenityNameConflict, wantStatus: http.StatusConflict, wantCalled: true},
		{name: "unexpected", amenityID: "7", body: `{"name":"Pool"}`, err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAmenityService{updateErr: tt.err}
			handler := NewAmenityHandler(service)
			recorder := performAmenityRequest(
				handler.UpdateAmenity,
				http.MethodPut,
				"/amenities/"+tt.amenityID,
				tt.body,
				map[string]string{"amenityID": tt.amenityID},
			)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.updateCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.updateCalled, tt.wantCalled)
			}
		})
	}
}

func TestAmenityHandlerDelete(t *testing.T) {
	service := &fakeAmenityService{}
	handler := NewAmenityHandler(service)
	recorder := performAmenityRequest(
		handler.DeleteAmenity,
		http.MethodDelete,
		"/amenities/7",
		"",
		map[string]string{"amenityID": "7"},
	)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if !service.deleteCalled || service.deleteID != 7 {
		t.Errorf("delete call = {called:%v id:%d}", service.deleteCalled, service.deleteID)
	}
}

func TestAmenityHandlerDeleteErrors(t *testing.T) {
	tests := []struct {
		name       string
		amenityID  string
		err        error
		wantStatus int
		wantCalled bool
	}{
		{name: "invalid ID", amenityID: "nope", wantStatus: http.StatusBadRequest},
		{name: "non-positive ID", amenityID: "0", wantStatus: http.StatusBadRequest},
		{name: "amenity missing", amenityID: "7", err: amenities.ErrAmenityNotFound, wantStatus: http.StatusNotFound, wantCalled: true},
		{name: "unexpected", amenityID: "7", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeAmenityService{deleteErr: tt.err}
			handler := NewAmenityHandler(service)
			recorder := performAmenityRequest(
				handler.DeleteAmenity,
				http.MethodDelete,
				"/amenities/"+tt.amenityID,
				"",
				map[string]string{"amenityID": tt.amenityID},
			)
			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if service.deleteCalled != tt.wantCalled {
				t.Errorf("service called = %v, want %v", service.deleteCalled, tt.wantCalled)
			}
		})
	}
}

func performAmenityRequest(
	handle http.HandlerFunc,
	method string,
	path string,
	body string,
	pathValues map[string]string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	for key, value := range pathValues {
		request.SetPathValue(key, value)
	}
	recorder := httptest.NewRecorder()
	handle(recorder, request)
	return recorder
}

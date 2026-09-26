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
	"github.com/khangtran2403/ryoko/internal/inventory"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type fakeInventoryService struct {
	setCalled bool
	setInput  inventory.SetBlockedInventoryInput
	setResult []sqlc.ListRoomTypeInventoryRow
	setErr    error

	listCalled bool
	listInput  inventory.ListInventoryInput
	listResult []sqlc.ListRoomTypeInventoryRow
	listErr    error
}

func (f *fakeInventoryService) SetBlockedInventory(
	_ context.Context,
	input inventory.SetBlockedInventoryInput,
) ([]sqlc.ListRoomTypeInventoryRow, error) {
	f.setCalled = true
	f.setInput = input
	return f.setResult, f.setErr
}

func (f *fakeInventoryService) ListRoomTypeInventory(
	_ context.Context,
	input inventory.ListInventoryInput,
) ([]sqlc.ListRoomTypeInventoryRow, error) {
	f.listCalled = true
	f.listInput = input
	return f.listResult, f.listErr
}

func TestInventoryHandlerSetsBlockedInventory(t *testing.T) {
	service := &fakeInventoryService{
		setResult: []sqlc.ListRoomTypeInventoryRow{
			inventoryTestRow("2030-05-10", 10, 3, 2, "Bathroom renovation"),
			inventoryTestRow("2030-05-11", 10, 1, 2, "Bathroom renovation"),
		},
	}
	mux, adminToken, _ := newInventoryMux(t, service)

	recorder := performInventoryRequest(
		mux,
		http.MethodPut,
		adminToken,
		"/admin/room-types/7/blocked-inventory",
		`{"date_from":"2030-05-10","date_to":"2030-05-12","rooms_blocked":2,"block_reason":"Bathroom renovation"}`,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.setCalled {
		t.Fatal("SetBlockedInventory was not called")
	}
	if service.setInput.RoomTypeID != 7 || service.setInput.RoomsBlocked != 2 || service.setInput.BlockReason != "Bathroom renovation" {
		t.Errorf("set input = %+v", service.setInput)
	}
	assertHandlerDate(t, service.setInput.DateFrom, "2030-05-10")
	assertHandlerDate(t, service.setInput.DateTo, "2030-05-12")

	var response RoomTypeInventoryResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Inventory) != 2 {
		t.Fatalf("inventory length = %d, want 2", len(response.Inventory))
	}
	first := response.Inventory[0]
	if first.Date != "2030-05-10" || first.RoomsBooked != 3 || first.RoomsBlocked != 2 || first.RoomsAvailable != 5 {
		t.Errorf("first inventory day = %+v", first)
	}
	if first.BlockReason == nil || *first.BlockReason != "Bathroom renovation" {
		t.Errorf("block reason = %v, want Bathroom renovation", first.BlockReason)
	}
}

func TestInventoryHandlerListsRoomTypeInventory(t *testing.T) {
	service := &fakeInventoryService{
		listResult: []sqlc.ListRoomTypeInventoryRow{
			inventoryTestRow("2030-05-10", 10, 0, 0, ""),
		},
	}
	mux, adminToken, _ := newInventoryMux(t, service)

	recorder := performInventoryRequest(
		mux,
		http.MethodGet,
		adminToken,
		"/admin/room-types/7/inventory?date_from=2030-05-10&date_to=2030-05-11",
		"",
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.listCalled {
		t.Fatal("ListRoomTypeInventory was not called")
	}
	if service.listInput.RoomTypeID != 7 {
		t.Errorf("room type ID = %d, want 7", service.listInput.RoomTypeID)
	}
	assertHandlerDate(t, service.listInput.DateFrom, "2030-05-10")
	assertHandlerDate(t, service.listInput.DateTo, "2030-05-11")

	var response RoomTypeInventoryResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Inventory) != 1 || response.Inventory[0].BlockReason != nil {
		t.Errorf("response = %+v, want one day with null block_reason", response)
	}
}

func TestInventoryHandlerSetRejectsInvalidRequests(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "invalid room type ID", path: "/admin/room-types/not-a-number/blocked-inventory", body: validSetInventoryBody()},
		{name: "non-positive room type ID", path: "/admin/room-types/0/blocked-inventory", body: validSetInventoryBody()},
		{name: "missing body", path: "/admin/room-types/7/blocked-inventory", body: ""},
		{name: "malformed JSON", path: "/admin/room-types/7/blocked-inventory", body: `{"date_from":`},
		{name: "unknown field", path: "/admin/room-types/7/blocked-inventory", body: `{"date_from":"2030-05-10","date_to":"2030-05-11","rooms_blocked":1,"block_reason":"Maintenance","unexpected":true}`},
		{name: "invalid date from", path: "/admin/room-types/7/blocked-inventory", body: `{"date_from":"10-05-2030","date_to":"2030-05-11","rooms_blocked":1,"block_reason":"Maintenance"}`},
		{name: "invalid date to", path: "/admin/room-types/7/blocked-inventory", body: `{"date_from":"2030-05-10","date_to":"11-05-2030","rooms_blocked":1,"block_reason":"Maintenance"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeInventoryService{}
			mux, adminToken, _ := newInventoryMux(t, service)
			recorder := performInventoryRequest(mux, http.MethodPut, adminToken, tt.path, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.setCalled {
				t.Fatal("SetBlockedInventory was called for an invalid request")
			}
		})
	}
}

func TestInventoryHandlerListRejectsInvalidRequests(t *testing.T) {
	paths := []string{
		"/admin/room-types/not-a-number/inventory?date_from=2030-05-10&date_to=2030-05-11",
		"/admin/room-types/0/inventory?date_from=2030-05-10&date_to=2030-05-11",
		"/admin/room-types/7/inventory?date_to=2030-05-11",
		"/admin/room-types/7/inventory?date_from=2030-05-10",
		"/admin/room-types/7/inventory?date_from=10-05-2030&date_to=2030-05-11",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			service := &fakeInventoryService{}
			mux, adminToken, _ := newInventoryMux(t, service)
			recorder := performInventoryRequest(mux, http.MethodGet, adminToken, path, "")

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
			if service.listCalled {
				t.Fatal("ListRoomTypeInventory was called for an invalid request")
			}
		})
	}
}

func TestInventoryHandlerSetMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid room type ID", err: inventory.ErrInvalidRoomTypeID, wantStatus: http.StatusBadRequest},
		{name: "invalid range", err: inventory.ErrInvalidDateRange, wantStatus: http.StatusBadRequest},
		{name: "past date", err: inventory.ErrInventoryDateInPast, wantStatus: http.StatusBadRequest},
		{name: "range too large", err: inventory.ErrInventoryRangeLarge, wantStatus: http.StatusBadRequest},
		{name: "invalid blocked rooms", err: inventory.ErrInvalidBlockedRooms, wantStatus: http.StatusBadRequest},
		{name: "invalid reason", err: inventory.ErrInvalidBlockReason, wantStatus: http.StatusBadRequest},
		{name: "room type not found", err: inventory.ErrRoomTypeNotFound, wantStatus: http.StatusNotFound},
		{name: "inventory conflict", err: inventory.ErrInventoryConflict, wantStatus: http.StatusConflict},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeInventoryService{setErr: tt.err}
			mux, adminToken, _ := newInventoryMux(t, service)
			recorder := performInventoryRequest(
				mux,
				http.MethodPut,
				adminToken,
				"/admin/room-types/7/blocked-inventory",
				validSetInventoryBody(),
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.setCalled {
				t.Fatal("SetBlockedInventory was not called")
			}
		})
	}
}

func TestInventoryHandlerListMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid room type ID", err: inventory.ErrInvalidRoomTypeID, wantStatus: http.StatusBadRequest},
		{name: "invalid range", err: inventory.ErrInvalidDateRange, wantStatus: http.StatusBadRequest},
		{name: "range too large", err: inventory.ErrInventoryRangeLarge, wantStatus: http.StatusBadRequest},
		{name: "room type not found", err: inventory.ErrRoomTypeNotFound, wantStatus: http.StatusNotFound},
		{name: "unexpected", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeInventoryService{listErr: tt.err}
			mux, adminToken, _ := newInventoryMux(t, service)
			recorder := performInventoryRequest(
				mux,
				http.MethodGet,
				adminToken,
				"/admin/room-types/7/inventory?date_from=2030-05-10&date_to=2030-05-11",
				"",
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if !service.listCalled {
				t.Fatal("ListRoomTypeInventory was not called")
			}
		})
	}
}

func TestInventoryRoutesRequireAdmin(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		useToken   func(adminToken, customerToken string) string
		wantStatus int
	}{
		{name: "set missing token", method: http.MethodPut, path: "/admin/room-types/7/blocked-inventory", body: validSetInventoryBody(), useToken: func(_, _ string) string { return "" }, wantStatus: http.StatusUnauthorized},
		{name: "set customer token", method: http.MethodPut, path: "/admin/room-types/7/blocked-inventory", body: validSetInventoryBody(), useToken: func(_, customer string) string { return customer }, wantStatus: http.StatusForbidden},
		{name: "list missing token", method: http.MethodGet, path: "/admin/room-types/7/inventory?date_from=2030-05-10&date_to=2030-05-11", useToken: func(_, _ string) string { return "" }, wantStatus: http.StatusUnauthorized},
		{name: "list customer token", method: http.MethodGet, path: "/admin/room-types/7/inventory?date_from=2030-05-10&date_to=2030-05-11", useToken: func(_, customer string) string { return customer }, wantStatus: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeInventoryService{}
			mux, adminToken, customerToken := newInventoryMux(t, service)
			recorder := performInventoryRequest(
				mux,
				tt.method,
				tt.useToken(adminToken, customerToken),
				tt.path,
				tt.body,
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if service.setCalled || service.listCalled {
				t.Fatal("inventory service was called without admin authorization")
			}
		})
	}
}

func newInventoryMux(t *testing.T, service inventoryService) (*http.ServeMux, string, string) {
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
	adminToken, err := tokenManager.GenerateToken(10, auth.RoleAdmin)
	if err != nil {
		t.Fatalf("generate admin token: %v", err)
	}
	customerToken, err := tokenManager.GenerateToken(20, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("generate customer token: %v", err)
	}

	handler := NewInventoryHandler(service)
	authMiddleware := middleware.NewAuthMiddleware(tokenManager)
	adminOnly := func(handler http.HandlerFunc) http.Handler {
		return authMiddleware.Authenticate(
			middleware.RequireRole(auth.RoleAdmin, handler),
		)
	}
	mux := http.NewServeMux()
	mux.Handle("PUT /admin/room-types/{roomTypeID}/blocked-inventory", adminOnly(handler.BlockedInventory))
	mux.Handle("GET /admin/room-types/{roomTypeID}/inventory", adminOnly(handler.ListRoomTypeInventory))
	return mux, adminToken, customerToken
}

func performInventoryRequest(
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

func inventoryTestRow(
	date string,
	totalRooms int32,
	roomsBooked int32,
	roomsBlocked int32,
	blockReason string,
) sqlc.ListRoomTypeInventoryRow {
	parsedDate, err := time.Parse(time.DateOnly, date)
	if err != nil {
		panic(err)
	}
	reason := pgtype.Text{}
	if blockReason != "" {
		reason = pgtype.Text{String: blockReason, Valid: true}
	}
	return sqlc.ListRoomTypeInventoryRow{
		Date:           pgtype.Date{Time: parsedDate, Valid: true},
		TotalRooms:     totalRooms,
		RoomsBooked:    roomsBooked,
		RoomsBlocked:   roomsBlocked,
		BlockReason:    reason,
		RoomsAvailable: totalRooms - roomsBooked - roomsBlocked,
	}
}

func validSetInventoryBody() string {
	return `{"date_from":"2030-05-10","date_to":"2030-05-11","rooms_blocked":1,"block_reason":"Maintenance"}`
}

func assertHandlerDate(t *testing.T, got time.Time, want string) {
	t.Helper()
	if got.Format(time.DateOnly) != want {
		t.Errorf("date = %s, want %s", got.Format(time.DateOnly), want)
	}
}

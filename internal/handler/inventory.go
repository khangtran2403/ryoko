package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/inventory"
	"github.com/khangtran2403/ryoko/internal/middleware"
)

type inventoryService interface {
	SetBlockedInventory(ctx context.Context, input inventory.SetBlockedInventoryInput) ([]sqlc.ListRoomTypeInventoryRow, error)

	ListRoomTypeInventory(ctx context.Context, input inventory.ListInventoryInput) ([]sqlc.ListRoomTypeInventoryRow, error)
}

type InventoryHandler struct {
	service inventoryService
}

func NewInventoryHandler(service inventoryService) *InventoryHandler {
	return &InventoryHandler{service: service}
}

type InventoryDayResponse struct {
	Date           string  `json:"date"`
	TotalRooms     int32   `json:"total_rooms"`
	RoomsBooked    int32   `json:"rooms_booked"`
	RoomsBlocked   int32   `json:"rooms_blocked"`
	RoomsAvailable int32   `json:"rooms_available"`
	BlockReason    *string `json:"block_reason"`
}

type RoomTypeInventoryResponse struct {
	Inventory []InventoryDayResponse `json:"inventory"`
}
type SetBlockedInventoryRequest struct {
	DateFrom     string `json:"date_from"`
	DateTo       string `json:"date_to"`
	RoomsBlocked int32  `json:"rooms_blocked"`
	BlockReason  string `json:"block_reason"`
}

func (h *InventoryHandler) BlockedInventory(w http.ResponseWriter, r *http.Request) {
	var req SetBlockedInventoryRequest
	roomType := r.PathValue("roomTypeID")
	if roomType == "" {
		http.Error(w, "room type id is empty", http.StatusBadRequest)
		return
	}
	convId, err := strconv.ParseInt(roomType, 10, 64)
	if err != nil || convId <= 0 {
		http.Error(w, "invalid room type id", http.StatusBadRequest)
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
	dateFrom, err := time.Parse(time.DateOnly, req.DateFrom)
	if err != nil {
		http.Error(w, "invalid date_from format", http.StatusBadRequest)
		return
	}
	dateTo, err := time.Parse(time.DateOnly, req.DateTo)
	if err != nil {
		http.Error(w, "invalid date_to format", http.StatusBadRequest)
		return
	}
	setBlocked, err := h.service.SetBlockedInventory(r.Context(), inventory.SetBlockedInventoryInput{
		RoomTypeID:   convId,
		DateFrom:     dateFrom,
		DateTo:       dateTo,
		RoomsBlocked: req.RoomsBlocked,
		BlockReason:  req.BlockReason,
	})
	switch {
	case errors.Is(err, inventory.ErrInvalidRoomTypeID),
		errors.Is(err, inventory.ErrInvalidDateRange),
		errors.Is(err, inventory.ErrInventoryDateInPast),
		errors.Is(err, inventory.ErrInventoryRangeLarge),
		errors.Is(err, inventory.ErrInvalidBlockedRooms),
		errors.Is(err, inventory.ErrInvalidBlockReason):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, inventory.ErrRoomTypeNotFound):
		http.Error(w, "room type not found", http.StatusNotFound)
		return
	case errors.Is(err, inventory.ErrInventoryConflict):
		http.Error(w, "inventory conflict", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "set blocked failed", http.StatusInternalServerError)
		return
	default:
		response, err := inventoryResponseFromModels(setBlocked)
		if err != nil {
			http.Error(w, "Failed to format", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}
func (h *InventoryHandler) ListRoomTypeInventory(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	roomType := r.PathValue("roomTypeID")
	if roomType == "" {
		http.Error(w, "room type id is empty", http.StatusBadRequest)
		return
	}
	convId, err := strconv.ParseInt(roomType, 10, 64)
	if err != nil || convId <= 0 {
		http.Error(w, "invalid room type id", http.StatusBadRequest)
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
	dateFrom, err := time.Parse(time.DateOnly, query.Get("date_from"))
	if err != nil {
		http.Error(w, "invalid date_from format", http.StatusBadRequest)
		return
	}
	dateTo, err := time.Parse(time.DateOnly, query.Get("date_to"))
	if err != nil {
		http.Error(w, "invalid date_to format", http.StatusBadRequest)
		return
	}
	list, err := h.service.ListRoomTypeInventory(r.Context(), inventory.ListInventoryInput{
		RoomTypeID: convId,
		DateFrom:   dateFrom,
		DateTo:     dateTo,
	})
	switch {
	case errors.Is(err, inventory.ErrInvalidRoomTypeID),
		errors.Is(err, inventory.ErrInvalidDateRange),
		errors.Is(err, inventory.ErrInventoryRangeLarge):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return

	case errors.Is(err, inventory.ErrRoomTypeNotFound):
		http.Error(w, "room type not found", http.StatusNotFound)
		return

	case err != nil:
		http.Error(
			w,
			"list inventory failed",
			http.StatusInternalServerError,
		)
	default:
		response, err := inventoryResponseFromModels(list)
		if err != nil {
			http.Error(w, "Failed to format", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}
func inventoryDayResponseFromModel(
	model sqlc.ListRoomTypeInventoryRow,
) (InventoryDayResponse, error) {
	if !model.Date.Valid {
		return InventoryDayResponse{},
			fmt.Errorf("invalid inventory: date is missing")
	}

	var blockReason *string
	if model.BlockReason.Valid {
		value := model.BlockReason.String
		blockReason = &value
	}

	return InventoryDayResponse{
		Date:           model.Date.Time.Format(time.DateOnly),
		TotalRooms:     model.TotalRooms,
		RoomsBooked:    model.RoomsBooked,
		RoomsBlocked:   model.RoomsBlocked,
		RoomsAvailable: model.RoomsAvailable,
		BlockReason:    blockReason,
	}, nil
}

func inventoryResponseFromModels(
	models []sqlc.ListRoomTypeInventoryRow,
) (RoomTypeInventoryResponse, error) {
	inventoryDays := make(
		[]InventoryDayResponse,
		0,
		len(models),
	)

	for _, model := range models {
		response, err := inventoryDayResponseFromModel(model)
		if err != nil {
			return RoomTypeInventoryResponse{},
				fmt.Errorf("format inventory date: %w", err)
		}

		inventoryDays = append(inventoryDays, response)
	}

	return RoomTypeInventoryResponse{
		Inventory: inventoryDays,
	}, nil
}

package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type RoomTypeHandler struct {
	queries *sqlc.Queries
}
type CreateRoomTypeRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	PricePerNight string `json:"price_per_night"`
	Capacity      int32  `json:"capacity"`
	TotalRooms    int32  `json:"total_rooms"`
}

func NewRoomTypeHandler(queries *sqlc.Queries) *RoomTypeHandler {
	return &RoomTypeHandler{
		queries: queries,
	}
}
func (req CreateRoomTypeRequest) Validate() []string {
	var problems []string
	if strings.TrimSpace(req.Name) == "" {
		problems = append(problems, "name is required")
	}
	var price pgtype.Numeric
	if err := price.Scan(req.PricePerNight); err != nil {
		problems = append(problems, "price per night must be a valid decimal number")
	} else if f, err := price.Float64Value(); err != nil || f.Float64 <= 0 {
		problems = append(problems, "price per night must be greater than zero")
	}

	if req.Capacity <= 0 {
		problems = append(problems, "capacity cannot be zero or negative")
	}
	if req.TotalRooms <= 0 {
		problems = append(problems, "total rooms cannot be zero or negative")
	}
	return problems
}

func (h *RoomTypeHandler) CreateRoomType(w http.ResponseWriter, r *http.Request) {
	var req CreateRoomTypeRequest
	var pgErr *pgconn.PgError

	getHotelid := r.PathValue("hotelID")
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	convHotelID, err := strconv.ParseInt(getHotelid, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	if problems := req.Validate(); len(problems) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"errors": problems})
		return
	}
	var price pgtype.Numeric
	// error already ruled out by Validate() above
	_ = price.Scan(req.PricePerNight)

	c, err := h.queries.CreateRoomType(r.Context(), sqlc.CreateRoomTypeParams{
		HotelID:       convHotelID,
		Name:          req.Name,
		Description:   PgtypeconvertToString(req.Description),
		PricePerNight: price,
		Capacity:      req.Capacity,
		TotalRooms:    req.TotalRooms,
	})
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		http.Error(w, "hotel does not exist", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "Failed to create room type", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(c)
}
func (h *RoomTypeHandler) GetRoomTypeByID(w http.ResponseWriter, r *http.Request) {
	getId := r.PathValue("id")
	if getId == "" {
		http.Error(w, "Missing room type ID", http.StatusBadRequest)
		return
	}
	convId, err := strconv.ParseInt(getId, 10, 64)
	if err != nil {
		http.Error(w, "Invalid room type ID", http.StatusBadRequest)
		return
	}
	getRoomType, err := h.queries.GetRoomTypeByID(r.Context(), convId)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Room type not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to get room type", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(getRoomType)
}
func (h *RoomTypeHandler) ListRoomTypesByHotel(w http.ResponseWriter, r *http.Request) {
	getHotelid := r.PathValue("hotelID")
	convHotelID, err := strconv.ParseInt(getHotelid, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	listRoomTypes, err := h.queries.ListRoomTypesByHotel(r.Context(), convHotelID)
	if err != nil {
		http.Error(w, "Failed to list room types", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(listRoomTypes)
}
func (h *RoomTypeHandler) UpdateRoomType(w http.ResponseWriter, r *http.Request) {
	var req CreateRoomTypeRequest
	getId := r.PathValue("id")
	if getId == "" {
		http.Error(w, "Missing room type ID", http.StatusBadRequest)
		return
	}
	convId, err := strconv.ParseInt(getId, 10, 64)
	if err != nil {
		http.Error(w, "Invalid room type ID", http.StatusBadRequest)
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if problems := req.Validate(); len(problems) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"errors": problems})
		return
	}
	var price pgtype.Numeric
	// error already ruled out by Validate() above
	_ = price.Scan(req.PricePerNight)

	updatedRoomType, err := h.queries.UpdateRoomType(r.Context(), sqlc.UpdateRoomTypeParams{
		ID:            convId,
		Name:          req.Name,
		Description:   PgtypeconvertToString(req.Description),
		PricePerNight: price,
		Capacity:      req.Capacity,
		TotalRooms:    req.TotalRooms,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Room type not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to update room type", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(updatedRoomType)
}
func (h *RoomTypeHandler) DeleteRoomType(w http.ResponseWriter, r *http.Request) {
	getId := r.PathValue("id")
	if getId == "" {
		http.Error(w, "Missing room type ID", http.StatusBadRequest)
		return
	}
	convId, err := strconv.ParseInt(getId, 10, 64)
	if err != nil {
		http.Error(w, "Invalid room type ID", http.StatusBadRequest)
		return
	}
	_, err = h.queries.DeleteRoomType(r.Context(), convId)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Room type not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to delete room type", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

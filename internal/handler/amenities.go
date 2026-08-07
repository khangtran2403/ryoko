package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type AmenityHandler struct {
	queries *sqlc.Queries
}

type CreateAmenityRequest struct {
	Name string `json:"name"`
}

type AttachAmenitytoHotelRequest struct {
	AmenityID int64 `json:"amenity_id"`
}

func NewAmenityHandler(queries *sqlc.Queries) *AmenityHandler {
	return &AmenityHandler{
		queries: queries,
	}
}

func (h *AmenityHandler) CreateAmenity(w http.ResponseWriter, r *http.Request) {
	var req CreateAmenityRequest
	var pgErr *pgconn.PgError

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "Amenity name is required", http.StatusBadRequest)
		return
	}

	c, err := h.queries.CreateAmenity(r.Context(), name)
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		http.Error(w, "Amenity name must be unique", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Failed to create amenity", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(c)
}
func (h *AmenityHandler) ListAmenities(w http.ResponseWriter, r *http.Request) {
	amenities, err := h.queries.ListAmenities(r.Context())
	if err != nil {
		http.Error(w, "Failed to list amenities", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(amenities)
}
func (h *AmenityHandler) AddAmenityToHotel(w http.ResponseWriter, r *http.Request) {
	var req AttachAmenitytoHotelRequest
	var pgErr *pgconn.PgError

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.AmenityID <= 0 {
		http.Error(w, "Invalid amenity ID", http.StatusBadRequest)
		return
	}
	getHotelid := r.PathValue("hotelID")
	if getHotelid == "" {
		http.Error(w, "Missing hotel ID", http.StatusBadRequest)
		return
	}
	convHotelID, err := strconv.ParseInt(getHotelid, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	addAmenity, err := h.queries.AddAmenityToHotel(r.Context(), sqlc.AddAmenityToHotelParams{
		HotelID:   convHotelID,
		AmenityID: req.AmenityID,
	})
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			http.Error(w, "Hotel or amenity not found", http.StatusNotFound)
			return
		case "23505":
			http.Error(w, "Amenity already added to hotel", http.StatusConflict)
			return
		}
	}
	if err != nil {
		http.Error(w, "Failed to add amenity to hotel", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(addAmenity)
}
func (h *AmenityHandler) RemoveAmenitiesFromHotel(w http.ResponseWriter, r *http.Request) {
	getAmenityID := r.PathValue("amenityID")
	if getAmenityID == "" {
		http.Error(w, "Missing amenity ID", http.StatusBadRequest)
		return
	}
	convAmenityID, err := strconv.ParseInt(getAmenityID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid amenity ID", http.StatusBadRequest)
		return
	}
	getHotelid := r.PathValue("hotelID")
	if getHotelid == "" {
		http.Error(w, "Missing hotel ID", http.StatusBadRequest)
		return
	}
	convHotelID, err := strconv.ParseInt(getHotelid, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	_, err = h.queries.RemoveAmenityFromHotel(r.Context(), sqlc.RemoveAmenityFromHotelParams{
		HotelID:   convHotelID,
		AmenityID: convAmenityID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Hotel or Amenity not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to remove amenity from hotel", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *AmenityHandler) ListAmenitiesByHotel(w http.ResponseWriter, r *http.Request) {
	getHotelid := r.PathValue("hotelID")
	if getHotelid == "" {
		http.Error(w, "Missing hotel ID", http.StatusBadRequest)
		return
	}
	convHotelID, err := strconv.ParseInt(getHotelid, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	amenities, err := h.queries.ListAmenitiesByHotel(r.Context(), convHotelID)
	if err != nil {
		http.Error(w, "Failed to list amenities", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(amenities)
}

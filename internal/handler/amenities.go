package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/khangtran2403/ryoko/internal/amenities"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type amenityService interface {
	CreateAmenity(ctx context.Context, name string) (sqlc.Amenity, error)
	UpdateAmenity(ctx context.Context, amenityID int64, name string) (sqlc.Amenity, error)
	DeleteAmenity(ctx context.Context, amenityID int64) error
	ListAmenities(ctx context.Context) ([]sqlc.Amenity, error)
	AddAmenityToHotel(ctx context.Context, hotelID int64, amenityID int64) (sqlc.HotelAmenity, error)
	RemoveAmenityFromHotel(ctx context.Context, hotelID int64, amenityID int64) error
	ListAmenitiesByHotel(ctx context.Context, hotelID int64) ([]sqlc.Amenity, error)
}
type AmenityHandler struct {
	amenityService amenityService
}

type CreateAmenityRequest struct {
	Name string `json:"name"`
}

type AttachAmenityToHotelRequest struct {
	AmenityID int64 `json:"amenity_id"`
}

func NewAmenityHandler(amenityService amenityService) *AmenityHandler {
	return &AmenityHandler{
		amenityService: amenityService,
	}
}

func (h *AmenityHandler) CreateAmenity(w http.ResponseWriter, r *http.Request) {
	var req CreateAmenityRequest

	if !decodeJSONRequest(w, r, &req, false) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "Amenity name is required", http.StatusBadRequest)
		return
	}

	c, err := h.amenityService.CreateAmenity(r.Context(), name)
	if errors.Is(err, amenities.ErrInvalidAmenityName) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, amenities.ErrAmenityNameConflict) {
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

func (h *AmenityHandler) UpdateAmenity(w http.ResponseWriter, r *http.Request) {
	amenityID, err := strconv.ParseInt(r.PathValue("amenityID"), 10, 64)
	if err != nil || amenityID <= 0 {
		http.Error(w, "Invalid amenity ID", http.StatusBadRequest)
		return
	}

	var req CreateAmenityRequest
	if !decodeJSONRequest(w, r, &req, false) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "Amenity name is required", http.StatusBadRequest)
		return
	}

	updated, err := h.amenityService.UpdateAmenity(r.Context(), amenityID, name)
	switch {
	case errors.Is(err, amenities.ErrInvalidAmenityID),
		errors.Is(err, amenities.ErrInvalidAmenityName):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, amenities.ErrAmenityNotFound):
		http.Error(w, "Amenity not found", http.StatusNotFound)
		return
	case errors.Is(err, amenities.ErrAmenityNameConflict):
		http.Error(w, "Amenity name must be unique", http.StatusConflict)
		return
	case err != nil:
		http.Error(w, "Failed to update amenity", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(updated)
}

func (h *AmenityHandler) DeleteAmenity(w http.ResponseWriter, r *http.Request) {
	amenityID, err := strconv.ParseInt(r.PathValue("amenityID"), 10, 64)
	if err != nil || amenityID <= 0 {
		http.Error(w, "Invalid amenity ID", http.StatusBadRequest)
		return
	}

	err = h.amenityService.DeleteAmenity(r.Context(), amenityID)
	switch {
	case errors.Is(err, amenities.ErrInvalidAmenityID):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case errors.Is(err, amenities.ErrAmenityNotFound):
		http.Error(w, "Amenity not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, "Failed to delete amenity", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *AmenityHandler) ListAmenities(w http.ResponseWriter, r *http.Request) {
	amenities, err := h.amenityService.ListAmenities(r.Context())
	if err != nil {
		http.Error(w, "Failed to list amenities", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(amenities)
}
func (h *AmenityHandler) AddAmenityToHotel(w http.ResponseWriter, r *http.Request) {
	var req AttachAmenityToHotelRequest

	if !decodeJSONRequest(w, r, &req, false) {
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
	if err != nil || convHotelID <= 0 {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	addAmenity, err := h.amenityService.AddAmenityToHotel(r.Context(), convHotelID, req.AmenityID)
	if errors.Is(err, amenities.ErrInvalidHotelID) || errors.Is(err, amenities.ErrInvalidAmenityID) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, amenities.ErrHotelOrAmenityNotFound) {
		http.Error(w, "Hotel or amenity not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, amenities.ErrAmenityAlreadyAdded) {
		http.Error(w, "Amenity already added to hotel", http.StatusConflict)
		return

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
	if err != nil || convAmenityID <= 0 {
		http.Error(w, "Invalid amenity ID", http.StatusBadRequest)
		return
	}
	getHotelid := r.PathValue("hotelID")
	if getHotelid == "" {
		http.Error(w, "Missing hotel ID", http.StatusBadRequest)
		return
	}
	convHotelID, err := strconv.ParseInt(getHotelid, 10, 64)
	if err != nil || convHotelID <= 0 {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	err = h.amenityService.RemoveAmenityFromHotel(r.Context(), convHotelID, convAmenityID)
	if errors.Is(err, amenities.ErrInvalidHotelID) || errors.Is(err, amenities.ErrInvalidAmenityID) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if errors.Is(err, amenities.ErrHotelOrAmenityNotFound) {
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
	if err != nil || convHotelID <= 0 {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	list, err := h.amenityService.ListAmenitiesByHotel(r.Context(), convHotelID)
	if errors.Is(err, amenities.ErrHotelNotFound) {
		http.Error(w, "Hotel not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to list amenities", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(list)
}

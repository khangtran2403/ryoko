package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/hotel_images"
)

type hotelImageService interface {
	CreateHotelImage(ctx context.Context, hotelID int64, imageURL string) (sqlc.HotelImage, error)
	ListHotelImages(ctx context.Context, hotelID int64) ([]sqlc.HotelImage, error)
	SetPrimaryHotelImage(ctx context.Context, hotelID int64, imageID int64) (sqlc.HotelImage, error)
	DeleteHotelImage(ctx context.Context, hotelID int64, imageID int64) error
}

type HotelImageHandler struct {
	service hotelImageService
}

func NewHotelImageHandler(service hotelImageService) *HotelImageHandler {
	return &HotelImageHandler{
		service: service,
	}
}

type CreateHotelImageRequest struct {
	ImageURL string `json:"image_url"`
}

func (h *HotelImageHandler) CreateHotelImage(w http.ResponseWriter, r *http.Request) {
	var req CreateHotelImageRequest
	hotelID := r.PathValue("hotelID")
	convID, err := strconv.ParseInt(hotelID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	if convID <= 0 {
		http.Error(w, "hotel ID must be positive", http.StatusBadRequest)
		return
	}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	c, err := h.service.CreateHotelImage(r.Context(), convID, req.ImageURL)
	switch {
	case errors.Is(err, hotel_images.ErrInvalidImageURL):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return

	case errors.Is(err, hotel_images.ErrHotelNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
		return

	case err != nil:
		http.Error(w, "create hotel image failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(c)
}
func (h *HotelImageHandler) ListHotelImages(w http.ResponseWriter, r *http.Request) {
	hotelID := r.PathValue("hotelID")
	convID, err := strconv.ParseInt(hotelID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	if convID <= 0 {
		http.Error(w, "hotel ID must be positive", http.StatusBadRequest)
		return
	}
	list, err := h.service.ListHotelImages(r.Context(), convID)
	if err != nil {
		http.Error(w, "list hotel images failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(list)
}
func (h *HotelImageHandler) SetPrimaryHotelImage(w http.ResponseWriter, r *http.Request) {
	hotelID := r.PathValue("hotelID")
	convID, err := strconv.ParseInt(hotelID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	if convID <= 0 {
		http.Error(w, "hotel ID must be positive", http.StatusBadRequest)
		return
	}
	imageID := r.PathValue("imageID")
	imgconvID, err := strconv.ParseInt(imageID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid image ID", http.StatusBadRequest)
		return
	}
	if imgconvID <= 0 {
		http.Error(w, "image ID must be positive", http.StatusBadRequest)
		return
	}
	set, err := h.service.SetPrimaryHotelImage(r.Context(), convID, imgconvID)
	switch {
	case errors.Is(err, hotel_images.ErrHotelNotFound),
		errors.Is(err, hotel_images.ErrImageNotFound):
		http.Error(w, "Hotel or image not found", http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, "set primary hotel image failed", http.StatusInternalServerError)
		return
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(set)
	}
}
func (h *HotelImageHandler) DeleteHotelImage(w http.ResponseWriter, r *http.Request) {
	hotelID := r.PathValue("hotelID")
	convID, err := strconv.ParseInt(hotelID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid hotel ID", http.StatusBadRequest)
		return
	}
	if convID <= 0 {
		http.Error(w, "hotel ID must be positive", http.StatusBadRequest)
		return
	}
	imageID := r.PathValue("imageID")
	imgconvID, err := strconv.ParseInt(imageID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid image ID", http.StatusBadRequest)
		return
	}
	if imgconvID <= 0 {
		http.Error(w, "image ID must be positive", http.StatusBadRequest)
		return
	}
	err = h.service.DeleteHotelImage(r.Context(), convID, imgconvID)
	if errors.Is(err, hotel_images.ErrImageNotFound) {
		http.Error(w, "image not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "delete hotel image failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

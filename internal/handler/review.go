package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/middleware"
	"github.com/khangtran2403/ryoko/internal/review"
)

type reviewService interface {
	CreateReview(ctx context.Context, input review.CreateReview) (sqlc.Review, error)
	GetReviewByID(ctx context.Context, reviewID int64) (sqlc.GetReviewByIDRow, error)
	ListReviewByHotel(ctx context.Context, hotelID int64) ([]sqlc.ListReviewsByHotelRow, error)
}

type ReviewHandler struct {
	service reviewService
}

func NewReviewHandler(service reviewService) *ReviewHandler {
	return &ReviewHandler{
		service: service,
	}
}

type CreateReviewRequest struct {
	Rating  int16   `json:"rating"`
	Comment *string `json:"comment"`
}

func (h *ReviewHandler) CreateReview(w http.ResponseWriter, r *http.Request) {
	var req CreateReviewRequest
	bookingID := r.PathValue("bookingID")
	convID, err := strconv.ParseInt(bookingID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid booking ID", http.StatusBadRequest)
		return
	}
	if convID <= 0 {
		http.Error(w, "booking ID must be positive", http.StatusBadRequest)
		return
	}
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	c, err := h.service.CreateReview(r.Context(), review.CreateReview{
		Rating:    req.Rating,
		Comment:   req.Comment,
		BookingID: convID,
		UserID:    principal.UserID,
	})
	switch {
	case errors.Is(err, review.ErrInvalidRating),
		errors.Is(err, review.ErrBlankComment):
		http.Error(w, err.Error(), http.StatusBadRequest)

	case errors.Is(err, review.ErrBookingNotReviewable):
		http.Error(w, err.Error(), http.StatusConflict)

	case errors.Is(err, review.ErrReviewAlreadyExists):
		http.Error(w, "Review already exists", http.StatusConflict)

	case err != nil:
		http.Error(w, "create review failed", http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(c)
	}
}
func (h ReviewHandler) GetReviewByID(w http.ResponseWriter, r *http.Request) {
	getID := r.PathValue("reviewID")
	if getID == "" {
		http.Error(w, "review ID is required", http.StatusBadRequest)
		return
	}
	convID, err := strconv.ParseInt(getID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	getReview, err := h.service.GetReviewByID(r.Context(), convID)
	if errors.Is(err, review.ErrReviewNotFound) {
		http.Error(w, "Review not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "get review failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(getReview)
}
func (h ReviewHandler) ListReviewByHotel(w http.ResponseWriter, r *http.Request) {
	getID := r.PathValue("hotelID")
	if getID == "" {
		http.Error(w, "hotel ID is required", http.StatusBadRequest)
		return
	}
	convID, err := strconv.ParseInt(getID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	getReview, err := h.service.ListReviewByHotel(r.Context(), convID)
	if errors.Is(err, review.ErrReviewNotFound) {
		http.Error(w, "Review not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "list review failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(getReview)
}

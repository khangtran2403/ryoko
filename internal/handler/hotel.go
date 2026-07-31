package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type HotelHandler struct {
	queries *sqlc.Queries
}

type CreateHotelRequest struct{
	Name string
	Address string
	City string 
	Description string
}
func NewHotelHandler(queries *sqlc.Queries) *HotelHandler {
	return &HotelHandler{
		queries: queries,
	}
}
func (req CreateHotelRequest) Validate() []string {
	var problems []string
	if strings.TrimSpace(req.Name) == "" {
		problems = append(problems, "name is required")
	}
	if strings.TrimSpace(req.Address) == "" {
		problems = append(problems, "address is required")
	}
	if strings.TrimSpace(req.City) == "" {
		problems = append(problems, "city is required")
	}
	return problems
}


func (h *HotelHandler) Create(w http.ResponseWriter,r *http.Request)  {
  var req CreateHotelRequest
  err := json.NewDecoder(r.Body).Decode(&req)
  if err != nil {
	http.Error(w, "Invalid request body", http.StatusBadRequest)
	return
  }
  	if problems := req.Validate(); len(problems) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"errors": problems})
		return
	}

   c,err := h.queries.CreateHotel(r.Context(),sqlc.CreateHotelParams{
	Name:        req.Name,
	Address:     req.Address,
	City:        req.City,
	Description: PgtypeconvertToString(req.Description),
 })
 if err != nil {
	http.Error(w, "Failed to create hotel", http.StatusInternalServerError)
	return
 }
   w.Header().Set("Content-Type", "application/json")
   w.WriteHeader(http.StatusCreated)
   json.NewEncoder(w).Encode(c)
}
func (h *HotelHandler) GetByID(w http.ResponseWriter,r *http.Request) {
    getId := r.PathValue("id")
	if getId == "" {
		http.Error(w, "Missing hotel ID", http.StatusBadRequest)
		return
	}
	convId, err := strconv.ParseInt(getId, 10, 64)
	if err != nil {
		http.Error(w, "invalid hotel id", http.StatusBadRequest)
		return
	}

	getHotel, err := h.queries.GetHotelByID(r.Context(),convId)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Hotel not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "failed to fetch hotel", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(getHotel)
}
func (h *HotelHandler) ListHotelsByCity(w http.ResponseWriter,r *http.Request) {
   c := r.URL.Query().Get("city")
   if c == "" {
		http.Error(w, "Missing city", http.StatusBadRequest)
		return
	}
	hotels, err := h.queries.ListHotelsByCity(r.Context(), c)
	if err != nil {
		http.Error(w, "failed to list hotels", http.StatusInternalServerError)
		return

	}
	// hotels is []Hotel{} (empty, not nil-error) when there are zero matches
	// that's a valid 200 with an empty array, not a 404.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(hotels)
}
// stringToPgText converts a plain (possibly empty) Go string into pgx's
// nullable text type. Empty string is treated as NULL, not as an empty
// stored value.
func PgtypeconvertToString(s string) pgtype.Text {
	if s == ""{
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}
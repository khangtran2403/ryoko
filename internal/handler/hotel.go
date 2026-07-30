package handler

import (
	"encoding/json"
	"net/http"
	"strings"

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

func PgtypeconvertToString(s string) pgtype.Text {
	if s == ""{
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}
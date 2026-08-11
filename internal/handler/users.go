package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type UserHandler struct {
	queries *sqlc.Queries
}

type CreateUserRequest struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
}

func NewUserHandler(queries *sqlc.Queries) *UserHandler {
	return &UserHandler{
		queries: queries,
	}
}
func (req *CreateUserRequest) Normalize() {
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.FullName = strings.TrimSpace(req.FullName)
	req.Phone = strings.TrimSpace(req.Phone)
}
func (req CreateUserRequest) Validate() []string {
	var problems []string
	if req.Email == "" {
		problems = append(problems, "email is required")
	} else {
		address, err := mail.ParseAddress(req.Email)
		if err != nil || address.Address != req.Email {
			problems = append(problems, "email must be valid")
		}
	}
	if req.FullName == "" {
		problems = append(problems, "full name is required")
	}

	return problems
}

func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	var pgErr *pgconn.PgError
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	//normalize before validate
	req.Normalize()
	if problems := req.Validate(); len(problems) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"errors": problems})
		return
	}
	c, err := h.queries.CreateUser(r.Context(), sqlc.CreateUserParams{
		Email:    req.Email,
		FullName: req.FullName,
		Phone:    PgtypeconvertToString(req.Phone),
	})
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		http.Error(w, "Email is already in use", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Failed to create user", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(c)
}
func (h *UserHandler) GetUserByID(w http.ResponseWriter, r *http.Request) {
	getId := r.PathValue("id")
	if getId == "" {
		http.Error(w, "Missing user ID", http.StatusBadRequest)
		return
	}
	convID, err := strconv.ParseInt(getId, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}
	c, err := h.queries.GetUserByID(r.Context(), convID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to get user", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(c)
}
func (h *UserHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	var pgErr *pgconn.PgError
	getId := r.PathValue("id")
	if getId == "" {
		http.Error(w, "Missing user ID", http.StatusBadRequest)
		return
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	convID, err := strconv.ParseInt(getId, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}
	req.Normalize()
	if problems := req.Validate(); len(problems) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{"errors": problems})
		return
	}
	u, err := h.queries.UpdateUser(r.Context(), sqlc.UpdateUserParams{
		ID:       convID,
		Email:    req.Email,
		FullName: req.FullName,
		Phone:    PgtypeconvertToString(req.Phone),
	})
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		http.Error(w, "Informations already in use", http.StatusConflict)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to update user", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(u)
}
func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	getId := r.PathValue("id")
	if getId == "" {
		http.Error(w, "Missing user ID", http.StatusBadRequest)
		return
	}
	convID, err := strconv.ParseInt(getId, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}
	_, err = h.queries.DeleteUser(r.Context(), convID)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "Failed to delete user", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

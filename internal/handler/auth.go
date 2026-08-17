package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

type AuthHandler struct {
	queries *sqlc.Queries
	tokens  *auth.TokenManager
}

type RegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
	Phone    string `json:"phone"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

func NewAuthHandler(queries *sqlc.Queries, tokens *auth.TokenManager) *AuthHandler {
	return &AuthHandler{
		queries: queries,
		tokens:  tokens,
	}
}

func (h *AuthHandler) RegisterUser(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	var pgErr *pgconn.PgError
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	profile := CreateUserRequest{
		Email:    req.Email,
		FullName: req.FullName,
		Phone:    req.Phone,
	}
	profile.Normalize()
	if problems := profile.Validate(); len(problems) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"errors": problems,
		})
		return
	}
	req.Email = profile.Email
	req.FullName = profile.FullName
	req.Phone = profile.Phone

	if err := auth.ValidatePassword(req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		http.Error(w, "Failed to hash password", http.StatusInternalServerError)
		return
	}
	user, err := h.queries.RegisterUser(r.Context(), sqlc.RegisterUserParams{
		Email:        req.Email,
		FullName:     req.FullName,
		Phone:        PgtypeconvertToString(req.Phone),
		PasswordHash: PgtypeconvertToString(hash),
	})
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		http.Error(w, "User is already registered", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "Failed to register user", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}
func (h *AuthHandler) LoginUser(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		http.Error(w, "Email and password are required", http.StatusBadRequest)
		return
	}
	user, err := h.queries.GetUserForLogin(r.Context(), req.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "Failed to login", http.StatusInternalServerError)
		return
	}
	if !user.PasswordHash.Valid ||
		!auth.CheckPasswordHash(req.Password, user.PasswordHash.String) {
		http.Error(w, "Invalid email or password", http.StatusUnauthorized)
		return
	}
	token, err := h.tokens.GenerateToken(user.ID, user.Role)
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LoginResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(h.tokens.TTL() / time.Second),
	})
}

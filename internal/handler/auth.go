package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/session"
)

type authSessionService interface {
	IssueTokenPair(ctx context.Context, userID int64, role string) (session.TokenPair, error)

	RotateTokenPair(ctx context.Context, rawRefreshToken string) (session.TokenPair, error)
	RevokeRefreshToken(ctx context.Context, rawRefreshToken string) error
}

type authQueries interface {
	RegisterUser(ctx context.Context, arg sqlc.RegisterUserParams) (sqlc.RegisterUserRow, error)
	GetUserForLogin(ctx context.Context, email string) (sqlc.GetUserForLoginRow, error)
}

type AuthHandler struct {
	queries        authQueries
	sessionService authSessionService
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
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}
type LoginResponse struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	TokenType        string    `json:"token_type"`
	ExpiresIn        int64     `json:"expires_in"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func NewAuthHandler(queries authQueries, sessionService authSessionService) *AuthHandler {
	return &AuthHandler{
		queries:        queries,
		sessionService: sessionService,
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
	token, err := h.sessionService.IssueTokenPair(r.Context(), user.ID, user.Role)
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LoginResponse{
		AccessToken:      token.AccessToken,
		RefreshToken:     token.RefreshToken,
		TokenType:        "Bearer",
		ExpiresIn:        token.AccessExpiresIn,
		RefreshExpiresAt: token.RefreshExpiresAt,
	})
}
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	if req.RefreshToken == "" {
		http.Error(w, "Refresh token is required", http.StatusBadRequest)
		return
	}
	token, err := h.sessionService.RotateTokenPair(r.Context(), req.RefreshToken)
	switch {
	case errors.Is(err, session.ErrInvalidRefreshToken),
		errors.Is(err, session.ErrRefreshTokenExpired),
		errors.Is(err, session.ErrRefreshTokenRevoked),
		errors.Is(err, session.ErrRefreshTokenReused):
		http.Error(w, "Invalid refresh token", http.StatusUnauthorized)
		return

	case err != nil:
		http.Error(w, "Failed to refresh token", http.StatusInternalServerError)
		return

	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(LoginResponse{
			AccessToken:      token.AccessToken,
			RefreshToken:     token.RefreshToken,
			TokenType:        "Bearer",
			ExpiresIn:        token.AccessExpiresIn,
			RefreshExpiresAt: token.RefreshExpiresAt,
		})
	}
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	if req.RefreshToken == "" {
		http.Error(w, "Refresh token is required", http.StatusBadRequest)
		return
	}

	err := h.sessionService.RevokeRefreshToken(r.Context(), req.RefreshToken)
	if errors.Is(err, session.ErrInvalidRefreshToken) {
		http.Error(w, "Invalid refresh token", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "Failed to logout", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

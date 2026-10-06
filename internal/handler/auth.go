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

type refreshCookieManager interface {
	Set(w http.ResponseWriter, rawToken string, expiresAt time.Time) error
	Read(r *http.Request) (string, error)
	Clear(w http.ResponseWriter)
}

type AuthHandler struct {
	queries        authQueries
	sessionService authSessionService
	refreshCookies refreshCookieManager
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
	AccessToken      string    `json:"access_token"`
	TokenType        string    `json:"token_type"`
	ExpiresIn        int64     `json:"expires_in"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

func NewAuthHandler(
	queries authQueries,
	sessionService authSessionService,
	refreshCookies refreshCookieManager,
) *AuthHandler {
	return &AuthHandler{
		queries:        queries,
		sessionService: sessionService,
		refreshCookies: refreshCookies,
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
	if err := writeSessionResponse(w, token, h.refreshCookies); err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
	}
}
func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	rawRefreshToken, err := h.refreshCookies.Read(r)
	if errors.Is(err, session.ErrRefreshCookieMissing) {
		h.refreshCookies.Clear(w)
		http.Error(w, "Invalid refresh token", http.StatusUnauthorized)
		return
	}
	if err != nil {
		http.Error(w, "Failed to read refresh token", http.StatusInternalServerError)
		return
	}
	token, err := h.sessionService.RotateTokenPair(r.Context(), rawRefreshToken)
	switch {
	case errors.Is(err, session.ErrInvalidRefreshToken),
		errors.Is(err, session.ErrRefreshTokenExpired),
		errors.Is(err, session.ErrRefreshTokenRevoked),
		errors.Is(err, session.ErrRefreshTokenReused):
		h.refreshCookies.Clear(w)
		http.Error(w, "Invalid refresh token", http.StatusUnauthorized)
		return

	case err != nil:
		http.Error(w, "Failed to refresh token", http.StatusInternalServerError)
		return

	default:
		if err := writeSessionResponse(w, token, h.refreshCookies); err != nil {
			http.Error(w, "Failed to update session", http.StatusInternalServerError)
		}
	}
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	rawRefreshToken, err := h.refreshCookies.Read(r)
	h.refreshCookies.Clear(w)
	if errors.Is(err, session.ErrRefreshCookieMissing) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		http.Error(w, "Failed to read refresh token", http.StatusInternalServerError)
		return
	}

	err = h.sessionService.RevokeRefreshToken(r.Context(), rawRefreshToken)
	if errors.Is(err, session.ErrInvalidRefreshToken) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		http.Error(w, "Failed to logout", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeSessionResponse(
	w http.ResponseWriter,
	pair session.TokenPair,
	cookies refreshCookieManager,
) error {
	if err := cookies.Set(w, pair.RefreshToken, pair.RefreshExpiresAt); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	return json.NewEncoder(w).Encode(LoginResponse{
		AccessToken:      pair.AccessToken,
		TokenType:        "Bearer",
		ExpiresIn:        pair.AccessExpiresIn,
		RefreshExpiresAt: pair.RefreshExpiresAt,
	})
}

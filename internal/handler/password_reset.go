package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/khangtran2403/ryoko/internal/passwordreset"
)

const passwordResetAcceptedMessage = "If an account exists for that email, a verification code will be sent."

type passwordResetService interface {
	RequestPasswordReset(ctx context.Context, email string) error
	VerifyPasswordResetOTP(ctx context.Context, email, otp string) (passwordreset.ResetToken, error)
	ConfirmPasswordReset(ctx context.Context, rawToken, newPassword string) error
}

type PasswordResetHandler struct {
	service passwordResetService
	logger  *log.Logger
}

type RequestPasswordResetRequest struct {
	Email string `json:"email"`
}

type VerifyPasswordResetRequest struct {
	Email string `json:"email"`
	OTP   string `json:"otp"`
}

type VerifyPasswordResetResponse struct {
	ResetToken string    `json:"reset_token"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type ConfirmPasswordResetRequest struct {
	ResetToken  string `json:"reset_token"`
	NewPassword string `json:"new_password"`
}

func NewPasswordResetHandler(service passwordResetService, logger *log.Logger) *PasswordResetHandler {
	if logger == nil {
		logger = log.Default()
	}
	return &PasswordResetHandler{
		service: service,
		logger:  logger,
	}
}

func (h *PasswordResetHandler) Request(w http.ResponseWriter, r *http.Request) {
	var req RequestPasswordResetRequest
	if !decodeJSONRequest(w, r, &req, false) {
		return
	}

	req.Email = normalizeEmail(req.Email)
	if !isValidEmail(req.Email) {
		http.Error(w, "A valid email is required", http.StatusBadRequest)
		return
	}

	if err := h.service.RequestPasswordReset(r.Context(), req.Email); err != nil {
		// Do not change the response based on account existence, cooldown state,
		// database errors, or email-provider errors.
		h.logger.Printf("password reset request failed: %v", err)
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"message": passwordResetAcceptedMessage,
	})
}

func (h *PasswordResetHandler) Verify(w http.ResponseWriter, r *http.Request) {
	var req VerifyPasswordResetRequest
	if !decodeJSONRequest(w, r, &req, false) {
		return
	}

	req.Email = normalizeEmail(req.Email)
	req.OTP = strings.TrimSpace(req.OTP)
	if !isValidEmail(req.Email) || !isSixDigitOTP(req.OTP) {
		http.Error(w, "A valid email and six-digit code are required", http.StatusBadRequest)
		return
	}

	resetToken, err := h.service.VerifyPasswordResetOTP(r.Context(), req.Email, req.OTP)
	if errors.Is(err, passwordreset.ErrInvalidOrExpiredOTP) {
		http.Error(w, "Invalid or expired verification code", http.StatusBadRequest)
		return
	}
	if err != nil {
		h.logger.Printf("password reset verification failed: %v", err)
		http.Error(w, "Failed to verify password reset code", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, VerifyPasswordResetResponse{
		ResetToken: resetToken.Token,
		ExpiresAt:  resetToken.ExpiresAt,
	})
}

func (h *PasswordResetHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	var req ConfirmPasswordResetRequest
	if !decodeJSONRequest(w, r, &req, false) {
		return
	}

	req.ResetToken = strings.TrimSpace(req.ResetToken)
	if req.ResetToken == "" || req.NewPassword == "" {
		http.Error(w, "Reset token and new password are required", http.StatusBadRequest)
		return
	}

	err := h.service.ConfirmPasswordReset(r.Context(), req.ResetToken, req.NewPassword)
	switch {
	case errors.Is(err, passwordreset.ErrInvalidOrExpiredResetToken):
		http.Error(w, "Invalid or expired reset token", http.StatusBadRequest)
		return
	case errors.Is(err, passwordreset.ErrInvalidNewPassword):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		h.logger.Printf("password reset confirmation failed: %v", err)
		http.Error(w, "Failed to reset password", http.StatusInternalServerError)
		return
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func isValidEmail(email string) bool {
	if email == "" {
		return false
	}
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}

func isSixDigitOTP(otp string) bool {
	if len(otp) != 6 {
		return false
	}
	for _, character := range otp {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

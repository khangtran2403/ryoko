package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/passwordreset"
)

type fakePasswordResetService struct {
	requestCalled bool
	requestEmail  string
	requestErr    error

	verifyCalled bool
	verifyEmail  string
	verifyOTP    string
	verifyResult passwordreset.ResetToken
	verifyErr    error

	confirmCalled      bool
	confirmToken       string
	confirmNewPassword string
	confirmErr         error
}

func (f *fakePasswordResetService) RequestPasswordReset(_ context.Context, email string) error {
	f.requestCalled = true
	f.requestEmail = email
	return f.requestErr
}

func (f *fakePasswordResetService) VerifyPasswordResetOTP(
	_ context.Context,
	email string,
	otp string,
) (passwordreset.ResetToken, error) {
	f.verifyCalled = true
	f.verifyEmail = email
	f.verifyOTP = otp
	return f.verifyResult, f.verifyErr
}

func (f *fakePasswordResetService) ConfirmPasswordReset(
	_ context.Context,
	rawToken string,
	newPassword string,
) error {
	f.confirmCalled = true
	f.confirmToken = rawToken
	f.confirmNewPassword = newPassword
	return f.confirmErr
}

func TestPasswordResetRequestReturnsUniformAcceptedResponse(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
	}{
		{name: "success"},
		{name: "unknown account"},
		{name: "database failure", serviceErr: errors.New("database unavailable")},
		{name: "email failure", serviceErr: errors.New("email provider unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePasswordResetService{requestErr: tt.serviceErr}
			handler := newTestPasswordResetHandler(service)
			recorder := performPasswordResetRequest(
				handler.Request,
				`{"email":"  USER@Example.com "}`,
			)

			if recorder.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusAccepted, recorder.Body.String())
			}
			if !service.requestCalled || service.requestEmail != "user@example.com" {
				t.Errorf("request call = {called:%v email:%q}", service.requestCalled, service.requestEmail)
			}

			var response map[string]string
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response["message"] != passwordResetAcceptedMessage {
				t.Errorf("message = %q, want %q", response["message"], passwordResetAcceptedMessage)
			}
		})
	}
}

func TestPasswordResetRequestRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"email":`},
		{name: "missing email", body: `{}`},
		{name: "blank email", body: `{"email":"  "}`},
		{name: "invalid email", body: `{"email":"not-an-email"}`},
		{name: "display name", body: `{"email":"User <user@example.com>"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePasswordResetService{}
			handler := newTestPasswordResetHandler(service)
			recorder := performPasswordResetRequest(handler.Request, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if service.requestCalled {
				t.Fatal("service called for invalid request")
			}
		})
	}
}

func TestPasswordResetVerifyReturnsResetToken(t *testing.T) {
	expiresAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	service := &fakePasswordResetService{verifyResult: passwordreset.ResetToken{
		Token:     "reset-token",
		ExpiresAt: expiresAt,
	}}
	handler := newTestPasswordResetHandler(service)
	recorder := performPasswordResetRequest(
		handler.Verify,
		`{"email":" USER@example.com ","otp":" 012345 "}`,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if !service.verifyCalled || service.verifyEmail != "user@example.com" || service.verifyOTP != "012345" {
		t.Errorf("verify call = {called:%v email:%q otp:%q}", service.verifyCalled, service.verifyEmail, service.verifyOTP)
	}

	var response struct {
		ResetToken string    `json:"reset_token"`
		ExpiresAt  time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ResetToken != "reset-token" || !response.ExpiresAt.Equal(expiresAt) {
		t.Errorf("response = %+v", response)
	}
}

func TestPasswordResetVerifyRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"email":`},
		{name: "invalid email", body: `{"email":"invalid","otp":"123456"}`},
		{name: "missing OTP", body: `{"email":"user@example.com"}`},
		{name: "short OTP", body: `{"email":"user@example.com","otp":"12345"}`},
		{name: "non-digit OTP", body: `{"email":"user@example.com","otp":"12345a"}`},
		{name: "Unicode digits", body: `{"email":"user@example.com","otp":"１２３４５６"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePasswordResetService{}
			handler := newTestPasswordResetHandler(service)
			recorder := performPasswordResetRequest(handler.Verify, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if service.verifyCalled {
				t.Fatal("service called for invalid request")
			}
		})
	}
}

func TestPasswordResetVerifyMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "invalid or expired OTP",
			serviceErr: passwordreset.ErrInvalidOrExpiredOTP,
			wantStatus: http.StatusBadRequest,
			wantBody:   "Invalid or expired verification code\n",
		},
		{
			name:       "unexpected failure",
			serviceErr: errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantBody:   "Failed to verify password reset code\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePasswordResetService{verifyErr: tt.serviceErr}
			handler := newTestPasswordResetHandler(service)
			recorder := performPasswordResetRequest(
				handler.Verify,
				`{"email":"user@example.com","otp":"123456"}`,
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if recorder.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", recorder.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestPasswordResetConfirm(t *testing.T) {
	service := &fakePasswordResetService{}
	handler := newTestPasswordResetHandler(service)
	recorder := performPasswordResetRequest(
		handler.Confirm,
		`{"reset_token":" reset-token ","new_password":"a-valid-new-password"}`,
	)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body = %s", recorder.Code, http.StatusNoContent, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", recorder.Body.String())
	}
	if !service.confirmCalled || service.confirmToken != "reset-token" || service.confirmNewPassword != "a-valid-new-password" {
		t.Errorf(
			"confirm call = {called:%v token:%q password:%q}",
			service.confirmCalled,
			service.confirmToken,
			service.confirmNewPassword,
		)
	}
}

func TestPasswordResetConfirmRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"reset_token":`},
		{name: "missing token", body: `{"new_password":"a-valid-new-password"}`},
		{name: "blank token", body: `{"reset_token":"  ","new_password":"a-valid-new-password"}`},
		{name: "missing password", body: `{"reset_token":"reset-token"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePasswordResetService{}
			handler := newTestPasswordResetHandler(service)
			recorder := performPasswordResetRequest(handler.Confirm, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
			}
			if service.confirmCalled {
				t.Fatal("service called for invalid request")
			}
		})
	}
}

func TestPasswordResetConfirmMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "invalid or expired token",
			serviceErr: passwordreset.ErrInvalidOrExpiredResetToken,
			wantStatus: http.StatusBadRequest,
			wantBody:   "Invalid or expired reset token\n",
		},
		{
			name:       "invalid password",
			serviceErr: errors.Join(passwordreset.ErrInvalidNewPassword, errors.New("password is too short")),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unexpected failure",
			serviceErr: errors.New("database unavailable"),
			wantStatus: http.StatusInternalServerError,
			wantBody:   "Failed to reset password\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakePasswordResetService{confirmErr: tt.serviceErr}
			handler := newTestPasswordResetHandler(service)
			recorder := performPasswordResetRequest(
				handler.Confirm,
				`{"reset_token":"reset-token","new_password":"a-valid-new-password"}`,
			)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && recorder.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", recorder.Body.String(), tt.wantBody)
			}
		})
	}
}

func newTestPasswordResetHandler(service passwordResetService) *PasswordResetHandler {
	return NewPasswordResetHandler(service, log.New(io.Discard, "", 0))
}

func performPasswordResetRequest(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/auth/password-reset", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

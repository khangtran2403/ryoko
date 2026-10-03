package passwordreset

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

const testPepper = "test-password-reset-pepper-at-least-32-bytes-long"

type fakeEmailSender struct {
	emails []OTPEmail
	err    error
}

func (f *fakeEmailSender) SendPasswordResetOTP(_ context.Context, email OTPEmail) error {
	f.emails = append(f.emails, email)
	return f.err
}

func TestNewServiceValidatesDependenciesAndConfiguration(t *testing.T) {
	pool := &pgxpool.Pool{}
	queries := sqlc.New(pool)
	sender := &fakeEmailSender{}

	tests := []struct {
		name            string
		pool            *pgxpool.Pool
		queries         *sqlc.Queries
		sender          EmailSender
		pepper          string
		otpTTL          time.Duration
		resetTokenTTL   time.Duration
		requestCooldown time.Duration
	}{
		{
			name: "missing pool", queries: queries, sender: sender, pepper: testPepper,
			otpTTL: time.Minute, resetTokenTTL: time.Minute, requestCooldown: time.Second,
		},
		{
			name: "missing queries", pool: pool, sender: sender, pepper: testPepper,
			otpTTL: time.Minute, resetTokenTTL: time.Minute, requestCooldown: time.Second,
		},
		{
			name: "missing sender", pool: pool, queries: queries, pepper: testPepper,
			otpTTL: time.Minute, resetTokenTTL: time.Minute, requestCooldown: time.Second,
		},
		{
			name: "short pepper", pool: pool, queries: queries, sender: sender, pepper: "too-short",
			otpTTL: time.Minute, resetTokenTTL: time.Minute, requestCooldown: time.Second,
		},
		{
			name: "zero OTP TTL", pool: pool, queries: queries, sender: sender, pepper: testPepper,
			resetTokenTTL: time.Minute, requestCooldown: time.Second,
		},
		{
			name: "negative token TTL", pool: pool, queries: queries, sender: sender, pepper: testPepper,
			otpTTL: time.Minute, resetTokenTTL: -time.Minute, requestCooldown: time.Second,
		},
		{
			name: "zero cooldown", pool: pool, queries: queries, sender: sender, pepper: testPepper,
			otpTTL: time.Minute, resetTokenTTL: time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewService(
				tt.pool,
				tt.queries,
				tt.sender,
				tt.pepper,
				tt.otpTTL,
				tt.resetTokenTTL,
				tt.requestCooldown,
			)
			if err == nil {
				t.Fatal("NewService() returned nil error")
			}
			if service != nil {
				t.Errorf("NewService() service = %+v, want nil", service)
			}
		})
	}
}

func TestNewServiceAcceptsValidConfiguration(t *testing.T) {
	pool := &pgxpool.Pool{}
	service, err := NewService(
		pool,
		sqlc.New(pool),
		&fakeEmailSender{},
		testPepper,
		10*time.Minute,
		15*time.Minute,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if service == nil {
		t.Fatal("NewService() returned nil service")
	}
}

func TestRequestPasswordResetRejectsBlankEmailBeforeDatabaseAccess(t *testing.T) {
	pool := &pgxpool.Pool{}
	service, err := NewService(
		pool,
		sqlc.New(pool),
		&fakeEmailSender{},
		testPepper,
		10*time.Minute,
		15*time.Minute,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	for _, email := range []string{"", "   "} {
		if err := service.RequestPasswordReset(context.Background(), email); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("RequestPasswordReset(%q) error = %v, want ErrInvalidEmail", email, err)
		}
	}
}

func TestVerifyPasswordResetOTPRejectsBlankInputBeforeDatabaseAccess(t *testing.T) {
	pool := &pgxpool.Pool{}
	service, err := NewService(
		pool,
		sqlc.New(pool),
		&fakeEmailSender{},
		testPepper,
		10*time.Minute,
		15*time.Minute,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	tests := []struct {
		name  string
		email string
		otp   string
	}{
		{name: "blank email", email: "   ", otp: "123456"},
		{name: "blank OTP", email: "user@example.com", otp: "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := service.VerifyPasswordResetOTP(context.Background(), tt.email, tt.otp)
			if !errors.Is(err, ErrInvalidOrExpiredOTP) {
				t.Fatalf("VerifyPasswordResetOTP() error = %v, want ErrInvalidOrExpiredOTP", err)
			}
			if result != (ResetToken{}) {
				t.Errorf("VerifyPasswordResetOTP() result = %+v, want empty", result)
			}
		})
	}
}

func TestConfirmPasswordResetValidatesInputBeforeDatabaseAccess(t *testing.T) {
	pool := &pgxpool.Pool{}
	service, err := NewService(
		pool,
		sqlc.New(pool),
		&fakeEmailSender{},
		testPepper,
		10*time.Minute,
		15*time.Minute,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	tests := []struct {
		name        string
		token       string
		newPassword string
		wantErr     error
	}{
		{
			name:        "blank token",
			token:       "   ",
			newPassword: "a-valid-new-password",
			wantErr:     ErrInvalidOrExpiredResetToken,
		},
		{
			name:        "short password",
			token:       "reset-token",
			newPassword: "too-short",
			wantErr:     ErrInvalidNewPassword,
		},
		{
			name:        "password above bcrypt limit",
			token:       "reset-token",
			newPassword: string(make([]byte, 73)),
			wantErr:     ErrInvalidNewPassword,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := service.ConfirmPasswordReset(context.Background(), tt.token, tt.newPassword); !errors.Is(err, tt.wantErr) {
				t.Errorf("ConfirmPasswordReset() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

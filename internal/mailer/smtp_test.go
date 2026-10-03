package mailer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/passwordreset"
)

func TestNewSMTPSenderValidatesConfiguration(t *testing.T) {
	valid := SMTPConfig{
		Host:       "smtp.example.com",
		Port:       587,
		From:       "noreply@example.com",
		RequireTLS: true,
		Timeout:    10 * time.Second,
	}
	tests := []struct {
		name   string
		mutate func(*SMTPConfig)
	}{
		{name: "missing host", mutate: func(config *SMTPConfig) { config.Host = " " }},
		{name: "zero port", mutate: func(config *SMTPConfig) { config.Port = 0 }},
		{name: "port above range", mutate: func(config *SMTPConfig) { config.Port = 65536 }},
		{name: "invalid from", mutate: func(config *SMTPConfig) { config.From = "invalid" }},
		{name: "from header injection", mutate: func(config *SMTPConfig) { config.From = "a@example.com\r\nBcc: x@example.com" }},
		{name: "username without password", mutate: func(config *SMTPConfig) { config.Username = "user" }},
		{name: "password without username", mutate: func(config *SMTPConfig) { config.Password = "password" }},
		{name: "zero timeout", mutate: func(config *SMTPConfig) { config.Timeout = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := valid
			tt.mutate(&config)
			sender, err := NewSMTPSender(config)
			if err == nil {
				t.Fatal("NewSMTPSender() returned nil error")
			}
			if sender != nil {
				t.Errorf("NewSMTPSender() sender = %+v, want nil", sender)
			}
		})
	}
}

func TestNewSMTPSenderAcceptsAuthenticatedAndUnauthenticatedConfiguration(t *testing.T) {
	for _, config := range []SMTPConfig{
		{
			Host: "localhost", Port: 1025, From: "noreply@example.com",
			Timeout: time.Second,
		},
		{
			Host: "smtp.example.com", Port: 587, Username: "user", Password: "password",
			From: "noreply@example.com", RequireTLS: true, Timeout: time.Second,
		},
	} {
		if _, err := NewSMTPSender(config); err != nil {
			t.Errorf("NewSMTPSender(%+v) error = %v", config, err)
		}
	}
}

func TestBuildPasswordResetOTPMessage(t *testing.T) {
	expiresAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	message := string(buildPasswordResetOTPMessage(
		"noreply@example.com",
		passwordreset.OTPEmail{
			To:        "user@example.com",
			OTP:       "012345",
			ExpiresAt: expiresAt,
		},
	))

	for _, expected := range []string{
		"From: noreply@example.com\r\n",
		"To: user@example.com\r\n",
		"Subject: Your Ryoko password reset code\r\n",
		"Content-Type: text/plain; charset=UTF-8\r\n",
		"\r\n\r\nYour Ryoko password reset verification code is: 012345",
		expiresAt.Format(time.RFC1123),
	} {
		if !strings.Contains(message, expected) {
			t.Errorf("message does not contain %q:\n%s", expected, message)
		}
	}
}

func TestSendPasswordResetOTPValidatesMessageBeforeConnecting(t *testing.T) {
	sender, err := NewSMTPSender(SMTPConfig{
		Host: "smtp.invalid", Port: 587, From: "noreply@example.com",
		RequireTLS: true, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender() error = %v", err)
	}

	tests := []struct {
		name  string
		email passwordreset.OTPEmail
	}{
		{name: "invalid recipient", email: passwordreset.OTPEmail{To: "invalid", OTP: "123456", ExpiresAt: time.Now()}},
		{name: "recipient injection", email: passwordreset.OTPEmail{To: "a@example.com\r\nBcc: x@example.com", OTP: "123456", ExpiresAt: time.Now()}},
		{name: "invalid OTP", email: passwordreset.OTPEmail{To: "user@example.com", OTP: "12345a", ExpiresAt: time.Now()}},
		{name: "missing expiry", email: passwordreset.OTPEmail{To: "user@example.com", OTP: "123456"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := sender.SendPasswordResetOTP(context.Background(), tt.email); err == nil {
				t.Fatal("SendPasswordResetOTP() returned nil error")
			}
		})
	}
}

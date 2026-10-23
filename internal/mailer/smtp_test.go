package mailer

import (
	"context"
	"strings"
	"testing"
	"time"

	notification "github.com/khangtran2403/ryoko/internal/notifications"
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

func TestBuildBookingEmailMessages(t *testing.T) {
	tests := []struct {
		eventType   string
		wantSubject string
		wantMessage string
	}{
		{
			eventType:   notification.BookingConfirmedEvent,
			wantSubject: "Your Ryoko booking is confirmed",
			wantMessage: "Your booking has been confirmed.",
		},
		{
			eventType:   notification.BookingCancelledEvent,
			wantSubject: "Your Ryoko booking was cancelled",
			wantMessage: "Your booking has been cancelled.",
		},
		{
			eventType:   notification.PaymentRefundedEvent,
			wantSubject: "Your Ryoko refund is complete",
			wantMessage: "Your refund has been completed.",
		},
	}

	for _, test := range tests {
		t.Run(test.eventType, func(t *testing.T) {
			email := validBookingEmail()
			email.EventType = test.eventType
			subject, body, err := bookingEmailContent(email)
			if err != nil {
				t.Fatalf("bookingEmailContent() error = %v", err)
			}
			if subject != test.wantSubject {
				t.Errorf("subject = %q, want %q", subject, test.wantSubject)
			}

			message := string(buildTextMessage(
				"noreply@example.com",
				email.To,
				subject,
				body,
			))
			for _, expected := range []string{
				"From: noreply@example.com\r\n",
				"To: guest@example.com\r\n",
				"Subject: " + test.wantSubject + "\r\n",
				"Hello Khang Tran,",
				test.wantMessage,
				"Booking ID: 42",
				"Hotel: Ryoko Hotel",
				"Room type: Deluxe",
				"Check-in: 2026-11-01",
				"Check-out: 2026-11-03",
				"Rooms: 1",
				"Guests: 2",
				"Price per night: 1500000.00 VND",
				"Total: 3000000.00 VND",
			} {
				if !strings.Contains(message, expected) {
					t.Errorf("message does not contain %q:\n%s", expected, message)
				}
			}
		})
	}
}

func TestSendBookingEmailValidatesBeforeConnecting(t *testing.T) {
	sender, err := NewSMTPSender(SMTPConfig{
		Host: "smtp.invalid", Port: 587, From: "noreply@example.com",
		RequireTLS: true, Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewSMTPSender() error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*notification.BookingEmail)
		wantErr string
	}{
		{
			name:    "invalid recipient",
			mutate:  func(email *notification.BookingEmail) { email.To = "invalid" },
			wantErr: "invalid booking email recipient",
		},
		{
			name: "recipient header injection",
			mutate: func(email *notification.BookingEmail) {
				email.To = "guest@example.com\r\nBcc: attacker@example.com"
			},
			wantErr: "invalid booking email recipient",
		},
		{
			name:    "unsupported event",
			mutate:  func(email *notification.BookingEmail) { email.EventType = "unknown" },
			wantErr: "unsupported booking email event type",
		},
		{
			name:    "invalid booking ID",
			mutate:  func(email *notification.BookingEmail) { email.Payload.BookingID = 0 },
			wantErr: "booking ID must be positive",
		},
		{
			name:    "missing guest name",
			mutate:  func(email *notification.BookingEmail) { email.Payload.GuestName = " " },
			wantErr: "guest name is required",
		},
		{
			name:    "invalid check-in",
			mutate:  func(email *notification.BookingEmail) { email.Payload.CheckIn = "November 1" },
			wantErr: "check-in must use YYYY-MM-DD",
		},
		{
			name:    "check-out before check-in",
			mutate:  func(email *notification.BookingEmail) { email.Payload.CheckOut = "2026-10-31" },
			wantErr: "check-out must use YYYY-MM-DD and follow check-in",
		},
		{
			name:    "invalid rooms",
			mutate:  func(email *notification.BookingEmail) { email.Payload.RoomsCount = 0 },
			wantErr: "rooms count must be positive",
		},
		{
			name:    "unsupported currency",
			mutate:  func(email *notification.BookingEmail) { email.Payload.Currency = "EUR" },
			wantErr: "currency must be USD or VND",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			email := validBookingEmail()
			test.mutate(&email)
			err := sender.SendBookingEmail(context.Background(), email)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("SendBookingEmail() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func validBookingEmail() notification.BookingEmail {
	return notification.BookingEmail{
		To:        "guest@example.com",
		EventType: notification.BookingConfirmedEvent,
		Payload: notification.BookingEmailPayload{
			BookingID:     42,
			GuestName:     "Khang Tran",
			HotelName:     "Ryoko Hotel",
			RoomTypeName:  "Deluxe",
			CheckIn:       "2026-11-01",
			CheckOut:      "2026-11-03",
			RoomsCount:    1,
			GuestCount:    2,
			PricePerNight: "1500000.00",
			TotalPrice:    "3000000.00",
			Currency:      "VND",
		},
	}
}

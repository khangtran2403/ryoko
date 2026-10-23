package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	notification "github.com/khangtran2403/ryoko/internal/notifications"
	"github.com/khangtran2403/ryoko/internal/passwordreset"
)

type SMTPConfig struct {
	Host       string
	Port       int
	Username   string
	Password   string
	From       string
	RequireTLS bool
	Timeout    time.Duration
}

type SMTPSender struct {
	host       string
	address    string
	username   string
	password   string
	from       string
	requireTLS bool
	timeout    time.Duration
}

var _ notification.EmailSender = (*SMTPSender)(nil)

func NewSMTPSender(config SMTPConfig) (*SMTPSender, error) {
	config.Host = strings.TrimSpace(config.Host)
	config.From = strings.TrimSpace(config.From)
	if config.Host == "" {
		return nil, errors.New("SMTP host is required")
	}
	if config.Port < 1 || config.Port > 65535 {
		return nil, errors.New("SMTP port must be from 1 to 65535")
	}
	if err := validateMailbox(config.From); err != nil {
		return nil, fmt.Errorf("invalid SMTP from address: %w", err)
	}
	if (config.Username == "") != (config.Password == "") {
		return nil, errors.New("SMTP username and password must either both be set or both be empty")
	}
	if config.Timeout <= 0 {
		return nil, errors.New("SMTP timeout must be positive")
	}

	return &SMTPSender{
		host:       config.Host,
		address:    net.JoinHostPort(config.Host, strconv.Itoa(config.Port)),
		username:   config.Username,
		password:   config.Password,
		from:       config.From,
		requireTLS: config.RequireTLS,
		timeout:    config.Timeout,
	}, nil
}

func (s *SMTPSender) SendPasswordResetOTP(ctx context.Context, email passwordreset.OTPEmail) error {
	email.To = strings.TrimSpace(email.To)
	if err := validateMailbox(email.To); err != nil {
		return fmt.Errorf("invalid password reset recipient: %w", err)
	}
	if !isSixDigitOTP(email.OTP) {
		return errors.New("password reset OTP must contain exactly six digits")
	}
	if email.ExpiresAt.IsZero() {
		return errors.New("password reset OTP expiry is required")
	}
	body := fmt.Sprintf(
		"Your Ryoko password reset verification code is: %s\r\n\r\n"+
			"This code expires at %s.\r\n\r\n"+
			"If you did not request a password reset, you can ignore this email.\r\n",
		email.OTP,
		email.ExpiresAt.UTC().Format(time.RFC1123),
	)
	return s.sendTextEmail(
		ctx,
		email.To,
		"Your Ryoko password reset code",
		body,
	)
}

func (s *SMTPSender) SendBookingEmail(
	ctx context.Context,
	email notification.BookingEmail,
) error {
	email.To = strings.TrimSpace(email.To)
	if err := validateMailbox(email.To); err != nil {
		return fmt.Errorf("invalid booking email recipient: %w", err)
	}
	subject, body, err := bookingEmailContent(email)
	if err != nil {
		return err
	}
	return s.sendTextEmail(ctx, email.To, subject, body)
}

func (s *SMTPSender) sendTextEmail(
	ctx context.Context,
	to string,
	subject string,
	body string,
) error {

	dialer := net.Dialer{Timeout: s.timeout}
	connection, err := dialer.DialContext(ctx, "tcp", s.address)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer connection.Close()

	deadline := time.Now().Add(s.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set SMTP connection deadline: %w", err)
	}

	client, err := smtp.NewClient(connection, s.host)
	if err != nil {
		return fmt.Errorf("create SMTP client: %w", err)
	}
	defer client.Close()

	supportsSTARTTLS, _ := client.Extension("STARTTLS")
	if supportsSTARTTLS {
		if err := client.StartTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: s.host,
		}); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	} else if s.requireTLS {
		return errors.New("SMTP server does not support required STARTTLS")
	}

	if s.username != "" {
		auth := smtp.PlainAuth("", s.username, s.password, s.host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("authenticate with SMTP server: %w", err)
		}
	}
	if err := client.Mail(s.from); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}

	messageWriter, err := client.Data()
	if err != nil {
		return fmt.Errorf("start SMTP message: %w", err)
	}
	message := buildTextMessage(s.from, to, subject, body)
	if _, err := messageWriter.Write(message); err != nil {
		_ = messageWriter.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := messageWriter.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("close SMTP session: %w", err)
	}

	return nil
}

func buildPasswordResetOTPMessage(from string, email passwordreset.OTPEmail) []byte {
	body := fmt.Sprintf(
		"Your Ryoko password reset verification code is: %s\r\n\r\n"+
			"This code expires at %s.\r\n\r\n"+
			"If you did not request a password reset, you can ignore this email.\r\n",
		email.OTP,
		email.ExpiresAt.UTC().Format(time.RFC1123),
	)
	return buildTextMessage(
		from,
		email.To,
		"Your Ryoko password reset code",
		body,
	)
}

func buildTextMessage(from, to, subject, body string) []byte {
	message := "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" + body
	return []byte(message)
}

func bookingEmailContent(email notification.BookingEmail) (string, string, error) {
	payload := email.Payload
	if err := validateBookingEmailPayload(payload); err != nil {
		return "", "", err
	}

	details := fmt.Sprintf(
		"Booking ID: %d\r\n"+
			"Hotel: %s\r\n"+
			"Room type: %s\r\n"+
			"Check-in: %s\r\n"+
			"Check-out: %s\r\n"+
			"Rooms: %d\r\n"+
			"Guests: %d\r\n"+
			"Price per night: %s %s\r\n"+
			"Total: %s %s\r\n",
		payload.BookingID,
		payload.HotelName,
		payload.RoomTypeName,
		payload.CheckIn,
		payload.CheckOut,
		payload.RoomsCount,
		payload.GuestCount,
		payload.PricePerNight,
		payload.Currency,
		payload.TotalPrice,
		payload.Currency,
	)

	var subject, introduction, closing string
	switch email.EventType {
	case notification.BookingConfirmedEvent:
		subject = "Your Ryoko booking is confirmed"
		introduction = "Your booking has been confirmed."
		closing = "We look forward to your stay."
	case notification.BookingCancelledEvent:
		subject = "Your Ryoko booking was cancelled"
		introduction = "Your booking has been cancelled."
		closing = "If you did not request this cancellation, please contact support."
	case notification.PaymentRefundedEvent:
		subject = "Your Ryoko refund is complete"
		introduction = "Your refund has been completed."
		closing = "The time required for the funds to appear depends on your payment provider."
	default:
		return "", "", fmt.Errorf("unsupported booking email event type %q", email.EventType)
	}

	body := fmt.Sprintf(
		"Hello %s,\r\n\r\n%s\r\n\r\n%s\r\n%s\r\n",
		payload.GuestName,
		introduction,
		details,
		closing,
	)
	return subject, body, nil
}

func validateBookingEmailPayload(payload notification.BookingEmailPayload) error {
	if payload.BookingID <= 0 {
		return errors.New("booking email booking ID must be positive")
	}
	if strings.TrimSpace(payload.GuestName) == "" {
		return errors.New("booking email guest name is required")
	}
	if strings.TrimSpace(payload.HotelName) == "" {
		return errors.New("booking email hotel name is required")
	}
	if strings.TrimSpace(payload.RoomTypeName) == "" {
		return errors.New("booking email room type name is required")
	}
	checkIn, err := time.Parse(time.DateOnly, payload.CheckIn)
	if err != nil {
		return errors.New("booking email check-in must use YYYY-MM-DD")
	}
	checkOut, err := time.Parse(time.DateOnly, payload.CheckOut)
	if err != nil || !checkOut.After(checkIn) {
		return errors.New("booking email check-out must use YYYY-MM-DD and follow check-in")
	}
	if payload.RoomsCount <= 0 {
		return errors.New("booking email rooms count must be positive")
	}
	if payload.GuestCount <= 0 {
		return errors.New("booking email guest count must be positive")
	}
	if strings.TrimSpace(payload.PricePerNight) == "" {
		return errors.New("booking email price per night is required")
	}
	if strings.TrimSpace(payload.TotalPrice) == "" {
		return errors.New("booking email total price is required")
	}
	if payload.Currency != "USD" && payload.Currency != "VND" {
		return errors.New("booking email currency must be USD or VND")
	}
	return nil
}

func validateMailbox(value string) error {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return errors.New("email address is required and must not contain line breaks")
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return errors.New("email address must be a plain valid address")
	}
	return nil
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

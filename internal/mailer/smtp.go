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
	if err := client.Rcpt(email.To); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}

	messageWriter, err := client.Data()
	if err != nil {
		return fmt.Errorf("start SMTP message: %w", err)
	}
	message := buildPasswordResetOTPMessage(s.from, email)
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
	message := "From: " + from + "\r\n" +
		"To: " + email.To + "\r\n" +
		"Subject: Your Ryoko password reset code\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" + body
	return []byte(message)
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

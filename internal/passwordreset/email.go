package passwordreset

import (
	"context"
	"time"
)

type OTPEmail struct {
	To        string
	OTP       string
	ExpiresAt time.Time
}

// EmailSender is defined by the password-reset package so the service does not
// depend on a particular SMTP or transactional-email provider.
type EmailSender interface {
	SendPasswordResetOTP(ctx context.Context, email OTPEmail) error
}

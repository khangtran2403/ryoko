package passwordreset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

var ErrInvalidEmail = errors.New("email must not be empty")

var ErrInvalidOrExpiredOTP = errors.New("invalid or expired password reset code")

var (
	ErrInvalidOrExpiredResetToken = errors.New("invalid or expired password reset token")
	ErrInvalidNewPassword         = errors.New("invalid new password")
)

const maxOTPAttempts int16 = 5

type ResetToken struct {
	Token     string
	ExpiresAt time.Time
}

type Service struct {
	pool            *pgxpool.Pool
	queries         *sqlc.Queries
	emailSender     EmailSender
	pepper          string
	otpTTL          time.Duration
	resetTokenTTL   time.Duration
	requestCooldown time.Duration
	now             func() time.Time
}

func NewService(
	pool *pgxpool.Pool,
	queries *sqlc.Queries,
	emailSender EmailSender,
	pepper string,
	otpTTL time.Duration,
	resetTokenTTL time.Duration,
	requestCooldown time.Duration,
) (*Service, error) {
	if pool == nil {
		return nil, errors.New("password reset database pool is required")
	}
	if queries == nil {
		return nil, errors.New("password reset queries are required")
	}
	if emailSender == nil {
		return nil, errors.New("password reset email sender is required")
	}
	if len([]byte(pepper)) < 32 {
		return nil, errors.New("password reset pepper must contain at least 32 bytes")
	}
	if otpTTL <= 0 {
		return nil, errors.New("password reset OTP TTL must be positive")
	}
	if resetTokenTTL <= 0 {
		return nil, errors.New("password reset token TTL must be positive")
	}
	if requestCooldown <= 0 {
		return nil, errors.New("password reset request cooldown must be positive")
	}

	return &Service{
		pool:            pool,
		queries:         queries,
		emailSender:     emailSender,
		pepper:          pepper,
		otpTTL:          otpTTL,
		resetTokenTTL:   resetTokenTTL,
		requestCooldown: requestCooldown,
		now:             time.Now,
	}, nil
}

// RequestPasswordReset creates a new OTP challenge for an existing user.
// Unknown emails and requests made during the cooldown both return nil so the
// public endpoint can give the same response without revealing account state.
func (s *Service) RequestPasswordReset(ctx context.Context, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return ErrInvalidEmail
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin password reset request: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	user, err := qtx.LockUserForPasswordResetByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock password reset user: %w", err)
	}

	now := s.now().UTC()
	latest, err := qtx.GetLatestPasswordResetRequest(ctx, user.ID)
	if err == nil && latest.CreatedAt.Valid && now.Before(latest.CreatedAt.Time.Add(s.requestCooldown)) {
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("get latest password reset request: %w", err)
	}

	otp, err := auth.GeneratePasswordResetOTP()
	if err != nil {
		return fmt.Errorf("generate password reset OTP: %w", err)
	}
	otpHash := auth.HashPasswordResetOTP(otp, s.pepper)

	if _, err := qtx.ConsumeActivePasswordResetRequests(ctx, sqlc.ConsumeActivePasswordResetRequestsParams{
		ConsumedAt: timestamptz(now),
		UserID:     user.ID,
	}); err != nil {
		return fmt.Errorf("consume active password reset requests: %w", err)
	}

	expiresAt := now.Add(s.otpTTL)
	if _, err := qtx.CreatePasswordResetRequest(ctx, sqlc.CreatePasswordResetRequestParams{
		UserID:    user.ID,
		OtpHash:   otpHash[:],
		ExpiresAt: timestamptz(expiresAt),
	}); err != nil {
		return fmt.Errorf("create password reset request: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password reset request: %w", err)
	}

	if err := s.emailSender.SendPasswordResetOTP(ctx, OTPEmail{
		To:        user.Email,
		OTP:       otp,
		ExpiresAt: expiresAt,
	}); err != nil {
		return fmt.Errorf("send password reset OTP: %w", err)
	}

	return nil
}

// VerifyPasswordResetOTP exchanges a valid OTP for a short-lived opaque reset
// token. Every user-controlled invalid state maps to the same error so callers
// do not reveal whether an account or active reset request exists.
func (s *Service) VerifyPasswordResetOTP(ctx context.Context, email, otp string) (ResetToken, error) {
	email = strings.TrimSpace(email)
	otp = strings.TrimSpace(otp)
	if email == "" || otp == "" {
		return ResetToken{}, ErrInvalidOrExpiredOTP
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ResetToken{}, fmt.Errorf("begin password reset verification: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	user, err := qtx.LockUserForPasswordResetByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResetToken{}, ErrInvalidOrExpiredOTP
	}
	if err != nil {
		return ResetToken{}, fmt.Errorf("lock password reset user: %w", err)
	}

	request, err := qtx.GetActivePasswordResetRequestForUpdate(ctx, user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResetToken{}, ErrInvalidOrExpiredOTP
	}
	if err != nil {
		return ResetToken{}, fmt.Errorf("get active password reset request: %w", err)
	}

	now := s.now().UTC()
	if request.Attempts >= maxOTPAttempts || !request.ExpiresAt.Valid || !now.Before(request.ExpiresAt.Time) {
		return ResetToken{}, ErrInvalidOrExpiredOTP
	}

	if !auth.PasswordResetOTPMatches(otp, s.pepper, request.OtpHash) {
		if _, err := qtx.IncrementPasswordResetAttempts(ctx, request.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ResetToken{}, ErrInvalidOrExpiredOTP
			}
			return ResetToken{}, fmt.Errorf("increment password reset attempts: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return ResetToken{}, fmt.Errorf("commit password reset attempt: %w", err)
		}
		return ResetToken{}, ErrInvalidOrExpiredOTP
	}

	rawToken, err := auth.GeneratePasswordResetToken()
	if err != nil {
		return ResetToken{}, fmt.Errorf("generate password reset token: %w", err)
	}
	tokenHash := auth.HashPasswordResetToken(rawToken)
	tokenExpiresAt := now.Add(s.resetTokenTTL)

	if _, err := qtx.MarkPasswordResetVerified(ctx, sqlc.MarkPasswordResetVerifiedParams{
		VerifiedAt:          timestamptz(now),
		ResetTokenHash:      tokenHash[:],
		ResetTokenExpiresAt: timestamptz(tokenExpiresAt),
		ID:                  request.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ResetToken{}, ErrInvalidOrExpiredOTP
		}
		return ResetToken{}, fmt.Errorf("mark password reset request verified: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ResetToken{}, fmt.Errorf("commit password reset verification: %w", err)
	}

	return ResetToken{
		Token:     rawToken,
		ExpiresAt: tokenExpiresAt,
	}, nil
}

// ConfirmPasswordReset consumes a verified reset token, changes the password,
// and revokes the user's active refresh sessions in one transaction.
func (s *Service) ConfirmPasswordReset(ctx context.Context, rawToken, newPassword string) error {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return ErrInvalidOrExpiredResetToken
	}
	if err := auth.ValidatePassword(newPassword); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidNewPassword, err)
	}

	tokenHash := auth.HashPasswordResetToken(rawToken)
	initialRequest, err := s.queries.GetPasswordResetRequestByTokenHash(ctx, tokenHash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidOrExpiredResetToken
	}
	if err != nil {
		return fmt.Errorf("find password reset request: %w", err)
	}

	passwordHash, err := auth.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash new password: %w", err)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin password reset confirmation: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	user, err := qtx.LockUserForPasswordResetByID(ctx, initialRequest.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidOrExpiredResetToken
	}
	if err != nil {
		return fmt.Errorf("lock password reset user: %w", err)
	}

	request, err := qtx.GetPasswordResetRequestForUpdate(ctx, initialRequest.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInvalidOrExpiredResetToken
	}
	if err != nil {
		return fmt.Errorf("lock password reset request: %w", err)
	}

	now := s.now().UTC()
	if request.UserID != user.ID ||
		!request.VerifiedAt.Valid ||
		request.ConsumedAt.Valid ||
		!request.ResetTokenExpiresAt.Valid ||
		!now.Before(request.ResetTokenExpiresAt.Time) ||
		!auth.PasswordResetTokenMatches(rawToken, request.ResetTokenHash) {
		return ErrInvalidOrExpiredResetToken
	}

	if _, err := qtx.UpdateUserPasswordAfterReset(ctx, sqlc.UpdateUserPasswordAfterResetParams{
		PasswordHash: passwordHash,
		UpdatedAt:    timestamptz(now),
		UserID:       user.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidOrExpiredResetToken
		}
		return fmt.Errorf("update password after reset: %w", err)
	}

	if _, err := qtx.ConsumePasswordResetRequest(ctx, sqlc.ConsumePasswordResetRequestParams{
		ConsumedAt: timestamptz(now),
		ID:         request.ID,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidOrExpiredResetToken
		}
		return fmt.Errorf("consume password reset request: %w", err)
	}

	if _, err := qtx.RevokeAllUserRefreshTokens(ctx, user.ID); err != nil {
		return fmt.Errorf("revoke refresh sessions after password reset: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit password reset confirmation: %w", err)
	}

	return nil
}

func (s *Service) DeleteExpiredPasswordResetRequests(ctx context.Context) (int64, error) {
	deleted, err := s.queries.DeleteExpiredPasswordResetRequests(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired password reset requests: %w", err)
	}
	return deleted, nil
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{
		Time:  value,
		Valid: true,
	}
}

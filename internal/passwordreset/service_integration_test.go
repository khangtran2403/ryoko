package passwordreset

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
)

func TestRequestPasswordResetCreatesChallengeAndEnforcesCooldown(t *testing.T) {
	pool, service, sender := newPasswordResetIntegrationService(t)
	userID := insertPasswordResetUser(t, pool, "Reset.User@example.com")
	fixedNow := time.Now().UTC().Truncate(time.Second)
	service.now = func() time.Time { return fixedNow }

	if err := service.RequestPasswordReset(context.Background(), "  reset.user@EXAMPLE.com  "); err != nil {
		t.Fatalf("RequestPasswordReset() error = %v", err)
	}
	if len(sender.emails) != 1 {
		t.Fatalf("sent emails = %d, want 1", len(sender.emails))
	}
	sent := sender.emails[0]
	if sent.To != "Reset.User@example.com" {
		t.Errorf("email recipient = %q, want stored user email", sent.To)
	}
	if !sent.ExpiresAt.Equal(fixedNow.Add(10 * time.Minute)) {
		t.Errorf("email expiry = %v, want %v", sent.ExpiresAt, fixedNow.Add(10*time.Minute))
	}

	var (
		requestID int64
		otpHash   []byte
		expiresAt time.Time
		attempts  int16
	)
	if err := pool.QueryRow(
		context.Background(),
		`SELECT id, otp_hash, expires_at, attempts
		 FROM password_reset_requests
		 WHERE user_id = $1 AND consumed_at IS NULL`,
		userID,
	).Scan(&requestID, &otpHash, &expiresAt, &attempts); err != nil {
		t.Fatalf("read password reset request: %v", err)
	}
	if requestID <= 0 {
		t.Errorf("request ID = %d, want positive", requestID)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0", attempts)
	}
	if !expiresAt.Equal(fixedNow.Add(10 * time.Minute)) {
		t.Errorf("stored expiry = %v, want %v", expiresAt, fixedNow.Add(10*time.Minute))
	}
	if !auth.PasswordResetOTPMatches(sent.OTP, testPepper, otpHash) {
		t.Error("emailed OTP does not match stored hash")
	}

	if err := service.RequestPasswordReset(context.Background(), "reset.user@example.com"); err != nil {
		t.Fatalf("cooldown RequestPasswordReset() error = %v", err)
	}
	if len(sender.emails) != 1 {
		t.Errorf("emails during cooldown = %d, want 1 total", len(sender.emails))
	}
	if countPasswordResetRequests(t, pool, userID) != 1 {
		t.Error("cooldown request created another database row")
	}

	service.now = func() time.Time { return fixedNow.Add(61 * time.Second) }
	if err := service.RequestPasswordReset(context.Background(), "reset.user@example.com"); err != nil {
		t.Fatalf("replacement RequestPasswordReset() error = %v", err)
	}
	if len(sender.emails) != 2 {
		t.Fatalf("emails after cooldown = %d, want 2", len(sender.emails))
	}
	if sender.emails[1].OTP == sent.OTP {
		t.Error("replacement request reused the previous OTP")
	}
	if countPasswordResetRequests(t, pool, userID) != 2 {
		t.Error("replacement request did not preserve the previous row for audit")
	}

	var activeCount int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM password_reset_requests WHERE user_id = $1 AND consumed_at IS NULL",
		userID,
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active password reset requests: %v", err)
	}
	if activeCount != 1 {
		t.Errorf("active requests = %d, want 1", activeCount)
	}
}

func TestRequestPasswordResetHidesUnknownEmail(t *testing.T) {
	pool, service, sender := newPasswordResetIntegrationService(t)

	if err := service.RequestPasswordReset(context.Background(), "unknown@example.com"); err != nil {
		t.Fatalf("RequestPasswordReset() error = %v", err)
	}
	if len(sender.emails) != 0 {
		t.Errorf("emails sent = %d, want 0", len(sender.emails))
	}

	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM password_reset_requests").Scan(&count); err != nil {
		t.Fatalf("count password reset requests: %v", err)
	}
	if count != 0 {
		t.Errorf("stored requests = %d, want 0", count)
	}
}

func TestRequestPasswordResetPersistsChallengeBeforeEmailFailure(t *testing.T) {
	pool, service, sender := newPasswordResetIntegrationService(t)
	userID := insertPasswordResetUser(t, pool, "delivery-failure@example.com")
	sender.err = errors.New("email provider unavailable")

	err := service.RequestPasswordReset(context.Background(), "delivery-failure@example.com")
	if err == nil || !errors.Is(err, sender.err) {
		t.Fatalf("RequestPasswordReset() error = %v, want sender error", err)
	}
	if countPasswordResetRequests(t, pool, userID) != 1 {
		t.Error("email failure rolled back the committed password reset request")
	}
}

func TestVerifyPasswordResetOTPReturnsStoredOpaqueToken(t *testing.T) {
	pool, service, sender := newPasswordResetIntegrationService(t)
	userID := insertPasswordResetUser(t, pool, "verify-reset@example.com")
	fixedNow := time.Now().UTC().Truncate(time.Second)
	service.now = func() time.Time { return fixedNow }

	if err := service.RequestPasswordReset(context.Background(), "verify-reset@example.com"); err != nil {
		t.Fatalf("RequestPasswordReset() error = %v", err)
	}
	result, err := service.VerifyPasswordResetOTP(
		context.Background(),
		"  VERIFY-RESET@example.com ",
		" "+sender.emails[0].OTP+" ",
	)
	if err != nil {
		t.Fatalf("VerifyPasswordResetOTP() error = %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(result.Token)
	if err != nil {
		t.Fatalf("reset token is not raw URL-safe base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded reset token length = %d, want 32", len(decoded))
	}
	wantExpiry := fixedNow.Add(15 * time.Minute)
	if !result.ExpiresAt.Equal(wantExpiry) {
		t.Errorf("reset token expiry = %v, want %v", result.ExpiresAt, wantExpiry)
	}

	tokenHash := auth.HashPasswordResetToken(result.Token)
	var (
		storedHash      []byte
		verifiedAt      time.Time
		storedExpiresAt time.Time
		attempts        int16
	)
	if err := pool.QueryRow(
		context.Background(),
		`SELECT reset_token_hash, verified_at, reset_token_expires_at, attempts
		 FROM password_reset_requests
		 WHERE user_id = $1 AND consumed_at IS NULL`,
		userID,
	).Scan(&storedHash, &verifiedAt, &storedExpiresAt, &attempts); err != nil {
		t.Fatalf("read verified password reset request: %v", err)
	}
	if string(storedHash) != string(tokenHash[:]) {
		t.Error("stored reset token hash does not match returned token")
	}
	if !verifiedAt.Equal(fixedNow) {
		t.Errorf("verified at = %v, want %v", verifiedAt, fixedNow)
	}
	if !storedExpiresAt.Equal(wantExpiry) {
		t.Errorf("stored token expiry = %v, want %v", storedExpiresAt, wantExpiry)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0", attempts)
	}

	second, err := service.VerifyPasswordResetOTP(context.Background(), "verify-reset@example.com", sender.emails[0].OTP)
	if !errors.Is(err, ErrInvalidOrExpiredOTP) {
		t.Fatalf("second verification error = %v, want ErrInvalidOrExpiredOTP", err)
	}
	if second != (ResetToken{}) {
		t.Errorf("second verification result = %+v, want empty", second)
	}
}

func TestVerifyPasswordResetOTPCountsAttemptsAndLocksAfterFive(t *testing.T) {
	pool, service, sender := newPasswordResetIntegrationService(t)
	userID := insertPasswordResetUser(t, pool, "attempts@example.com")
	if err := service.RequestPasswordReset(context.Background(), "attempts@example.com"); err != nil {
		t.Fatalf("RequestPasswordReset() error = %v", err)
	}

	for attempt := 1; attempt <= int(maxOTPAttempts); attempt++ {
		result, err := service.VerifyPasswordResetOTP(context.Background(), "attempts@example.com", "not-the-code")
		if !errors.Is(err, ErrInvalidOrExpiredOTP) {
			t.Fatalf("attempt %d error = %v, want ErrInvalidOrExpiredOTP", attempt, err)
		}
		if result != (ResetToken{}) {
			t.Errorf("attempt %d result = %+v, want empty", attempt, result)
		}
	}

	var attempts int16
	if err := pool.QueryRow(
		context.Background(),
		"SELECT attempts FROM password_reset_requests WHERE user_id = $1 AND consumed_at IS NULL",
		userID,
	).Scan(&attempts); err != nil {
		t.Fatalf("read password reset attempts: %v", err)
	}
	if attempts != maxOTPAttempts {
		t.Errorf("attempts = %d, want %d", attempts, maxOTPAttempts)
	}

	result, err := service.VerifyPasswordResetOTP(context.Background(), "attempts@example.com", sender.emails[0].OTP)
	if !errors.Is(err, ErrInvalidOrExpiredOTP) {
		t.Fatalf("correct OTP after exhaustion error = %v, want ErrInvalidOrExpiredOTP", err)
	}
	if result != (ResetToken{}) {
		t.Errorf("correct OTP after exhaustion result = %+v, want empty", result)
	}
}

func TestVerifyPasswordResetOTPRejectsExpiredAndUnknownRequests(t *testing.T) {
	_, service, sender := newPasswordResetIntegrationService(t)
	fixedNow := time.Now().UTC().Truncate(time.Second)
	service.now = func() time.Time { return fixedNow }

	result, err := service.VerifyPasswordResetOTP(context.Background(), "unknown@example.com", "123456")
	if !errors.Is(err, ErrInvalidOrExpiredOTP) {
		t.Fatalf("unknown email error = %v, want ErrInvalidOrExpiredOTP", err)
	}
	if result != (ResetToken{}) {
		t.Errorf("unknown email result = %+v, want empty", result)
	}

	insertPasswordResetUser(t, service.pool, "expired@example.com")
	if err := service.RequestPasswordReset(context.Background(), "expired@example.com"); err != nil {
		t.Fatalf("RequestPasswordReset() error = %v", err)
	}
	service.now = func() time.Time { return fixedNow.Add(10 * time.Minute) }
	result, err = service.VerifyPasswordResetOTP(context.Background(), "expired@example.com", sender.emails[0].OTP)
	if !errors.Is(err, ErrInvalidOrExpiredOTP) {
		t.Fatalf("expired OTP error = %v, want ErrInvalidOrExpiredOTP", err)
	}
	if result != (ResetToken{}) {
		t.Errorf("expired OTP result = %+v, want empty", result)
	}
}

func TestConfirmPasswordResetChangesPasswordConsumesTokenAndRevokesSessions(t *testing.T) {
	pool, service, sender := newPasswordResetIntegrationService(t)
	userID := insertPasswordResetUser(t, pool, "confirm-reset@example.com")
	const (
		oldPassword = "old-password-long-enough"
		newPassword = "new-password-long-enough"
	)
	setPasswordResetUserPassword(t, pool, userID, oldPassword)
	insertPasswordResetRefreshToken(t, pool, userID, "first-refresh-token")
	insertPasswordResetRefreshToken(t, pool, userID, "second-refresh-token")

	fixedNow := time.Now().UTC().Truncate(time.Second)
	service.now = func() time.Time { return fixedNow }
	if err := service.RequestPasswordReset(context.Background(), "confirm-reset@example.com"); err != nil {
		t.Fatalf("RequestPasswordReset() error = %v", err)
	}
	resetToken, err := service.VerifyPasswordResetOTP(
		context.Background(),
		"confirm-reset@example.com",
		sender.emails[0].OTP,
	)
	if err != nil {
		t.Fatalf("VerifyPasswordResetOTP() error = %v", err)
	}

	if err := service.ConfirmPasswordReset(context.Background(), " "+resetToken.Token+" ", newPassword); err != nil {
		t.Fatalf("ConfirmPasswordReset() error = %v", err)
	}

	var storedPasswordHash string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT password_hash FROM users WHERE id = $1",
		userID,
	).Scan(&storedPasswordHash); err != nil {
		t.Fatalf("read updated password hash: %v", err)
	}
	if auth.CheckPasswordHash(oldPassword, storedPasswordHash) {
		t.Error("old password still matches updated hash")
	}
	if !auth.CheckPasswordHash(newPassword, storedPasswordHash) {
		t.Error("new password does not match updated hash")
	}

	var consumed bool
	if err := pool.QueryRow(
		context.Background(),
		"SELECT consumed_at IS NOT NULL FROM password_reset_requests WHERE user_id = $1",
		userID,
	).Scan(&consumed); err != nil {
		t.Fatalf("read consumed password reset request: %v", err)
	}
	if !consumed {
		t.Error("password reset request was not consumed")
	}

	var activeRefreshTokens int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL",
		userID,
	).Scan(&activeRefreshTokens); err != nil {
		t.Fatalf("count active refresh tokens: %v", err)
	}
	if activeRefreshTokens != 0 {
		t.Errorf("active refresh tokens = %d, want 0", activeRefreshTokens)
	}

	if err := service.ConfirmPasswordReset(context.Background(), resetToken.Token, "another-valid-password"); !errors.Is(err, ErrInvalidOrExpiredResetToken) {
		t.Fatalf("second confirmation error = %v, want ErrInvalidOrExpiredResetToken", err)
	}
}

func TestConfirmPasswordResetRejectsExpiredAndUnknownTokens(t *testing.T) {
	pool, service, sender := newPasswordResetIntegrationService(t)
	fixedNow := time.Now().UTC().Truncate(time.Second)
	service.now = func() time.Time { return fixedNow }

	if err := service.ConfirmPasswordReset(
		context.Background(),
		"unknown-reset-token",
		"a-valid-new-password",
	); !errors.Is(err, ErrInvalidOrExpiredResetToken) {
		t.Fatalf("unknown token error = %v, want ErrInvalidOrExpiredResetToken", err)
	}

	userID := insertPasswordResetUser(t, pool, "expired-token@example.com")
	const oldPassword = "old-password-long-enough"
	setPasswordResetUserPassword(t, pool, userID, oldPassword)
	if err := service.RequestPasswordReset(context.Background(), "expired-token@example.com"); err != nil {
		t.Fatalf("RequestPasswordReset() error = %v", err)
	}
	resetToken, err := service.VerifyPasswordResetOTP(
		context.Background(),
		"expired-token@example.com",
		sender.emails[0].OTP,
	)
	if err != nil {
		t.Fatalf("VerifyPasswordResetOTP() error = %v", err)
	}

	service.now = func() time.Time { return fixedNow.Add(15 * time.Minute) }
	if err := service.ConfirmPasswordReset(
		context.Background(),
		resetToken.Token,
		"a-valid-new-password",
	); !errors.Is(err, ErrInvalidOrExpiredResetToken) {
		t.Fatalf("expired token error = %v, want ErrInvalidOrExpiredResetToken", err)
	}

	var storedPasswordHash string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT password_hash FROM users WHERE id = $1",
		userID,
	).Scan(&storedPasswordHash); err != nil {
		t.Fatalf("read password after expired confirmation: %v", err)
	}
	if !auth.CheckPasswordHash(oldPassword, storedPasswordHash) {
		t.Error("expired token changed the password")
	}
}

func TestDeleteExpiredPasswordResetRequests(t *testing.T) {
	pool, service, _ := newPasswordResetIntegrationService(t)
	expiredUserID := insertPasswordResetUser(t, pool, "cleanup-expired@example.com")
	activeUserID := insertPasswordResetUser(t, pool, "cleanup-active@example.com")
	consumedUserID := insertPasswordResetUser(t, pool, "cleanup-consumed@example.com")
	otpHash := make([]byte, 32)

	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO password_reset_requests (user_id, otp_hash, expires_at, created_at)
		 VALUES ($1, $2, NOW() - INTERVAL '1 hour', NOW() - INTERVAL '2 hours')`,
		expiredUserID,
		otpHash,
	); err != nil {
		t.Fatalf("insert expired password reset request: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO password_reset_requests (user_id, otp_hash, expires_at)
		 VALUES ($1, $2, NOW() + INTERVAL '1 hour')`,
		activeUserID,
		otpHash,
	); err != nil {
		t.Fatalf("insert active password reset request: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO password_reset_requests (
		     user_id, otp_hash, expires_at, consumed_at, created_at
		 ) VALUES (
		     $1, $2, NOW() + INTERVAL '1 hour', NOW(), NOW() - INTERVAL '1 hour'
		 )`,
		consumedUserID,
		otpHash,
	); err != nil {
		t.Fatalf("insert consumed password reset request: %v", err)
	}

	deleted, err := service.DeleteExpiredPasswordResetRequests(context.Background())
	if err != nil {
		t.Fatalf("DeleteExpiredPasswordResetRequests() error = %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	var remainingUserID int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT user_id FROM password_reset_requests",
	).Scan(&remainingUserID); err != nil {
		t.Fatalf("read remaining password reset request: %v", err)
	}
	if remainingUserID != activeUserID {
		t.Errorf("remaining user ID = %d, want active user %d", remainingUserID, activeUserID)
	}

	deleted, err = service.DeleteExpiredPasswordResetRequests(context.Background())
	if err != nil {
		t.Fatalf("second DeleteExpiredPasswordResetRequests() error = %v", err)
	}
	if deleted != 0 {
		t.Errorf("second deleted = %d, want 0", deleted)
	}
}

func newPasswordResetIntegrationService(t *testing.T) (*pgxpool.Pool, *Service, *fakeEmailSender) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL password-reset integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create integration admin pool: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		adminPool.Close()
		t.Fatalf("ping integration database: %v", err)
	}

	schemaName := randomPasswordResetSchemaName(t)
	schemaIdentifier := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+schemaIdentifier); err != nil {
		adminPool.Close()
		t.Fatalf("create integration schema: %v", err)
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		adminPool.Exec(context.Background(), "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		adminPool.Close()
		t.Fatalf("parse integration database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaName
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		adminPool.Exec(context.Background(), "DROP SCHEMA "+schemaIdentifier+" CASCADE")
		adminPool.Close()
		t.Fatalf("create isolated integration pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := adminPool.Exec(cleanupCtx, "DROP SCHEMA "+schemaIdentifier+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
		adminPool.Close()
	})

	applyPasswordResetMigrations(t, pool)
	sender := &fakeEmailSender{}
	service, err := NewService(
		pool,
		sqlc.New(pool),
		sender,
		testPepper,
		10*time.Minute,
		15*time.Minute,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return pool, service, sender
}

func applyPasswordResetMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("find migrations: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no up migrations found")
	}
	sort.Strings(paths)
	for _, path := range paths {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		if _, err := pool.Exec(context.Background(), string(migration)); err != nil {
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}
}

func insertPasswordResetUser(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	var userID int64
	if err := pool.QueryRow(
		context.Background(),
		"INSERT INTO users (email, full_name) VALUES ($1, 'Password Reset User') RETURNING id",
		email,
	).Scan(&userID); err != nil {
		t.Fatalf("insert password reset user: %v", err)
	}
	return userID
}

func setPasswordResetUserPassword(t *testing.T, pool *pgxpool.Pool, userID int64, password string) {
	t.Helper()
	passwordHash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		"UPDATE users SET password_hash = $1 WHERE id = $2",
		passwordHash,
		userID,
	); err != nil {
		t.Fatalf("set user password: %v", err)
	}
}

func insertPasswordResetRefreshToken(t *testing.T, pool *pgxpool.Pool, userID int64, rawToken string) {
	t.Helper()
	tokenHash := auth.HashRefreshToken(rawToken)
	if _, err := pool.Exec(
		context.Background(),
		`INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		 VALUES ($1, $2, NOW() + INTERVAL '1 day')`,
		userID,
		tokenHash[:],
	); err != nil {
		t.Fatalf("insert refresh token: %v", err)
	}
}

func countPasswordResetRequests(t *testing.T, pool *pgxpool.Pool, userID int64) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM password_reset_requests WHERE user_id = $1",
		userID,
	).Scan(&count); err != nil {
		t.Fatalf("count password reset requests: %v", err)
	}
	return count
}

func randomPasswordResetSchemaName(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_password_reset_test_%s", hex.EncodeToString(randomBytes[:]))
}

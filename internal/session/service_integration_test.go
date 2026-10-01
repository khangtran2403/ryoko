package session

import (
	"context"
	"crypto/rand"
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

const sessionRefreshTTL = 30 * 24 * time.Hour

func TestIssueTokenPairPersistsRefreshToken(t *testing.T) {
	pool, service, tokens := newSessionIntegrationService(t)
	userID := insertSessionUser(t, pool, "session@example.com", auth.RoleCustomer)
	fixedNow := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	service.now = func() time.Time { return fixedNow }

	pair, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("IssueTokenPair() error = %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("IssueTokenPair() returned empty token: %+v", pair)
	}
	if pair.AccessExpiresIn != 900 {
		t.Errorf("AccessExpiresIn = %d, want 900", pair.AccessExpiresIn)
	}
	wantRefreshExpiry := fixedNow.Add(sessionRefreshTTL)
	if !pair.RefreshExpiresAt.Equal(wantRefreshExpiry) {
		t.Errorf("RefreshExpiresAt = %v, want %v", pair.RefreshExpiresAt, wantRefreshExpiry)
	}
	principal, err := tokens.ParseToken(pair.AccessToken)
	if err != nil {
		t.Fatalf("ParseToken() error = %v", err)
	}
	if principal.UserID != userID || principal.Role != auth.RoleCustomer {
		t.Errorf("principal = %+v", principal)
	}

	stored := getStoredRefreshToken(t, pool, pair.RefreshToken)
	if stored.UserID != userID {
		t.Errorf("stored user ID = %d, want %d", stored.UserID, userID)
	}
	if !stored.ExpiresAt.Valid || !stored.ExpiresAt.Time.Equal(wantRefreshExpiry) {
		t.Errorf("stored expiry = %+v, want %v", stored.ExpiresAt, wantRefreshExpiry)
	}
	if stored.RevokedAt.Valid || stored.ReplacedByTokenID.Valid {
		t.Errorf("new refresh token is already revoked or replaced: %+v", stored)
	}

	missingPair, err := service.IssueTokenPair(context.Background(), 999999, auth.RoleCustomer)
	if err == nil {
		t.Fatal("IssueTokenPair() for missing user returned nil error")
	}
	if missingPair != (TokenPair{}) {
		t.Errorf("missing-user pair = %+v, want empty", missingPair)
	}
}

func TestRotateTokenPairReplacesToken(t *testing.T) {
	pool, service, tokens := newSessionIntegrationService(t)
	userID := insertSessionUser(t, pool, "rotate@example.com", auth.RoleAdmin)
	fixedNow := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	service.now = func() time.Time { return fixedNow }

	original, err := service.IssueTokenPair(context.Background(), userID, auth.RoleAdmin)
	if err != nil {
		t.Fatalf("IssueTokenPair() error = %v", err)
	}
	service.now = func() time.Time { return fixedNow.Add(time.Hour) }
	replacement, err := service.RotateTokenPair(context.Background(), original.RefreshToken)
	if err != nil {
		t.Fatalf("RotateTokenPair() error = %v", err)
	}
	if replacement.RefreshToken == original.RefreshToken {
		t.Fatal("rotation returned the original refresh token")
	}
	if !replacement.RefreshExpiresAt.Equal(fixedNow.Add(time.Hour).Add(sessionRefreshTTL)) {
		t.Errorf("replacement expiry = %v", replacement.RefreshExpiresAt)
	}
	principal, err := tokens.ParseToken(replacement.AccessToken)
	if err != nil {
		t.Fatalf("parse replacement access token: %v", err)
	}
	if principal.UserID != userID || principal.Role != auth.RoleAdmin {
		t.Errorf("replacement principal = %+v", principal)
	}

	oldStored := getStoredRefreshToken(t, pool, original.RefreshToken)
	newStored := getStoredRefreshToken(t, pool, replacement.RefreshToken)
	if !oldStored.RevokedAt.Valid {
		t.Error("original refresh token was not revoked")
	}
	if !oldStored.ReplacedByTokenID.Valid || oldStored.ReplacedByTokenID.Int64 != newStored.ID {
		t.Errorf("original replacement link = %+v, want %d", oldStored.ReplacedByTokenID, newStored.ID)
	}
	if newStored.RevokedAt.Valid || newStored.ReplacedByTokenID.Valid {
		t.Errorf("replacement token is not active: %+v", newStored)
	}
}

func TestRotateTokenPairRejectsUnknownExpiredAndRevokedTokens(t *testing.T) {
	pool, service, _ := newSessionIntegrationService(t)
	userID := insertSessionUser(t, pool, "invalid-refresh@example.com", auth.RoleCustomer)
	fixedNow := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	service.now = func() time.Time { return fixedNow }

	if _, err := service.RotateTokenPair(context.Background(), "unknown-token"); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("unknown token error = %v, want ErrInvalidRefreshToken", err)
	}

	expired, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("issue token for expiry test: %v", err)
	}
	service.now = func() time.Time { return expired.RefreshExpiresAt }
	if _, err := service.RotateTokenPair(context.Background(), expired.RefreshToken); !errors.Is(err, ErrRefreshTokenExpired) {
		t.Fatalf("expired token error = %v, want ErrRefreshTokenExpired", err)
	}

	service.now = func() time.Time { return fixedNow }
	revoked, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("issue token for revocation test: %v", err)
	}
	revokedHash := auth.HashRefreshToken(revoked.RefreshToken)
	if _, err := sqlc.New(pool).RevokeRefreshTokenByHash(context.Background(), revokedHash[:]); err != nil {
		t.Fatalf("revoke refresh token: %v", err)
	}
	if _, err := service.RotateTokenPair(context.Background(), revoked.RefreshToken); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Fatalf("revoked token error = %v, want ErrRefreshTokenRevoked", err)
	}
}

func TestConcurrentRefreshTokenReuseRevokesReplacement(t *testing.T) {
	pool, service, _ := newSessionIntegrationService(t)
	userID := insertSessionUser(t, pool, "concurrent-refresh@example.com", auth.RoleCustomer)
	original, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("IssueTokenPair() error = %v", err)
	}

	type result struct {
		pair TokenPair
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			pair, err := service.RotateTokenPair(context.Background(), original.RefreshToken)
			results <- result{pair: pair, err: err}
		}()
	}
	close(start)

	var successfulPair TokenPair
	var successCount, reuseCount int
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successCount++
			successfulPair = result.pair
		case errors.Is(result.err, ErrRefreshTokenReused):
			reuseCount++
		default:
			t.Fatalf("unexpected concurrent rotation error: %v", result.err)
		}
	}
	if successCount != 1 || reuseCount != 1 {
		t.Fatalf("results = {success:%d reuse:%d}, want {1 1}", successCount, reuseCount)
	}

	replacement := getStoredRefreshToken(t, pool, successfulPair.RefreshToken)
	if !replacement.RevokedAt.Valid {
		t.Error("replayed token did not revoke the active replacement")
	}
	if _, err := service.RotateTokenPair(context.Background(), successfulPair.RefreshToken); !errors.Is(err, ErrRefreshTokenRevoked) {
		t.Fatalf("replacement token error = %v, want ErrRefreshTokenRevoked", err)
	}
}

func TestDeleteExpiredRefreshTokens(t *testing.T) {
	pool, service, _ := newSessionIntegrationService(t)
	userID := insertSessionUser(t, pool, "cleanup@example.com", auth.RoleCustomer)
	pair, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("IssueTokenPair() error = %v", err)
	}
	token := getStoredRefreshToken(t, pool, pair.RefreshToken)
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE refresh_tokens
		 SET created_at = NOW() - INTERVAL '2 hours',
		     expires_at = NOW() - INTERVAL '1 hour'
		 WHERE id = $1`,
		token.ID,
	); err != nil {
		t.Fatalf("expire refresh token: %v", err)
	}

	deleted, err := service.DeleteExpiredRefreshTokens(context.Background())
	if err != nil {
		t.Fatalf("DeleteExpiredRefreshTokens() error = %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}
	deleted, err = service.DeleteExpiredRefreshTokens(context.Background())
	if err != nil {
		t.Fatalf("second DeleteExpiredRefreshTokens() error = %v", err)
	}
	if deleted != 0 {
		t.Errorf("second deleted = %d, want 0", deleted)
	}
}

func TestRevokeRefreshTokenIsIdempotentAndRevokesReplacement(t *testing.T) {
	pool, service, _ := newSessionIntegrationService(t)
	userID := insertSessionUser(t, pool, "logout@example.com", auth.RoleCustomer)

	active, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("issue active token: %v", err)
	}
	if err := service.RevokeRefreshToken(context.Background(), active.RefreshToken); err != nil {
		t.Fatalf("RevokeRefreshToken() error = %v", err)
	}
	if err := service.RevokeRefreshToken(context.Background(), active.RefreshToken); err != nil {
		t.Fatalf("second RevokeRefreshToken() error = %v", err)
	}
	if !getStoredRefreshToken(t, pool, active.RefreshToken).RevokedAt.Valid {
		t.Error("active refresh token was not revoked")
	}
	if err := service.RevokeRefreshToken(context.Background(), "unknown-refresh-token"); err != nil {
		t.Fatalf("unknown RevokeRefreshToken() error = %v", err)
	}

	original, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("issue token for rotation: %v", err)
	}
	replacement, err := service.RotateTokenPair(context.Background(), original.RefreshToken)
	if err != nil {
		t.Fatalf("RotateTokenPair() error = %v", err)
	}
	if err := service.RevokeRefreshToken(context.Background(), original.RefreshToken); err != nil {
		t.Fatalf("logout with rotated token: %v", err)
	}
	if !getStoredRefreshToken(t, pool, replacement.RefreshToken).RevokedAt.Valid {
		t.Error("logout with rotated token did not revoke its replacement")
	}
}

func TestConcurrentRefreshAndLogoutLeaveNoActiveToken(t *testing.T) {
	pool, service, _ := newSessionIntegrationService(t)
	userID := insertSessionUser(t, pool, "refresh-logout-race@example.com", auth.RoleCustomer)
	original, err := service.IssueTokenPair(context.Background(), userID, auth.RoleCustomer)
	if err != nil {
		t.Fatalf("IssueTokenPair() error = %v", err)
	}

	start := make(chan struct{})
	refreshResult := make(chan error, 1)
	logoutResult := make(chan error, 1)
	go func() {
		<-start
		_, err := service.RotateTokenPair(context.Background(), original.RefreshToken)
		refreshResult <- err
	}()
	go func() {
		<-start
		logoutResult <- service.RevokeRefreshToken(context.Background(), original.RefreshToken)
	}()
	close(start)

	refreshErr := <-refreshResult
	logoutErr := <-logoutResult
	if logoutErr != nil {
		t.Fatalf("concurrent logout error = %v", logoutErr)
	}
	if refreshErr != nil && !errors.Is(refreshErr, ErrRefreshTokenRevoked) {
		t.Fatalf("concurrent refresh error = %v, want nil or ErrRefreshTokenRevoked", refreshErr)
	}

	var activeCount int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL",
		userID,
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active refresh tokens: %v", err)
	}
	if activeCount != 0 {
		t.Errorf("active refresh token count = %d, want 0", activeCount)
	}
}

func newSessionIntegrationService(t *testing.T) (*pgxpool.Pool, *Service, *auth.TokenManager) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL session integration tests")
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

	schemaName := randomSessionSchemaName(t)
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

	applySessionMigrations(t, pool)
	tokens := newSessionTestTokenManager(t)
	service, err := NewService(pool, sqlc.New(pool), tokens, sessionRefreshTTL)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return pool, service, tokens
}

func applySessionMigrations(t *testing.T, pool *pgxpool.Pool) {
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

func insertSessionUser(t *testing.T, pool *pgxpool.Pool, email, role string) int64 {
	t.Helper()
	var userID int64
	if err := pool.QueryRow(
		context.Background(),
		`INSERT INTO users (email, full_name, role)
		 VALUES ($1, 'Session User', $2)
		 RETURNING id`,
		email,
		role,
	).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return userID
}

func getStoredRefreshToken(t *testing.T, pool *pgxpool.Pool, rawToken string) sqlc.RefreshToken {
	t.Helper()
	tokenHash := auth.HashRefreshToken(rawToken)
	var token sqlc.RefreshToken
	if err := pool.QueryRow(
		context.Background(),
		`SELECT id, user_id, token_hash, expires_at, revoked_at,
		        replaced_by_token_id, created_at
		 FROM refresh_tokens
		 WHERE token_hash = $1`,
		tokenHash[:],
	).Scan(
		&token.ID,
		&token.UserID,
		&token.TokenHash,
		&token.ExpiresAt,
		&token.RevokedAt,
		&token.ReplacedByTokenID,
		&token.CreatedAt,
	); err != nil {
		t.Fatalf("get stored refresh token: %v", err)
	}
	return token
}

func randomSessionSchemaName(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_session_test_%s", hex.EncodeToString(randomBytes[:]))
}

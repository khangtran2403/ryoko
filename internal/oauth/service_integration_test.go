package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/auth"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/session"
)

type recordingTokenIssuer struct {
	mu    sync.Mutex
	calls []issuedSession
}

type issuedSession struct {
	userID int64
	role   string
}

func (i *recordingTokenIssuer) IssueTokenPair(_ context.Context, userID int64, role string) (session.TokenPair, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls = append(i.calls, issuedSession{userID: userID, role: role})
	return session.TokenPair{AccessToken: fmt.Sprintf("access-%d", userID)}, nil
}

func (i *recordingTokenIssuer) snapshot() []issuedSession {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]issuedSession(nil), i.calls...)
}

func TestAuthenticateGoogleCreatesPasswordlessUserAndLink(t *testing.T) {
	pool, service, issuer := newOAuthIntegrationService(t)

	pair, err := service.AuthenticateGoogle(context.Background(), GoogleIdentity{
		Subject: "google-new-user",
		Email:   "NEW.User@Example.com",
		Name:    "New User",
	})
	if err != nil {
		t.Fatalf("AuthenticateGoogle() error = %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatal("AuthenticateGoogle() returned an empty access token")
	}

	var userID int64
	var email, fullName, role string
	var passwordHash pgtype.Text
	if err := pool.QueryRow(context.Background(),
		`SELECT id, email, password_hash, full_name, role
		 FROM users WHERE lower(email) = lower($1)`,
		"new.user@example.com",
	).Scan(&userID, &email, &passwordHash, &fullName, &role); err != nil {
		t.Fatalf("load OAuth user: %v", err)
	}
	if email != "new.user@example.com" || fullName != "New User" || role != auth.RoleCustomer {
		t.Errorf("created user = {%q %q %q}", email, fullName, role)
	}
	if passwordHash.Valid {
		t.Errorf("password hash = %q, want NULL", passwordHash.String)
	}

	assertOAuthAccount(t, pool, userID, "google-new-user", "new.user@example.com")
	calls := issuer.snapshot()
	if len(calls) != 1 || calls[0] != (issuedSession{userID: userID, role: auth.RoleCustomer}) {
		t.Errorf("issued sessions = %+v", calls)
	}
}

func TestAuthenticateGoogleLinksExistingUserWithoutOverwritingProfile(t *testing.T) {
	pool, service, issuer := newOAuthIntegrationService(t)
	userID := insertOAuthUser(t, pool, "existing@example.com", "Existing Name", auth.RoleAdmin)

	_, err := service.AuthenticateGoogle(context.Background(), GoogleIdentity{
		Subject: "google-existing-user",
		Email:   "existing@example.com",
		Name:    "Google Profile Name",
	})
	if err != nil {
		t.Fatalf("AuthenticateGoogle() error = %v", err)
	}

	var fullName, role string
	if err := pool.QueryRow(context.Background(),
		"SELECT full_name, role FROM users WHERE id = $1", userID,
	).Scan(&fullName, &role); err != nil {
		t.Fatalf("load existing user: %v", err)
	}
	if fullName != "Existing Name" || role != auth.RoleAdmin {
		t.Errorf("existing profile changed to {%q %q}", fullName, role)
	}
	assertOAuthAccount(t, pool, userID, "google-existing-user", "existing@example.com")
	calls := issuer.snapshot()
	if len(calls) != 1 || calls[0].userID != userID || calls[0].role != auth.RoleAdmin {
		t.Errorf("issued sessions = %+v", calls)
	}
}

func TestAuthenticateGoogleExistingLinkUpdatesOnlyProviderEmail(t *testing.T) {
	pool, service, _ := newOAuthIntegrationService(t)
	userID := insertOAuthUser(t, pool, "ryoko-address@example.com", "Linked User", auth.RoleCustomer)
	insertOAuthAccount(t, pool, userID, "stable-google-subject", "old-google-address@example.com")

	if _, err := service.AuthenticateGoogle(context.Background(), GoogleIdentity{
		Subject: "stable-google-subject",
		Email:   "new-google-address@example.com",
		Name:    "Linked User",
	}); err != nil {
		t.Fatalf("AuthenticateGoogle() error = %v", err)
	}

	assertOAuthAccount(t, pool, userID, "stable-google-subject", "new-google-address@example.com")
	var userEmail string
	if err := pool.QueryRow(context.Background(), "SELECT email FROM users WHERE id = $1", userID).Scan(&userEmail); err != nil {
		t.Fatalf("load user email: %v", err)
	}
	if userEmail != "ryoko-address@example.com" {
		t.Errorf("user email = %q, want unchanged Ryoko email", userEmail)
	}
}

func TestAuthenticateGoogleRejectsSecondGoogleIdentityForSameUser(t *testing.T) {
	pool, service, issuer := newOAuthIntegrationService(t)
	userID := insertOAuthUser(t, pool, "linked@example.com", "Linked User", auth.RoleCustomer)
	insertOAuthAccount(t, pool, userID, "first-google-subject", "linked@example.com")

	_, err := service.AuthenticateGoogle(context.Background(), GoogleIdentity{
		Subject: "different-google-subject",
		Email:   "linked@example.com",
		Name:    "Linked User",
	})
	if !errors.Is(err, ErrOAuthAccountConflict) {
		t.Fatalf("AuthenticateGoogle() error = %v, want ErrOAuthAccountConflict", err)
	}
	if calls := issuer.snapshot(); len(calls) != 0 {
		t.Errorf("issued sessions = %+v, want none", calls)
	}
}

func TestConcurrentAuthenticateGoogleConvergesOnOneAccount(t *testing.T) {
	pool, service, issuer := newOAuthIntegrationService(t)
	identity := GoogleIdentity{
		Subject: "concurrent-google-subject",
		Email:   "concurrent@example.com",
		Name:    "Concurrent User",
	}

	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := service.AuthenticateGoogle(context.Background(), identity)
			errorsCh <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-errorsCh; err != nil {
			t.Fatalf("concurrent AuthenticateGoogle() error = %v", err)
		}
	}

	var users, accounts int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM users WHERE lower(email) = lower($1)", identity.Email,
	).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM oauth_accounts WHERE provider = $1 AND provider_user_id = $2",
		ProviderGoogle, identity.Subject,
	).Scan(&accounts); err != nil {
		t.Fatalf("count OAuth accounts: %v", err)
	}
	if users != 1 || accounts != 1 {
		t.Errorf("users = %d, accounts = %d, want 1 and 1", users, accounts)
	}
	calls := issuer.snapshot()
	if len(calls) != 2 || calls[0].userID != calls[1].userID {
		t.Errorf("issued sessions = %+v, want two for the same user", calls)
	}
}

func newOAuthIntegrationService(t *testing.T) (*pgxpool.Pool, *Service, *recordingTokenIssuer) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL OAuth integration tests")
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

	schemaName := randomOAuthSchemaName(t)
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

	applyOAuthMigrations(t, pool)
	issuer := &recordingTokenIssuer{}
	service, err := NewService(pool, sqlc.New(pool), issuer)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return pool, service, issuer
}

func applyOAuthMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.up.sql"))
	if err != nil {
		t.Fatalf("find migrations: %v", err)
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

func insertOAuthUser(t *testing.T, pool *pgxpool.Pool, email, fullName, role string) int64 {
	t.Helper()
	var userID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, password_hash, full_name, role)
		 VALUES ($1, 'existing-password-hash', $2, $3)
		 RETURNING id`,
		email, fullName, role,
	).Scan(&userID); err != nil {
		t.Fatalf("insert OAuth test user: %v", err)
	}
	return userID
}

func insertOAuthAccount(t *testing.T, pool *pgxpool.Pool, userID int64, subject, email string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO oauth_accounts (user_id, provider, provider_user_id, provider_email)
		 VALUES ($1, $2, $3, $4)`,
		userID, ProviderGoogle, subject, email,
	); err != nil {
		t.Fatalf("insert OAuth account: %v", err)
	}
}

func assertOAuthAccount(t *testing.T, pool *pgxpool.Pool, userID int64, subject, email string) {
	t.Helper()
	var gotUserID int64
	var gotEmail string
	if err := pool.QueryRow(context.Background(),
		`SELECT user_id, provider_email FROM oauth_accounts
		 WHERE provider = $1 AND provider_user_id = $2`,
		ProviderGoogle, subject,
	).Scan(&gotUserID, &gotEmail); err != nil {
		t.Fatalf("load OAuth account: %v", err)
	}
	if gotUserID != userID || gotEmail != email {
		t.Errorf("OAuth account = {userID:%d email:%q}, want {%d %q}", gotUserID, gotEmail, userID, email)
	}
}

func randomOAuthSchemaName(t *testing.T) string {
	t.Helper()
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		t.Fatalf("generate random schema name: %v", err)
	}
	return fmt.Sprintf("ryoko_oauth_test_%s", hex.EncodeToString(randomBytes[:]))
}

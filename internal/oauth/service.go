package oauth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/session"
)

const (
	oauthProviderSubjectConstraint = "oauth_accounts_provider_provider_user_id_key"
	oauthUserProviderConstraint    = "oauth_accounts_user_id_provider_key"
)

var (
	ErrOAuthAccountConflict = errors.New("user already has a different account for this OAuth provider")
	ErrInvalidLoginCode     = errors.New("invalid OAuth login code")
	ErrExpiredLoginCode     = errors.New("OAuth login code expired")
	ErrConsumedLoginCode    = errors.New("OAuth login code already consumed")
)

type tokenPairIssuer interface {
	IssueTokenPairInTx(ctx context.Context, tx pgx.Tx, userID int64, role string) (session.TokenPair, error)
}

type AuthenticatedUser struct {
	ID   int64
	Role string
}

type LoginCode struct {
	Code      string
	ExpiresAt time.Time
}

// Service links verified provider identities, creates short-lived login codes,
// and exchanges each code exactly once for a Ryoko session. Provider tokens and
// raw login codes are deliberately never persisted.
type Service struct {
	pool         *pgxpool.Pool
	queries      *sqlc.Queries
	sessions     tokenPairIssuer
	loginCodeTTL time.Duration
	now          func() time.Time
}

func NewService(
	pool *pgxpool.Pool,
	queries *sqlc.Queries,
	sessions tokenPairIssuer,
	loginCodeTTL time.Duration,
) (*Service, error) {
	if pool == nil {
		return nil, errors.New("OAuth database pool is required")
	}
	if queries == nil {
		return nil, errors.New("OAuth queries are required")
	}
	if sessions == nil {
		return nil, errors.New("OAuth session service is required")
	}
	if loginCodeTTL < time.Second {
		return nil, errors.New("OAuth login code TTL must be at least one second")
	}
	return &Service{
		pool:         pool,
		queries:      queries,
		sessions:     sessions,
		loginCodeTTL: loginCodeTTL,
		now:          time.Now,
	}, nil
}

// CreateGoogleLoginCode authenticates and links a Google identity, then creates
// a short-lived code for the frontend. Only the code's SHA-256 hash is stored.
func (s *Service) CreateGoogleLoginCode(ctx context.Context, identity GoogleIdentity) (LoginCode, error) {
	user, err := s.AuthenticateGoogle(ctx, identity)
	if err != nil {
		return LoginCode{}, err
	}

	rawCode, err := generateRandomValue("OAuth login code")
	if err != nil {
		return LoginCode{}, err
	}
	codeHash := sha256.Sum256([]byte(rawCode))
	expiresAt := s.now().UTC().Add(s.loginCodeTTL)
	if _, err := s.queries.CreateOAuthLoginCode(ctx, sqlc.CreateOAuthLoginCodeParams{
		UserID:   user.ID,
		CodeHash: codeHash[:],
		ExpiresAt: pgtype.Timestamptz{
			Time:  expiresAt,
			Valid: true,
		},
	}); err != nil {
		return LoginCode{}, fmt.Errorf("store OAuth login code: %w", err)
	}
	return LoginCode{Code: rawCode, ExpiresAt: expiresAt}, nil
}

// ExchangeLoginCode locks and consumes a login code in the same transaction
// that stores the new refresh token. Concurrent or repeated exchanges cannot
// produce more than one session.
func (s *Service) ExchangeLoginCode(ctx context.Context, rawCode string) (session.TokenPair, error) {
	rawCode = strings.TrimSpace(rawCode)
	if rawCode == "" {
		return session.TokenPair{}, ErrInvalidLoginCode
	}
	codeHash := sha256.Sum256([]byte(rawCode))

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return session.TokenPair{}, fmt.Errorf("begin OAuth login code exchange: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := s.queries.WithTx(tx)

	stored, err := qtx.GetOAuthLoginCodeForUpdate(ctx, codeHash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return session.TokenPair{}, ErrInvalidLoginCode
	}
	if err != nil {
		return session.TokenPair{}, fmt.Errorf("get OAuth login code: %w", err)
	}
	if stored.ConsumedAt.Valid {
		return session.TokenPair{}, ErrConsumedLoginCode
	}
	now := s.now().UTC()
	if !stored.ExpiresAt.Valid || !now.Before(stored.ExpiresAt.Time) {
		return session.TokenPair{}, ErrExpiredLoginCode
	}

	pair, err := s.sessions.IssueTokenPairInTx(ctx, tx, stored.UserID, stored.Role)
	if err != nil {
		return session.TokenPair{}, fmt.Errorf("issue OAuth session: %w", err)
	}
	if _, err := qtx.ConsumeOAuthLoginCode(ctx, sqlc.ConsumeOAuthLoginCodeParams{
		ConsumedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ID:         stored.ID,
	}); errors.Is(err, pgx.ErrNoRows) {
		return session.TokenPair{}, ErrConsumedLoginCode
	} else if err != nil {
		return session.TokenPair{}, fmt.Errorf("consume OAuth login code: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return session.TokenPair{}, fmt.Errorf("commit OAuth login code exchange: %w", err)
	}
	return pair, nil
}

// AuthenticateGoogle logs in an already-linked identity or atomically links a
// first-time identity to a user with the same verified email address.
func (s *Service) AuthenticateGoogle(ctx context.Context, identity GoogleIdentity) (AuthenticatedUser, error) {
	identity, err := normalizeGoogleIdentity(identity)
	if err != nil {
		return AuthenticatedUser{}, err
	}

	lookup := sqlc.GetOAuthUserByProviderSubjectParams{
		Provider:       ProviderGoogle,
		ProviderUserID: identity.Subject,
	}
	linked, err := s.queries.GetOAuthUserByProviderSubject(ctx, lookup)
	if err == nil {
		if linked.ProviderEmail != identity.Email {
			if _, err := s.queries.UpdateOAuthAccountEmail(ctx, sqlc.UpdateOAuthAccountEmailParams{
				ProviderEmail:  identity.Email,
				OauthAccountID: linked.OauthAccountID,
			}); err != nil {
				return AuthenticatedUser{}, fmt.Errorf("update Google account email: %w", err)
			}
		}
		return AuthenticatedUser{ID: linked.UserID, Role: linked.Role}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AuthenticatedUser{}, fmt.Errorf("find Google account: %w", err)
	}

	return s.linkGoogleIdentity(ctx, identity)
}

func (s *Service) linkGoogleIdentity(ctx context.Context, identity GoogleIdentity) (AuthenticatedUser, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return AuthenticatedUser{}, fmt.Errorf("begin Google account linking: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	accountLookup := sqlc.GetOAuthAccountForUpdateParams{
		Provider:       ProviderGoogle,
		ProviderUserID: identity.Subject,
	}
	account, err := qtx.GetOAuthAccountForUpdate(ctx, accountLookup)
	if err == nil {
		return s.finishExistingLink(ctx, tx, qtx, account, identity.Email)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AuthenticatedUser{}, fmt.Errorf("lock Google account: %w", err)
	}

	user, err := qtx.GetOrCreateOAuthUser(ctx, sqlc.GetOrCreateOAuthUserParams{
		Email: identity.Email, FullName: identity.Name,
	})
	if err != nil {
		return AuthenticatedUser{}, fmt.Errorf("get or create OAuth user: %w", err)
	}

	account, err = qtx.GetOAuthAccountForUpdate(ctx, accountLookup)
	if err == nil {
		return s.finishExistingLink(ctx, tx, qtx, account, identity.Email)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AuthenticatedUser{}, fmt.Errorf("recheck Google account: %w", err)
	}

	_, err = qtx.CreateOAuthAccount(ctx, sqlc.CreateOAuthAccountParams{
		UserID: user.ID, Provider: ProviderGoogle,
		ProviderUserID: identity.Subject, ProviderEmail: identity.Email,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.ConstraintName {
			case oauthUserProviderConstraint:
				return AuthenticatedUser{}, ErrOAuthAccountConflict
			case oauthProviderSubjectConstraint:
				if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
					return AuthenticatedUser{}, fmt.Errorf("roll back concurrent Google link: %w", rollbackErr)
				}
				return s.loginExistingGoogleIdentity(ctx, identity)
			}
		}
		return AuthenticatedUser{}, fmt.Errorf("create Google account: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return AuthenticatedUser{}, fmt.Errorf("commit Google account linking: %w", err)
	}
	return AuthenticatedUser{ID: user.ID, Role: user.Role}, nil
}

func (s *Service) finishExistingLink(
	ctx context.Context,
	tx pgx.Tx,
	qtx *sqlc.Queries,
	account sqlc.OauthAccount,
	providerEmail string,
) (AuthenticatedUser, error) {
	if account.ProviderEmail != providerEmail {
		if _, err := qtx.UpdateOAuthAccountEmail(ctx, sqlc.UpdateOAuthAccountEmailParams{
			ProviderEmail: providerEmail, OauthAccountID: account.ID,
		}); err != nil {
			return AuthenticatedUser{}, fmt.Errorf("update Google account email: %w", err)
		}
	}
	linked, err := qtx.GetOAuthUserByProviderSubject(ctx, sqlc.GetOAuthUserByProviderSubjectParams{
		Provider: ProviderGoogle, ProviderUserID: account.ProviderUserID,
	})
	if err != nil {
		return AuthenticatedUser{}, fmt.Errorf("load linked Google user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AuthenticatedUser{}, fmt.Errorf("commit Google account login: %w", err)
	}
	return AuthenticatedUser{ID: linked.UserID, Role: linked.Role}, nil
}

func (s *Service) loginExistingGoogleIdentity(ctx context.Context, identity GoogleIdentity) (AuthenticatedUser, error) {
	linked, err := s.queries.GetOAuthUserByProviderSubject(ctx, sqlc.GetOAuthUserByProviderSubjectParams{
		Provider: ProviderGoogle, ProviderUserID: identity.Subject,
	})
	if err != nil {
		return AuthenticatedUser{}, fmt.Errorf("load concurrently linked Google user: %w", err)
	}
	if linked.ProviderEmail != identity.Email {
		if _, err := s.queries.UpdateOAuthAccountEmail(ctx, sqlc.UpdateOAuthAccountEmailParams{
			ProviderEmail: identity.Email, OauthAccountID: linked.OauthAccountID,
		}); err != nil {
			return AuthenticatedUser{}, fmt.Errorf("update Google account email: %w", err)
		}
	}
	return AuthenticatedUser{ID: linked.UserID, Role: linked.Role}, nil
}

func (s *Service) DeleteExpiredLoginCodes(ctx context.Context) (int64, error) {
	deleted, err := s.queries.DeleteExpiredOAuthLoginCodes(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired OAuth login codes: %w", err)
	}
	return deleted, nil
}

func normalizeGoogleIdentity(identity GoogleIdentity) (GoogleIdentity, error) {
	identity.Subject = strings.TrimSpace(identity.Subject)
	identity.Email = strings.ToLower(strings.TrimSpace(identity.Email))
	identity.Name = strings.TrimSpace(identity.Name)
	if identity.Subject == "" || identity.Email == "" || identity.Name == "" {
		return GoogleIdentity{}, ErrInvalidGoogleIdentity
	}
	parsed, err := mail.ParseAddress(identity.Email)
	if err != nil || parsed.Address != identity.Email {
		return GoogleIdentity{}, ErrInvalidGoogleIdentity
	}
	return identity, nil
}

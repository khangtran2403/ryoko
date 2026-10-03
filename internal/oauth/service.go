package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khangtran2403/ryoko/internal/db/sqlc"
	"github.com/khangtran2403/ryoko/internal/session"
)

const (
	oauthProviderSubjectConstraint = "oauth_accounts_provider_provider_user_id_key"
	oauthUserProviderConstraint    = "oauth_accounts_user_id_provider_key"
)

var ErrOAuthAccountConflict = errors.New("user already has a different account for this OAuth provider")

type tokenPairIssuer interface {
	IssueTokenPair(ctx context.Context, userID int64, role string) (session.TokenPair, error)
}

// Service links verified provider identities to Ryoko users and creates Ryoko
// sessions. Provider access and ID tokens are deliberately not persisted.
type Service struct {
	pool     *pgxpool.Pool
	queries  *sqlc.Queries
	sessions tokenPairIssuer
}

func NewService(pool *pgxpool.Pool, queries *sqlc.Queries, sessions tokenPairIssuer) (*Service, error) {
	if pool == nil {
		return nil, errors.New("OAuth database pool is required")
	}
	if queries == nil {
		return nil, errors.New("OAuth queries are required")
	}
	if sessions == nil {
		return nil, errors.New("OAuth session service is required")
	}
	return &Service{pool: pool, queries: queries, sessions: sessions}, nil
}

// AuthenticateGoogle logs in an already-linked Google identity or atomically
// links a first-time identity to a user with the same verified email address.
func (s *Service) AuthenticateGoogle(ctx context.Context, identity GoogleIdentity) (session.TokenPair, error) {
	identity, err := normalizeGoogleIdentity(identity)
	if err != nil {
		return session.TokenPair{}, err
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
				return session.TokenPair{}, fmt.Errorf("update Google account email: %w", err)
			}
		}
		return s.issueTokenPair(ctx, linked.UserID, linked.Role)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return session.TokenPair{}, fmt.Errorf("find Google account: %w", err)
	}

	return s.linkGoogleIdentity(ctx, identity)
}

func (s *Service) linkGoogleIdentity(ctx context.Context, identity GoogleIdentity) (session.TokenPair, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return session.TokenPair{}, fmt.Errorf("begin Google account linking: %w", err)
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
		return session.TokenPair{}, fmt.Errorf("lock Google account: %w", err)
	}

	user, err := qtx.GetOrCreateOAuthUser(ctx, sqlc.GetOrCreateOAuthUserParams{
		Email:    identity.Email,
		FullName: identity.Name,
	})
	if err != nil {
		return session.TokenPair{}, fmt.Errorf("get or create OAuth user: %w", err)
	}

	// GetOrCreateOAuthUser locks the email owner. Rechecking after that lock
	// makes concurrent first-time callbacks for the same identity converge.
	account, err = qtx.GetOAuthAccountForUpdate(ctx, accountLookup)
	if err == nil {
		return s.finishExistingLink(ctx, tx, qtx, account, identity.Email)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return session.TokenPair{}, fmt.Errorf("recheck Google account: %w", err)
	}

	_, err = qtx.CreateOAuthAccount(ctx, sqlc.CreateOAuthAccountParams{
		UserID:         user.ID,
		Provider:       ProviderGoogle,
		ProviderUserID: identity.Subject,
		ProviderEmail:  identity.Email,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.ConstraintName {
			case oauthUserProviderConstraint:
				return session.TokenPair{}, ErrOAuthAccountConflict
			case oauthProviderSubjectConstraint:
				// Another transaction linked this provider subject to a user
				// with a different email. Roll back our user change, then log
				// in the identity that won the race.
				if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
					return session.TokenPair{}, fmt.Errorf("roll back concurrent Google link: %w", rollbackErr)
				}
				return s.loginExistingGoogleIdentity(ctx, identity)
			}
		}
		return session.TokenPair{}, fmt.Errorf("create Google account: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return session.TokenPair{}, fmt.Errorf("commit Google account linking: %w", err)
	}
	return s.issueTokenPair(ctx, user.ID, user.Role)
}

func (s *Service) finishExistingLink(
	ctx context.Context,
	tx pgx.Tx,
	qtx *sqlc.Queries,
	account sqlc.OauthAccount,
	providerEmail string,
) (session.TokenPair, error) {
	if account.ProviderEmail != providerEmail {
		if _, err := qtx.UpdateOAuthAccountEmail(ctx, sqlc.UpdateOAuthAccountEmailParams{
			ProviderEmail:  providerEmail,
			OauthAccountID: account.ID,
		}); err != nil {
			return session.TokenPair{}, fmt.Errorf("update Google account email: %w", err)
		}
	}
	linked, err := qtx.GetOAuthUserByProviderSubject(ctx, sqlc.GetOAuthUserByProviderSubjectParams{
		Provider:       ProviderGoogle,
		ProviderUserID: account.ProviderUserID,
	})
	if err != nil {
		return session.TokenPair{}, fmt.Errorf("load linked Google user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return session.TokenPair{}, fmt.Errorf("commit Google account login: %w", err)
	}
	return s.issueTokenPair(ctx, linked.UserID, linked.Role)
}

func (s *Service) loginExistingGoogleIdentity(ctx context.Context, identity GoogleIdentity) (session.TokenPair, error) {
	linked, err := s.queries.GetOAuthUserByProviderSubject(ctx, sqlc.GetOAuthUserByProviderSubjectParams{
		Provider:       ProviderGoogle,
		ProviderUserID: identity.Subject,
	})
	if err != nil {
		return session.TokenPair{}, fmt.Errorf("load concurrently linked Google user: %w", err)
	}
	if linked.ProviderEmail != identity.Email {
		if _, err := s.queries.UpdateOAuthAccountEmail(ctx, sqlc.UpdateOAuthAccountEmailParams{
			ProviderEmail:  identity.Email,
			OauthAccountID: linked.OauthAccountID,
		}); err != nil {
			return session.TokenPair{}, fmt.Errorf("update Google account email: %w", err)
		}
	}
	return s.issueTokenPair(ctx, linked.UserID, linked.Role)
}

func (s *Service) issueTokenPair(ctx context.Context, userID int64, role string) (session.TokenPair, error) {
	pair, err := s.sessions.IssueTokenPair(ctx, userID, role)
	if err != nil {
		return session.TokenPair{}, fmt.Errorf("issue OAuth session: %w", err)
	}
	return pair, nil
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

package session

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

var (
	ErrInvalidUserID       = errors.New("user ID must be positive")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrRefreshTokenExpired = errors.New("refresh token expired")
	ErrRefreshTokenRevoked = errors.New("refresh token revoked")
	ErrRefreshTokenReused  = errors.New("refresh token reuse detected")
)

type TokenPair struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresIn  int64
	RefreshExpiresAt time.Time
}

type Service struct {
	pool       *pgxpool.Pool
	queries    *sqlc.Queries
	tokens     *auth.TokenManager
	refreshTTL time.Duration
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool, queries *sqlc.Queries, tokens *auth.TokenManager, refreshTTL time.Duration) (*Service, error) {
	if refreshTTL <= 0 {
		return nil, errors.New("refresh token TTL must be positive")
	}

	return &Service{
		pool:       pool,
		queries:    queries,
		tokens:     tokens,
		refreshTTL: refreshTTL,
		now:        time.Now,
	}, nil
}
func (s *Service) IssueTokenPair(ctx context.Context, userID int64, role string) (TokenPair, error) {
	if userID <= 0 {
		return TokenPair{}, ErrInvalidUserID
	}
	accessToken, err := s.tokens.GenerateToken(userID, role)
	if err != nil {
		return TokenPair{}, fmt.Errorf("generate access token: %w", err)
	}
	refreshToken, err := auth.GenerateRefreshToken()
	if err != nil {
		return TokenPair{}, fmt.Errorf("generate refresh token: %w", err)
	}
	refreshExpiresAt := s.now().UTC().Add(s.refreshTTL)
	hashToken := auth.HashRefreshToken(refreshToken)
	_, err = s.queries.CreateRefreshToken(ctx, sqlc.CreateRefreshTokenParams{
		UserID:    userID,
		TokenHash: hashToken[:],
		ExpiresAt: pgtype.Timestamptz{
			Time:  refreshExpiresAt,
			Valid: true,
		},
	},
	)
	if err != nil {
		return TokenPair{}, fmt.Errorf("store refresh token: %w", err)
	}
	return TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     refreshToken,
		AccessExpiresIn:  int64(s.tokens.TTL() / time.Second),
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func (s *Service) RotateTokenPair(ctx context.Context, rawRefreshToken string) (TokenPair, error) {
	rawRefreshToken = strings.TrimSpace(rawRefreshToken)
	if rawRefreshToken == "" {
		return TokenPair{}, ErrInvalidRefreshToken
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return TokenPair{}, fmt.Errorf("begin refresh token rotation: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	tokenHash := auth.HashRefreshToken(rawRefreshToken)
	currentToken, err := qtx.GetRefreshTokenForUpdate(ctx, tokenHash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	if err != nil {
		return TokenPair{}, fmt.Errorf("get refresh token for update: %w", err)
	}

	if currentToken.ReplacedByTokenID.Valid {
		if _, err := qtx.RevokeAllUserRefreshTokens(ctx, currentToken.UserID); err != nil {
			return TokenPair{}, fmt.Errorf("revoke user refresh tokens after reuse: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return TokenPair{}, fmt.Errorf("commit refresh token reuse revocation: %w", err)
		}
		return TokenPair{}, ErrRefreshTokenReused
	}
	if currentToken.RevokedAt.Valid {
		return TokenPair{}, ErrRefreshTokenRevoked
	}

	now := s.now().UTC()
	if !currentToken.ExpiresAt.Valid || !now.Before(currentToken.ExpiresAt.Time) {
		return TokenPair{}, ErrRefreshTokenExpired
	}

	user, err := qtx.GetUserByID(ctx, currentToken.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrInvalidRefreshToken
	}
	if err != nil {
		return TokenPair{}, fmt.Errorf("get refresh token user: %w", err)
	}

	accessToken, err := s.tokens.GenerateToken(user.ID, user.Role)
	if err != nil {
		return TokenPair{}, fmt.Errorf("generate rotated access token: %w", err)
	}
	rawReplacement, err := auth.GenerateRefreshToken()
	if err != nil {
		return TokenPair{}, fmt.Errorf("generate replacement refresh token: %w", err)
	}
	replacementHash := auth.HashRefreshToken(rawReplacement)
	replacementExpiresAt := now.Add(s.refreshTTL)
	replacement, err := qtx.CreateRefreshToken(ctx, sqlc.CreateRefreshTokenParams{
		UserID:    user.ID,
		TokenHash: replacementHash[:],
		ExpiresAt: pgtype.Timestamptz{
			Time:  replacementExpiresAt,
			Valid: true,
		},
	})
	if err != nil {
		return TokenPair{}, fmt.Errorf("store replacement refresh token: %w", err)
	}

	_, err = qtx.MarkRefreshTokenReplaced(ctx, sqlc.MarkRefreshTokenReplacedParams{
		ID: currentToken.ID,
		ReplacedByTokenID: pgtype.Int8{
			Int64: replacement.ID,
			Valid: true,
		},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TokenPair{}, ErrRefreshTokenRevoked
	}
	if err != nil {
		return TokenPair{}, fmt.Errorf("mark refresh token replaced: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return TokenPair{}, fmt.Errorf("commit refresh token rotation: %w", err)
	}

	return TokenPair{
		AccessToken:      accessToken,
		RefreshToken:     rawReplacement,
		AccessExpiresIn:  int64(s.tokens.TTL() / time.Second),
		RefreshExpiresAt: replacementExpiresAt,
	}, nil
}

func (s *Service) RevokeRefreshToken(ctx context.Context, rawRefreshToken string) error {
	rawRefreshToken = strings.TrimSpace(rawRefreshToken)
	if rawRefreshToken == "" {
		return ErrInvalidRefreshToken
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin refresh token revocation: %w", err)
	}
	defer tx.Rollback(ctx)

	qtx := s.queries.WithTx(tx)
	tokenHash := auth.HashRefreshToken(rawRefreshToken)
	stored, err := qtx.GetRefreshTokenForUpdate(ctx, tokenHash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get refresh token for revocation: %w", err)
	}

	if stored.ReplacedByTokenID.Valid {
		if _, err := qtx.RevokeAllUserRefreshTokens(ctx, stored.UserID); err != nil {
			return fmt.Errorf("revoke user refresh tokens after logout: %w", err)
		}
	} else if !stored.RevokedAt.Valid {
		if _, err := qtx.RevokeRefreshTokenByHash(ctx, tokenHash[:]); err != nil {
			return fmt.Errorf("revoke refresh token: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit refresh token revocation: %w", err)
	}

	return nil
}

func (s *Service) DeleteExpiredRefreshTokens(ctx context.Context) (int64, error) {
	deleted, err := s.queries.DeleteExpiredRefreshTokens(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired refresh tokens: %w", err)
	}
	return deleted, nil
}

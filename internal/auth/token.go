package auth

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	RoleCustomer = "customer"
	RoleAdmin    = "admin"
)

var ErrInvalidToken = errors.New("invalid token")

type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

type Principal struct {
	UserID int64
	Role   string
}

type TokenManager struct {
	secret   []byte
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

func NewTokenManager(secret, issuer, audience string, ttl time.Duration) (*TokenManager, error) {
	if len([]byte(secret)) < 32 || strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("JWT secret must contain at least 32 bytes")
	}

	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		return nil, fmt.Errorf("JWT issuer is required")
	}

	audience = strings.TrimSpace(audience)
	if audience == "" {
		return nil, fmt.Errorf("JWT audience is required")
	}

	if ttl <= 0 {
		return nil, fmt.Errorf("JWT TTL must be positive")
	}

	return &TokenManager{
		secret:   append([]byte(nil), []byte(secret)...),
		issuer:   issuer,
		audience: audience,
		ttl:      ttl,
		now:      time.Now,
	}, nil
}

func (m *TokenManager) GenerateToken(userID int64, role string) (string, error) {
	if userID <= 0 {
		return "", fmt.Errorf("user ID must be positive")
	}
	if !validRole(role) {
		return "", fmt.Errorf("invalid role")
	}

	now := m.now().UTC()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{m.audience},
			Subject:   strconv.FormatInt(userID, 10),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

func (m *TokenManager) ParseToken(tokenString string) (*Principal, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		func(_ *jwt.Token) (any, error) {
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithNotBeforeRequired(),
		jwt.WithTimeFunc(m.now),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 || !validRole(claims.Role) {
		return nil, ErrInvalidToken
	}

	return &Principal{
		UserID: userID,
		Role:   claims.Role,
	}, nil
}
func (m *TokenManager) TTL() time.Duration {
	return m.ttl
}

func validRole(role string) bool {
	return role == RoleCustomer || role == RoleAdmin
}

package oauth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	googleJWKSURL      = "https://www.googleapis.com/oauth2/v3/certs"
	googleModernIssuer = "https://accounts.google.com"
	googleLegacyIssuer = "accounts.google.com"
)

type GoogleIDTokenValidator struct {
	client  *http.Client
	jwksURL string
	now     func() time.Time

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	expiresAt time.Time
	refreshMu sync.Mutex
}

type googleIDTokenClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Nonce         string `json:"nonce"`
	jwt.RegisteredClaims
}

type googleJWKS struct {
	Keys []googleJWK `json:"keys"`
}

type googleJWK struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	Modulus   string `json:"n"`
	Exponent  string `json:"e"`
}

func NewGoogleIDTokenValidator(client *http.Client) *GoogleIDTokenValidator {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &GoogleIDTokenValidator{
		client:  client,
		jwksURL: googleJWKSURL,
		now:     time.Now,
		keys:    make(map[string]*rsa.PublicKey),
	}
}

func (v *GoogleIDTokenValidator) Validate(
	ctx context.Context,
	rawToken string,
	audience string,
	expectedNonce string,
) (GoogleIdentity, error) {
	if rawToken == "" || audience == "" || expectedNonce == "" {
		return GoogleIdentity{}, errors.New("ID token, audience, and nonce are required")
	}

	claims := &googleIDTokenClaims{}
	token, err := jwt.ParseWithClaims(
		rawToken,
		claims,
		func(token *jwt.Token) (any, error) {
			keyID, ok := token.Header["kid"].(string)
			if !ok || keyID == "" {
				return nil, errors.New("Google ID token is missing a key ID")
			}
			return v.key(ctx, keyID)
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(v.now),
	)
	if err != nil || !token.Valid {
		return GoogleIdentity{}, fmt.Errorf("validate Google ID token: %w", err)
	}
	if claims.Issuer != googleModernIssuer && claims.Issuer != googleLegacyIssuer {
		return GoogleIdentity{}, errors.New("Google ID token has an invalid issuer")
	}
	if claims.Subject == "" {
		return GoogleIdentity{}, errors.New("Google ID token is missing a subject")
	}
	if !SecureEqual(claims.Nonce, expectedNonce) {
		return GoogleIdentity{}, errors.New("Google ID token nonce does not match")
	}
	if !claims.EmailVerified {
		return GoogleIdentity{}, errors.New("Google email is not verified")
	}

	email := strings.ToLower(strings.TrimSpace(claims.Email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return GoogleIdentity{}, errors.New("Google ID token contains an invalid email")
	}
	name := strings.TrimSpace(claims.Name)
	if name == "" {
		name = strings.SplitN(email, "@", 2)[0]
	}

	return GoogleIdentity{
		Subject: claims.Subject,
		Email:   email,
		Name:    name,
	}, nil
}

func (v *GoogleIDTokenValidator) key(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	if key := v.cachedKey(keyID); key != nil {
		return key, nil
	}

	v.refreshMu.Lock()
	defer v.refreshMu.Unlock()
	if key := v.cachedKey(keyID); key != nil {
		return key, nil
	}
	if err := v.refreshKeys(ctx); err != nil {
		return nil, err
	}
	v.mu.RLock()
	key := v.keys[keyID]
	v.mu.RUnlock()
	if key == nil {
		return nil, errors.New("Google signing key was not found")
	}
	return key, nil
}

func (v *GoogleIDTokenValidator) cachedKey(keyID string) *rsa.PublicKey {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if !v.now().Before(v.expiresAt) {
		return nil
	}
	return v.keys[keyID]
}

func (v *GoogleIDTokenValidator) refreshKeys(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("create Google JWKS request: %w", err)
	}
	response, err := v.client.Do(request)
	if err != nil {
		return fmt.Errorf("fetch Google JWKS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return fmt.Errorf("fetch Google JWKS: unexpected status %d", response.StatusCode)
	}

	var document googleJWKS
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode Google JWKS: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, jwk := range document.Keys {
		if jwk.KeyType != "RSA" || jwk.Algorithm != "RS256" || jwk.Use != "sig" || jwk.KeyID == "" {
			continue
		}
		key, err := rsaKeyFromJWK(jwk)
		if err != nil {
			continue
		}
		keys[jwk.KeyID] = key
	}
	if len(keys) == 0 {
		return errors.New("Google JWKS did not contain usable RSA signing keys")
	}

	cacheTTL := cacheMaxAge(response.Header.Get("Cache-Control"))
	v.mu.Lock()
	v.keys = keys
	v.expiresAt = v.now().Add(cacheTTL)
	v.mu.Unlock()
	return nil
}

func rsaKeyFromJWK(jwk googleJWK) (*rsa.PublicKey, error) {
	modulusBytes, err := base64.RawURLEncoding.DecodeString(jwk.Modulus)
	if err != nil || len(modulusBytes) == 0 {
		return nil, errors.New("invalid RSA modulus")
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(jwk.Exponent)
	if err != nil || len(exponentBytes) == 0 {
		return nil, errors.New("invalid RSA exponent")
	}
	exponent := new(big.Int).SetBytes(exponentBytes)
	if !exponent.IsInt64() || exponent.Int64() <= 1 || exponent.Int64() > int64(^uint(0)>>1) {
		return nil, errors.New("invalid RSA exponent")
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulusBytes),
		E: int(exponent.Int64()),
	}, nil
}

func cacheMaxAge(cacheControl string) time.Duration {
	for _, directive := range strings.Split(cacheControl, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(directive), "=")
		if !found || strings.ToLower(name) != "max-age" {
			continue
		}
		seconds, err := strconv.ParseInt(strings.Trim(value, `"`), 10, 64)
		if err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Hour
}

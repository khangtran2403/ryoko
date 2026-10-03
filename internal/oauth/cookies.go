package oauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	flowCookiePath     = "/auth/google"
	stateCookieName    = "ryoko_oauth_state"
	verifierCookieName = "ryoko_oauth_pkce"
	nonceCookieName    = "ryoko_oauth_nonce"
)

var ErrInvalidFlowCookies = errors.New("invalid or expired OAuth flow cookies")

type FlowValues struct {
	State        string
	PKCEVerifier string
	Nonce        string
}

type FlowCookieManager struct {
	secret []byte
	secure bool
	ttl    time.Duration
	now    func() time.Time
}

func NewFlowCookieManager(secret string, secure bool, ttl time.Duration) (*FlowCookieManager, error) {
	if len([]byte(secret)) < 32 {
		return nil, errors.New("OAuth cookie secret must contain at least 32 bytes")
	}
	if ttl < time.Second {
		return nil, errors.New("OAuth flow TTL must be at least one second")
	}

	return &FlowCookieManager{
		secret: []byte(secret),
		secure: secure,
		ttl:    ttl,
		now:    time.Now,
	}, nil
}

func (m *FlowCookieManager) Set(w http.ResponseWriter, values FlowValues) error {
	if values.State == "" || values.PKCEVerifier == "" || values.Nonce == "" {
		return errors.New("OAuth flow values must not be empty")
	}

	expiresAt := m.now().UTC().Add(m.ttl)
	m.setCookie(w, stateCookieName, m.sign(stateCookieName, values.State), expiresAt, int(m.ttl/time.Second))
	m.setCookie(w, verifierCookieName, m.sign(verifierCookieName, values.PKCEVerifier), expiresAt, int(m.ttl/time.Second))
	m.setCookie(w, nonceCookieName, m.sign(nonceCookieName, values.Nonce), expiresAt, int(m.ttl/time.Second))
	return nil
}

// ReadAndClear deletes all flow cookies before attempting validation so every
// callback consumes the browser-side flow state, including failed callbacks.
func (m *FlowCookieManager) ReadAndClear(w http.ResponseWriter, r *http.Request) (FlowValues, error) {
	m.Clear(w)

	state, err := m.readSignedCookie(r, stateCookieName)
	if err != nil {
		return FlowValues{}, ErrInvalidFlowCookies
	}
	verifier, err := m.readSignedCookie(r, verifierCookieName)
	if err != nil {
		return FlowValues{}, ErrInvalidFlowCookies
	}
	nonce, err := m.readSignedCookie(r, nonceCookieName)
	if err != nil {
		return FlowValues{}, ErrInvalidFlowCookies
	}

	return FlowValues{
		State:        state,
		PKCEVerifier: verifier,
		Nonce:        nonce,
	}, nil
}

func (m *FlowCookieManager) Clear(w http.ResponseWriter) {
	expiredAt := time.Unix(1, 0).UTC()
	m.setCookie(w, stateCookieName, "", expiredAt, -1)
	m.setCookie(w, verifierCookieName, "", expiredAt, -1)
	m.setCookie(w, nonceCookieName, "", expiredAt, -1)
}

func (m *FlowCookieManager) setCookie(
	w http.ResponseWriter,
	name string,
	value string,
	expires time.Time,
	maxAge int,
) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     flowCookiePath,
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *FlowCookieManager) sign(name, value string) string {
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(name))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return value + "." + signature
}

func (m *FlowCookieManager) readSignedCookie(r *http.Request, name string) (string, error) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return "", err
	}
	value, signature, found := strings.Cut(cookie.Value, ".")
	if !found || value == "" || signature == "" {
		return "", ErrInvalidFlowCookies
	}
	expected := m.sign(name, value)
	if !SecureEqual(cookie.Value, expected) {
		return "", ErrInvalidFlowCookies
	}
	return value, nil
}

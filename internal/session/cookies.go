package session

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	refreshCookieName = "ryoko_refresh_token"
	refreshCookiePath = "/auth"
)

var ErrRefreshCookieMissing = errors.New("refresh-token cookie is missing")

type RefreshCookieManager struct {
	secure bool
	now    func() time.Time
}

func NewRefreshCookieManager(secure bool) *RefreshCookieManager {
	return &RefreshCookieManager{secure: secure, now: time.Now}
}

func (m *RefreshCookieManager) Set(w http.ResponseWriter, rawToken string, expiresAt time.Time) error {
	rawToken = strings.TrimSpace(rawToken)
	now := m.now().UTC()
	if rawToken == "" || !expiresAt.After(now) {
		return errors.New("valid refresh token and future expiry are required")
	}
	maxAge := int(expiresAt.Sub(now).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: rawToken,
		Path: refreshCookiePath, Expires: expiresAt.UTC(), MaxAge: maxAge,
		HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (m *RefreshCookieManager) Read(r *http.Request) (string, error) {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", ErrRefreshCookieMissing
	}
	return strings.TrimSpace(cookie.Value), nil
}

func (m *RefreshCookieManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: "",
		Path: refreshCookiePath, Expires: time.Unix(1, 0).UTC(), MaxAge: -1,
		HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteLaxMode,
	})
}

package session

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRefreshCookieManagerSetReadAndClear(t *testing.T) {
	fixedNow := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	manager := NewRefreshCookieManager(true)
	manager.now = func() time.Time { return fixedNow }
	recorder := httptest.NewRecorder()
	if err := manager.Set(recorder, "refresh-token", fixedNow.Add(time.Hour)); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != refreshCookieName || cookie.Value != "refresh-token" ||
		cookie.Path != refreshCookiePath || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 3600 {
		t.Errorf("refresh cookie = %+v", cookie)
	}

	request := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	request.AddCookie(cookie)
	got, err := manager.Read(request)
	if err != nil || got != "refresh-token" {
		t.Errorf("Read() = %q, %v", got, err)
	}

	clearRecorder := httptest.NewRecorder()
	manager.Clear(clearRecorder)
	cleared := clearRecorder.Result().Cookies()[0]
	if cleared.MaxAge != -1 || cleared.Value != "" || cleared.Path != refreshCookiePath || !cleared.HttpOnly {
		t.Errorf("cleared cookie = %+v", cleared)
	}
}

func TestRefreshCookieManagerRejectsMissingOrInvalidCookie(t *testing.T) {
	fixedNow := time.Now().UTC()
	manager := NewRefreshCookieManager(false)
	manager.now = func() time.Time { return fixedNow }
	if err := manager.Set(httptest.NewRecorder(), "", fixedNow.Add(time.Hour)); err == nil {
		t.Fatal("Set() with blank token returned nil error")
	}
	if err := manager.Set(httptest.NewRecorder(), "token", fixedNow); err == nil {
		t.Fatal("Set() with expired timestamp returned nil error")
	}
	request := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	if _, err := manager.Read(request); !errors.Is(err, ErrRefreshCookieMissing) {
		t.Errorf("Read() error = %v, want ErrRefreshCookieMissing", err)
	}
}

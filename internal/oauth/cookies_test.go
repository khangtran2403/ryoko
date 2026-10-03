package oauth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testCookieSecret = "test-oauth-cookie-secret-at-least-32-bytes-long"

func TestNewFlowCookieManagerValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		ttl    time.Duration
	}{
		{name: "short secret", secret: "too-short", ttl: time.Minute},
		{name: "zero TTL", secret: testCookieSecret},
		{name: "sub-second TTL", secret: testCookieSecret, ttl: 500 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewFlowCookieManager(tt.secret, true, tt.ttl)
			if err == nil {
				t.Fatal("NewFlowCookieManager() returned nil error")
			}
			if manager != nil {
				t.Errorf("manager = %+v, want nil", manager)
			}
		})
	}
}

func TestFlowCookieManagerSetAndReadAndClear(t *testing.T) {
	manager, err := NewFlowCookieManager(testCookieSecret, true, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewFlowCookieManager() error = %v", err)
	}
	fixedNow := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	manager.now = func() time.Time { return fixedNow }
	want := FlowValues{
		State:        "state-value",
		PKCEVerifier: "verifier-value",
		Nonce:        "nonce-value",
	}

	setRecorder := httptest.NewRecorder()
	if err := manager.Set(setRecorder, want); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	setCookies := setRecorder.Result().Cookies()
	if len(setCookies) != 3 {
		t.Fatalf("set cookies = %d, want 3", len(setCookies))
	}

	request := httptest.NewRequest(http.MethodGet, "/auth/google/callback", nil)
	for _, cookie := range setCookies {
		if cookie.Path != flowCookiePath || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie attributes = %+v", cookie)
		}
		if cookie.MaxAge != 600 {
			t.Errorf("cookie MaxAge = %d, want 600", cookie.MaxAge)
		}
		if !cookie.Expires.Equal(fixedNow.Add(10 * time.Minute)) {
			t.Errorf("cookie expiry = %v", cookie.Expires)
		}
		request.AddCookie(cookie)
	}

	clearRecorder := httptest.NewRecorder()
	got, err := manager.ReadAndClear(clearRecorder, request)
	if err != nil {
		t.Fatalf("ReadAndClear() error = %v", err)
	}
	if got != want {
		t.Errorf("ReadAndClear() = %+v, want %+v", got, want)
	}
	assertClearedFlowCookies(t, clearRecorder)
}

func TestFlowCookieManagerRejectsTamperingAndStillClears(t *testing.T) {
	manager, err := NewFlowCookieManager(testCookieSecret, false, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewFlowCookieManager() error = %v", err)
	}
	setRecorder := httptest.NewRecorder()
	if err := manager.Set(setRecorder, FlowValues{
		State: "state", PKCEVerifier: "verifier", Nonce: "nonce",
	}); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/auth/google/callback", nil)
	for _, cookie := range setRecorder.Result().Cookies() {
		if cookie.Name == stateCookieName {
			cookie.Value = "tampered." + cookie.Value
		}
		request.AddCookie(cookie)
	}
	clearRecorder := httptest.NewRecorder()
	if _, err := manager.ReadAndClear(clearRecorder, request); err != ErrInvalidFlowCookies {
		t.Fatalf("ReadAndClear() error = %v, want ErrInvalidFlowCookies", err)
	}
	assertClearedFlowCookies(t, clearRecorder)
}

func TestFlowCookieManagerSetRejectsMissingValues(t *testing.T) {
	manager, err := NewFlowCookieManager(testCookieSecret, false, 10*time.Minute)
	if err != nil {
		t.Fatalf("NewFlowCookieManager() error = %v", err)
	}
	for _, values := range []FlowValues{
		{PKCEVerifier: "verifier", Nonce: "nonce"},
		{State: "state", Nonce: "nonce"},
		{State: "state", PKCEVerifier: "verifier"},
	} {
		if err := manager.Set(httptest.NewRecorder(), values); err == nil {
			t.Errorf("Set(%+v) returned nil error", values)
		}
	}
}

func assertClearedFlowCookies(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	cookies := recorder.Result().Cookies()
	if len(cookies) != 3 {
		t.Fatalf("cleared cookies = %d, want 3", len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.Value != "" || cookie.MaxAge != -1 || cookie.Path != flowCookiePath || !cookie.HttpOnly {
			t.Errorf("cleared cookie attributes = %+v", cookie)
		}
	}
}

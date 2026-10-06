package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadTokenDurations(t *testing.T) {
	setRequiredSMTPEnv(t)
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
	t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
	t.Setenv("ACCESS_TOKEN_TTL_MINUTES", "20")
	t.Setenv("REFRESH_TOKEN_TTL_HOURS", "48")
	t.Setenv("REFRESH_TOKEN_CLEANUP_INTERVAL_HOURS", "6")
	t.Setenv("REFRESH_COOKIE_SECURE", "false")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.JWT.AccessTTL != 20*time.Minute {
		t.Errorf("AccessTTL = %v, want %v", loaded.JWT.AccessTTL, 20*time.Minute)
	}
	if loaded.JWT.RefreshTTL != 48*time.Hour {
		t.Errorf("RefreshTTL = %v, want %v", loaded.JWT.RefreshTTL, 48*time.Hour)
	}
	if loaded.JWT.CleanupInterval != 6*time.Hour {
		t.Errorf("CleanupInterval = %v, want %v", loaded.JWT.CleanupInterval, 6*time.Hour)
	}
	if loaded.JWT.RefreshCookieSecure {
		t.Error("RefreshCookieSecure = true, want false")
	}
}

func TestLoadRejectsInvalidTokenDurations(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid access TTL", key: "ACCESS_TOKEN_TTL_MINUTES", value: "abc"},
		{name: "zero refresh TTL", key: "REFRESH_TOKEN_TTL_HOURS", value: "0"},
		{name: "negative cleanup interval", key: "REFRESH_TOKEN_CLEANUP_INTERVAL_HOURS", value: "-1"},
		{name: "invalid refresh cookie secure", key: "REFRESH_COOKIE_SECURE", value: "sometimes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredSMTPEnv(t)
			t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
			t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
			t.Setenv("ACCESS_TOKEN_TTL_MINUTES", "15")
			t.Setenv("REFRESH_TOKEN_TTL_HOURS", "720")
			t.Setenv("REFRESH_TOKEN_CLEANUP_INTERVAL_HOURS", "24")
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() returned nil error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error = %q, want key %q", err, tt.key)
			}
		})
	}
}

func TestLoadPasswordResetConfig(t *testing.T) {
	setRequiredSMTPEnv(t)
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
	t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
	t.Setenv("PASSWORD_RESET_OTP_TTL_MINUTES", "12")
	t.Setenv("PASSWORD_RESET_TOKEN_TTL_MINUTES", "18")
	t.Setenv("PASSWORD_RESET_REQUEST_COOLDOWN_SECONDS", "90")
	t.Setenv("PASSWORD_RESET_CLEANUP_INTERVAL_HOURS", "8")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.PasswordReset.Pepper != "test-password-reset-pepper-at-least-32-bytes-long" {
		t.Error("PasswordReset.Pepper does not match configured value")
	}
	if loaded.PasswordReset.OTPTTL != 12*time.Minute {
		t.Errorf("PasswordReset.OTPTTL = %v, want %v", loaded.PasswordReset.OTPTTL, 12*time.Minute)
	}
	if loaded.PasswordReset.TokenTTL != 18*time.Minute {
		t.Errorf("PasswordReset.TokenTTL = %v, want %v", loaded.PasswordReset.TokenTTL, 18*time.Minute)
	}
	if loaded.PasswordReset.RequestCooldown != 90*time.Second {
		t.Errorf("PasswordReset.RequestCooldown = %v, want %v", loaded.PasswordReset.RequestCooldown, 90*time.Second)
	}
	if loaded.PasswordReset.CleanupInterval != 8*time.Hour {
		t.Errorf("PasswordReset.CleanupInterval = %v, want %v", loaded.PasswordReset.CleanupInterval, 8*time.Hour)
	}
}

func TestLoadPasswordResetDefaults(t *testing.T) {
	setRequiredSMTPEnv(t)
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
	t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
	t.Chdir(t.TempDir())

	for _, key := range []string{
		"PASSWORD_RESET_OTP_TTL_MINUTES",
		"PASSWORD_RESET_TOKEN_TTL_MINUTES",
		"PASSWORD_RESET_REQUEST_COOLDOWN_SECONDS",
		"PASSWORD_RESET_CLEANUP_INTERVAL_HOURS",
	} {
		originalValue, existed := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, originalValue)
				return
			}
			_ = os.Unsetenv(key)
		})
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.PasswordReset.OTPTTL != 10*time.Minute {
		t.Errorf("PasswordReset.OTPTTL = %v, want %v", loaded.PasswordReset.OTPTTL, 10*time.Minute)
	}
	if loaded.PasswordReset.TokenTTL != 15*time.Minute {
		t.Errorf("PasswordReset.TokenTTL = %v, want %v", loaded.PasswordReset.TokenTTL, 15*time.Minute)
	}
	if loaded.PasswordReset.RequestCooldown != 60*time.Second {
		t.Errorf("PasswordReset.RequestCooldown = %v, want %v", loaded.PasswordReset.RequestCooldown, 60*time.Second)
	}
	if loaded.PasswordReset.CleanupInterval != 24*time.Hour {
		t.Errorf("PasswordReset.CleanupInterval = %v, want %v", loaded.PasswordReset.CleanupInterval, 24*time.Hour)
	}
}

func TestLoadRejectsInvalidPasswordResetConfig(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "missing pepper", key: "PASSWORD_RESET_PEPPER", value: ""},
		{name: "short pepper", key: "PASSWORD_RESET_PEPPER", value: "too-short"},
		{name: "invalid OTP TTL", key: "PASSWORD_RESET_OTP_TTL_MINUTES", value: "abc"},
		{name: "zero token TTL", key: "PASSWORD_RESET_TOKEN_TTL_MINUTES", value: "0"},
		{name: "negative request cooldown", key: "PASSWORD_RESET_REQUEST_COOLDOWN_SECONDS", value: "-1"},
		{name: "zero cleanup interval", key: "PASSWORD_RESET_CLEANUP_INTERVAL_HOURS", value: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredSMTPEnv(t)
			t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
			t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
			t.Setenv("PASSWORD_RESET_OTP_TTL_MINUTES", "10")
			t.Setenv("PASSWORD_RESET_TOKEN_TTL_MINUTES", "15")
			t.Setenv("PASSWORD_RESET_REQUEST_COOLDOWN_SECONDS", "60")
			t.Setenv("PASSWORD_RESET_CLEANUP_INTERVAL_HOURS", "24")
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() returned nil error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error = %q, want key %q", err, tt.key)
			}
		})
	}
}

func TestLoadSMTPConfig(t *testing.T) {
	setRequiredSMTPEnv(t)
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
	t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "2525")
	t.Setenv("SMTP_USERNAME", "smtp-user")
	t.Setenv("SMTP_PASSWORD", "smtp-password")
	t.Setenv("SMTP_FROM", "noreply@example.com")
	t.Setenv("SMTP_REQUIRE_TLS", "false")
	t.Setenv("SMTP_TIMEOUT_SECONDS", "20")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.SMTP.Host != "smtp.example.com" || loaded.SMTP.Port != 2525 {
		t.Errorf("SMTP endpoint = %s:%d", loaded.SMTP.Host, loaded.SMTP.Port)
	}
	if loaded.SMTP.Username != "smtp-user" || loaded.SMTP.Password != "smtp-password" {
		t.Error("SMTP credentials do not match configured values")
	}
	if loaded.SMTP.From != "noreply@example.com" {
		t.Errorf("SMTP.From = %q", loaded.SMTP.From)
	}
	if loaded.SMTP.RequireTLS {
		t.Error("SMTP.RequireTLS = true, want false")
	}
	if loaded.SMTP.Timeout != 20*time.Second {
		t.Errorf("SMTP.Timeout = %v, want %v", loaded.SMTP.Timeout, 20*time.Second)
	}
}

func TestLoadRejectsInvalidSMTPConfig(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "missing host", key: "SMTP_HOST", value: ""},
		{name: "invalid port", key: "SMTP_PORT", value: "invalid"},
		{name: "port out of range", key: "SMTP_PORT", value: "70000"},
		{name: "missing from", key: "SMTP_FROM", value: ""},
		{name: "invalid TLS flag", key: "SMTP_REQUIRE_TLS", value: "sometimes"},
		{name: "zero timeout", key: "SMTP_TIMEOUT_SECONDS", value: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
			t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
			setRequiredSMTPEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() returned nil error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error = %q, want key %q", err, tt.key)
			}
		})
	}
}

func TestLoadRejectsPartialSMTPCredentials(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
	t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
	setRequiredSMTPEnv(t)
	t.Setenv("SMTP_USERNAME", "smtp-user")
	t.Setenv("SMTP_PASSWORD", "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() returned nil error")
	}
	if !strings.Contains(err.Error(), "SMTP_USERNAME") || !strings.Contains(err.Error(), "SMTP_PASSWORD") {
		t.Errorf("error = %q, want SMTP credential keys", err)
	}
}

func TestLoadOAuthConfig(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
	t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
	setRequiredSMTPEnv(t)
	t.Setenv("GOOGLE_CLIENT_ID", "google-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "google-client-secret")
	t.Setenv("GOOGLE_OAUTH_REDIRECT_URL", "https://api.example.com/auth/google/callback")
	t.Setenv("OAUTH_COOKIE_SECRET", "test-oauth-cookie-secret-at-least-32-bytes-long")
	t.Setenv("OAUTH_COOKIE_SECURE", "false")
	t.Setenv("OAUTH_FLOW_TTL_MINUTES", "12")
	t.Setenv("OAUTH_SUCCESS_REDIRECT_URL", "https://app.example.com/auth/callback")
	t.Setenv("OAUTH_LOGIN_CODE_TTL_SECONDS", "90")
	t.Setenv("OAUTH_CLEANUP_INTERVAL_MINUTES", "20")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.GoogleOAuth.ClientID != "google-client-id" ||
		loaded.GoogleOAuth.ClientSecret != "google-client-secret" ||
		loaded.GoogleOAuth.RedirectURL != "https://api.example.com/auth/google/callback" {
		t.Errorf("GoogleOAuth config = %+v", loaded.GoogleOAuth)
	}
	if loaded.OAuth.CookieSecret != "test-oauth-cookie-secret-at-least-32-bytes-long" {
		t.Error("OAuth.CookieSecret does not match configured value")
	}
	if loaded.OAuth.CookieSecure {
		t.Error("OAuth.CookieSecure = true, want false")
	}
	if loaded.OAuth.FlowTTL != 12*time.Minute {
		t.Errorf("OAuth.FlowTTL = %v, want %v", loaded.OAuth.FlowTTL, 12*time.Minute)
	}
	if loaded.OAuth.SuccessRedirectURL != "https://app.example.com/auth/callback" {
		t.Errorf("OAuth.SuccessRedirectURL = %q", loaded.OAuth.SuccessRedirectURL)
	}
	if loaded.OAuth.LoginCodeTTL != 90*time.Second {
		t.Errorf("OAuth.LoginCodeTTL = %v, want 90s", loaded.OAuth.LoginCodeTTL)
	}
	if loaded.OAuth.CleanupInterval != 20*time.Minute {
		t.Errorf("OAuth.CleanupInterval = %v, want 20m", loaded.OAuth.CleanupInterval)
	}
}

func TestLoadCORSConfig(t *testing.T) {
	setRequiredSMTPEnv(t)
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
	t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
	t.Setenv("FRONTEND_ORIGIN", "https://APP.Example.com/")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.CORS.FrontendOrigin != "https://app.example.com" {
		t.Errorf("CORS.FrontendOrigin = %q", loaded.CORS.FrontendOrigin)
	}
}

func TestLoadRejectsInvalidFrontendOrigin(t *testing.T) {
	for _, origin := range []string{"", "/relative", "ftp://example.com", "https://example.com/path", "https://example.com?query=1"} {
		t.Run(origin, func(t *testing.T) {
			setRequiredSMTPEnv(t)
			t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
			t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
			t.Setenv("FRONTEND_ORIGIN", origin)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "FRONTEND_ORIGIN") {
				t.Errorf("Load() error = %v, want FRONTEND_ORIGIN error", err)
			}
		})
	}
}

func TestLoadRejectsInvalidOAuthConfig(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "missing client ID", key: "GOOGLE_CLIENT_ID", value: ""},
		{name: "missing client secret", key: "GOOGLE_CLIENT_SECRET", value: ""},
		{name: "missing redirect URL", key: "GOOGLE_OAUTH_REDIRECT_URL", value: ""},
		{name: "relative redirect URL", key: "GOOGLE_OAUTH_REDIRECT_URL", value: "/auth/google/callback"},
		{name: "unsupported redirect scheme", key: "GOOGLE_OAUTH_REDIRECT_URL", value: "ftp://example.com/callback"},
		{name: "short cookie secret", key: "OAUTH_COOKIE_SECRET", value: "too-short"},
		{name: "invalid secure flag", key: "OAUTH_COOKIE_SECURE", value: "sometimes"},
		{name: "zero flow TTL", key: "OAUTH_FLOW_TTL_MINUTES", value: "0"},
		{name: "missing success redirect", key: "OAUTH_SUCCESS_REDIRECT_URL", value: ""},
		{name: "relative success redirect", key: "OAUTH_SUCCESS_REDIRECT_URL", value: "/auth/callback"},
		{name: "unsupported success redirect scheme", key: "OAUTH_SUCCESS_REDIRECT_URL", value: "ftp://example.com/callback"},
		{name: "zero login code TTL", key: "OAUTH_LOGIN_CODE_TTL_SECONDS", value: "0"},
		{name: "zero cleanup interval", key: "OAUTH_CLEANUP_INTERVAL_MINUTES", value: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
			t.Setenv("PASSWORD_RESET_PEPPER", "test-password-reset-pepper-at-least-32-bytes-long")
			setRequiredSMTPEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() returned nil error")
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Errorf("error = %q, want key %q", err, tt.key)
			}
		})
	}
}

func setRequiredSMTPEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USERNAME", "")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("SMTP_FROM", "noreply@example.com")
	t.Setenv("SMTP_REQUIRE_TLS", "true")
	t.Setenv("SMTP_TIMEOUT_SECONDS", "10")
	t.Setenv("GOOGLE_CLIENT_ID", "test-google-client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "test-google-client-secret")
	t.Setenv("GOOGLE_OAUTH_REDIRECT_URL", "http://localhost:8080/auth/google/callback")
	t.Setenv("OAUTH_COOKIE_SECRET", "test-oauth-cookie-secret-at-least-32-bytes-long")
	t.Setenv("OAUTH_COOKIE_SECURE", "false")
	t.Setenv("OAUTH_FLOW_TTL_MINUTES", "10")
	t.Setenv("OAUTH_SUCCESS_REDIRECT_URL", "http://localhost:3000/auth/callback")
	t.Setenv("OAUTH_LOGIN_CODE_TTL_SECONDS", "120")
	t.Setenv("OAUTH_CLEANUP_INTERVAL_MINUTES", "30")
	t.Setenv("FRONTEND_ORIGIN", "http://localhost:3000")
	t.Setenv("REFRESH_COOKIE_SECURE", "false")
}

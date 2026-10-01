package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadTokenDurations(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
	t.Setenv("ACCESS_TOKEN_TTL_MINUTES", "20")
	t.Setenv("REFRESH_TOKEN_TTL_HOURS", "48")
	t.Setenv("REFRESH_TOKEN_CLEANUP_INTERVAL_HOURS", "6")

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", "test-secret-that-is-at-least-32-bytes-long")
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

package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Database struct {
		URL string
	}
	API struct {
		Port int
	}
	SMTP struct {
		Host     string
		Port     string
		Username string
		Password string
		From     string
	}
	GoogleConnect struct {
		ClientID     string
		ClientSecret string
	}
	OAuth struct {
		RedirectURL                  string
		GoogleIntegrationRedirectURL string
	}
	JWT struct {
		Secret          string
		AccessTTL       time.Duration
		RefreshTTL      time.Duration
		CleanupInterval time.Duration
	}
	EncryptionKey string
	Environment   string
}

var cfg *Config

func Load() (*Config, error) {
	if err := godotenv.Load(); err != nil {
		if _, statErr := os.Stat(".env"); statErr == nil {
			return nil, fmt.Errorf("found .env but failed to load it: %w", err)
		}
	}

	port, err := getEnvInt("API_PORT", 8080)
	if err != nil {
		return nil, fmt.Errorf("invalid API_PORT: %w", err)
	}
	jwtSecret := getEnv("JWT_SECRET", "")
	if len([]byte(jwtSecret)) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must contain at least 32 bytes")
	}
	accessTTL, err := getEnvDuration("ACCESS_TOKEN_TTL_MINUTES", 15, "m")
	if err != nil {
		return nil, fmt.Errorf("invalid ACCESS_TOKEN_TTL_MINUTES: %w", err)
	}
	refreshTTL, err := getEnvDuration("REFRESH_TOKEN_TTL_HOURS", 720, "h")
	if err != nil {
		return nil, fmt.Errorf("invalid REFRESH_TOKEN_TTL_HOURS: %w", err)
	}
	cleanupInterval, err := getEnvDuration("REFRESH_TOKEN_CLEANUP_INTERVAL_HOURS", 24, "h")
	if err != nil {
		return nil, fmt.Errorf("invalid REFRESH_TOKEN_CLEANUP_INTERVAL_HOURS: %w", err)
	}

	cfg = &Config{
		Database: struct {
			URL string
		}{
			URL: getEnv("DATABASE_URL", ""),
		},
		API: struct {
			Port int
		}{
			Port: port,
		},
		JWT: struct {
			Secret          string
			AccessTTL       time.Duration
			RefreshTTL      time.Duration
			CleanupInterval time.Duration
		}{
			Secret:          jwtSecret,
			AccessTTL:       accessTTL,
			RefreshTTL:      refreshTTL,
			CleanupInterval: cleanupInterval,
		},
		Environment: getEnv("ENV", "development"),
	}

	return cfg, nil
}

func Get() *Config {
	return cfg
}

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) (int, error) {
	val := getEnv(key, "")
	if val == "" {
		return defaultVal, nil
	}
	intVal, err := strconv.Atoi(val)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a valid integer", key, val)
	}
	return intVal, nil
}

func getEnvDuration(key string, defaultValue int, unit string) (time.Duration, error) {
	raw := getEnv(key, strconv.Itoa(defaultValue))
	duration, err := time.ParseDuration(raw + unit)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a valid duration: %w", key, raw, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return duration, nil
}

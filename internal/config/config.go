package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	CORS struct {
		FrontendOrigin string
	}
	SMTP struct {
		Host       string
		Port       int
		Username   string
		Password   string
		From       string
		RequireTLS bool
		Timeout    time.Duration
	}
	GoogleOAuth struct {
		ClientID     string
		ClientSecret string
		RedirectURL  string
	}
	OAuth struct {
		CookieSecret       string
		CookieSecure       bool
		FlowTTL            time.Duration
		SuccessRedirectURL string
		LoginCodeTTL       time.Duration
		CleanupInterval    time.Duration
	}
	JWT struct {
		Secret              string
		AccessTTL           time.Duration
		RefreshTTL          time.Duration
		CleanupInterval     time.Duration
		RefreshCookieSecure bool
	}
	PasswordReset struct {
		Pepper          string
		OTPTTL          time.Duration
		TokenTTL        time.Duration
		RequestCooldown time.Duration
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
	refreshCookieSecure, err := getEnvBool("REFRESH_COOKIE_SECURE", true)
	if err != nil {
		return nil, fmt.Errorf("invalid REFRESH_COOKIE_SECURE: %w", err)
	}
	passwordResetPepper := getEnv("PASSWORD_RESET_PEPPER", "")
	if len([]byte(passwordResetPepper)) < 32 {
		return nil, fmt.Errorf("PASSWORD_RESET_PEPPER must contain at least 32 bytes")
	}
	passwordResetOTPTTL, err := getEnvDuration("PASSWORD_RESET_OTP_TTL_MINUTES", 10, "m")
	if err != nil {
		return nil, fmt.Errorf("invalid PASSWORD_RESET_OTP_TTL_MINUTES: %w", err)
	}
	passwordResetTokenTTL, err := getEnvDuration("PASSWORD_RESET_TOKEN_TTL_MINUTES", 15, "m")
	if err != nil {
		return nil, fmt.Errorf("invalid PASSWORD_RESET_TOKEN_TTL_MINUTES: %w", err)
	}
	passwordResetRequestCooldown, err := getEnvDuration("PASSWORD_RESET_REQUEST_COOLDOWN_SECONDS", 60, "s")
	if err != nil {
		return nil, fmt.Errorf("invalid PASSWORD_RESET_REQUEST_COOLDOWN_SECONDS: %w", err)
	}
	passwordResetCleanupInterval, err := getEnvDuration("PASSWORD_RESET_CLEANUP_INTERVAL_HOURS", 24, "h")
	if err != nil {
		return nil, fmt.Errorf("invalid PASSWORD_RESET_CLEANUP_INTERVAL_HOURS: %w", err)
	}
	smtpHost := strings.TrimSpace(getEnv("SMTP_HOST", ""))
	if smtpHost == "" {
		return nil, fmt.Errorf("SMTP_HOST is required")
	}
	smtpPort, err := getEnvInt("SMTP_PORT", 587)
	if err != nil || smtpPort < 1 || smtpPort > 65535 {
		return nil, fmt.Errorf("invalid SMTP_PORT: must be an integer from 1 to 65535")
	}
	smtpUsername := getEnv("SMTP_USERNAME", "")
	smtpPassword := getEnv("SMTP_PASSWORD", "")
	if (smtpUsername == "") != (smtpPassword == "") {
		return nil, fmt.Errorf("SMTP_USERNAME and SMTP_PASSWORD must either both be set or both be empty")
	}
	smtpFrom := strings.TrimSpace(getEnv("SMTP_FROM", ""))
	if smtpFrom == "" {
		return nil, fmt.Errorf("SMTP_FROM is required")
	}
	smtpRequireTLS, err := getEnvBool("SMTP_REQUIRE_TLS", true)
	if err != nil {
		return nil, fmt.Errorf("invalid SMTP_REQUIRE_TLS: %w", err)
	}
	smtpTimeout, err := getEnvDuration("SMTP_TIMEOUT_SECONDS", 10, "s")
	if err != nil {
		return nil, fmt.Errorf("invalid SMTP_TIMEOUT_SECONDS: %w", err)
	}
	googleClientID := strings.TrimSpace(getEnv("GOOGLE_CLIENT_ID", ""))
	if googleClientID == "" {
		return nil, fmt.Errorf("GOOGLE_CLIENT_ID is required")
	}
	googleClientSecret := getEnv("GOOGLE_CLIENT_SECRET", "")
	if googleClientSecret == "" {
		return nil, fmt.Errorf("GOOGLE_CLIENT_SECRET is required")
	}
	googleRedirectURL := strings.TrimSpace(getEnv("GOOGLE_OAUTH_REDIRECT_URL", ""))
	parsedGoogleRedirectURL, err := url.Parse(googleRedirectURL)
	if err != nil || parsedGoogleRedirectURL.Scheme == "" || parsedGoogleRedirectURL.Host == "" {
		return nil, fmt.Errorf("GOOGLE_OAUTH_REDIRECT_URL must be an absolute URL")
	}
	if parsedGoogleRedirectURL.Scheme != "http" && parsedGoogleRedirectURL.Scheme != "https" {
		return nil, fmt.Errorf("GOOGLE_OAUTH_REDIRECT_URL must use http or https")
	}
	oauthCookieSecret := getEnv("OAUTH_COOKIE_SECRET", "")
	if len([]byte(oauthCookieSecret)) < 32 {
		return nil, fmt.Errorf("OAUTH_COOKIE_SECRET must contain at least 32 bytes")
	}
	oauthCookieSecure, err := getEnvBool("OAUTH_COOKIE_SECURE", true)
	if err != nil {
		return nil, fmt.Errorf("invalid OAUTH_COOKIE_SECURE: %w", err)
	}
	oauthFlowTTL, err := getEnvDuration("OAUTH_FLOW_TTL_MINUTES", 10, "m")
	if err != nil {
		return nil, fmt.Errorf("invalid OAUTH_FLOW_TTL_MINUTES: %w", err)
	}
	oauthSuccessRedirectURL := strings.TrimSpace(getEnv("OAUTH_SUCCESS_REDIRECT_URL", ""))
	parsedOAuthSuccessRedirectURL, err := url.Parse(oauthSuccessRedirectURL)
	if err != nil || parsedOAuthSuccessRedirectURL.Scheme == "" || parsedOAuthSuccessRedirectURL.Host == "" {
		return nil, fmt.Errorf("OAUTH_SUCCESS_REDIRECT_URL must be an absolute URL")
	}
	if parsedOAuthSuccessRedirectURL.Scheme != "http" && parsedOAuthSuccessRedirectURL.Scheme != "https" {
		return nil, fmt.Errorf("OAUTH_SUCCESS_REDIRECT_URL must use http or https")
	}
	oauthLoginCodeTTL, err := getEnvDuration("OAUTH_LOGIN_CODE_TTL_SECONDS", 120, "s")
	if err != nil {
		return nil, fmt.Errorf("invalid OAUTH_LOGIN_CODE_TTL_SECONDS: %w", err)
	}
	oauthCleanupInterval, err := getEnvDuration("OAUTH_CLEANUP_INTERVAL_MINUTES", 30, "m")
	if err != nil {
		return nil, fmt.Errorf("invalid OAUTH_CLEANUP_INTERVAL_MINUTES: %w", err)
	}
	frontendOrigin := strings.TrimSpace(getEnv("FRONTEND_ORIGIN", ""))
	parsedFrontendOrigin, err := url.Parse(frontendOrigin)
	if err != nil || parsedFrontendOrigin.Scheme == "" || parsedFrontendOrigin.Host == "" {
		return nil, fmt.Errorf("FRONTEND_ORIGIN must be an absolute origin")
	}
	if parsedFrontendOrigin.Scheme != "http" && parsedFrontendOrigin.Scheme != "https" {
		return nil, fmt.Errorf("FRONTEND_ORIGIN must use http or https")
	}
	if parsedFrontendOrigin.User != nil ||
		(parsedFrontendOrigin.Path != "" && parsedFrontendOrigin.Path != "/") ||
		parsedFrontendOrigin.RawQuery != "" || parsedFrontendOrigin.Fragment != "" {
		return nil, fmt.Errorf("FRONTEND_ORIGIN must not contain credentials, a path, query, or fragment")
	}
	frontendOrigin = strings.ToLower(parsedFrontendOrigin.Scheme) + "://" + strings.ToLower(parsedFrontendOrigin.Host)

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
		CORS: struct {
			FrontendOrigin string
		}{
			FrontendOrigin: frontendOrigin,
		},
		JWT: struct {
			Secret              string
			AccessTTL           time.Duration
			RefreshTTL          time.Duration
			CleanupInterval     time.Duration
			RefreshCookieSecure bool
		}{
			Secret:              jwtSecret,
			AccessTTL:           accessTTL,
			RefreshTTL:          refreshTTL,
			CleanupInterval:     cleanupInterval,
			RefreshCookieSecure: refreshCookieSecure,
		},
		PasswordReset: struct {
			Pepper          string
			OTPTTL          time.Duration
			TokenTTL        time.Duration
			RequestCooldown time.Duration
			CleanupInterval time.Duration
		}{
			Pepper:          passwordResetPepper,
			OTPTTL:          passwordResetOTPTTL,
			TokenTTL:        passwordResetTokenTTL,
			RequestCooldown: passwordResetRequestCooldown,
			CleanupInterval: passwordResetCleanupInterval,
		},
		SMTP: struct {
			Host       string
			Port       int
			Username   string
			Password   string
			From       string
			RequireTLS bool
			Timeout    time.Duration
		}{
			Host:       smtpHost,
			Port:       smtpPort,
			Username:   smtpUsername,
			Password:   smtpPassword,
			From:       smtpFrom,
			RequireTLS: smtpRequireTLS,
			Timeout:    smtpTimeout,
		},
		GoogleOAuth: struct {
			ClientID     string
			ClientSecret string
			RedirectURL  string
		}{
			ClientID:     googleClientID,
			ClientSecret: googleClientSecret,
			RedirectURL:  googleRedirectURL,
		},
		OAuth: struct {
			CookieSecret       string
			CookieSecure       bool
			FlowTTL            time.Duration
			SuccessRedirectURL string
			LoginCodeTTL       time.Duration
			CleanupInterval    time.Duration
		}{
			CookieSecret:       oauthCookieSecret,
			CookieSecure:       oauthCookieSecure,
			FlowTTL:            oauthFlowTTL,
			SuccessRedirectURL: oauthSuccessRedirectURL,
			LoginCodeTTL:       oauthLoginCodeTTL,
			CleanupInterval:    oauthCleanupInterval,
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

func getEnvBool(key string, defaultValue bool) (bool, error) {
	raw := getEnv(key, strconv.FormatBool(defaultValue))
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s=%q is not a valid boolean", key, raw)
	}
	return value, nil
}

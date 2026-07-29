package config

import (
	"fmt"
	"os"
	"strconv"

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
		Secret string
	}
	EncryptionKey   string
	Environment     string
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
 
	cfg = &Config{
		Database: struct {
			URL string
		}{
			URL : getEnv("DATABASE_URL", ""),
		},
		API: struct {
			Port int
		}{
			Port: port,
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


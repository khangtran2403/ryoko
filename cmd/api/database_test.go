package main

import (
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/config"
)

func TestNewDatabasePoolConfigAppliesSettings(t *testing.T) {
	database := config.DatabaseConfig{
		URL:               "postgres://user:password@localhost:5432/ryoko?sslmode=disable",
		MaxConns:          25,
		MinConns:          3,
		MaxConnLifetime:   90 * time.Minute,
		MaxConnIdleTime:   20 * time.Minute,
		HealthCheckPeriod: 45 * time.Second,
	}

	poolConfig, err := newDatabasePoolConfig(database)
	if err != nil {
		t.Fatalf("newDatabasePoolConfig() error = %v", err)
	}
	if poolConfig.MaxConns != database.MaxConns {
		t.Errorf("MaxConns = %d, want %d", poolConfig.MaxConns, database.MaxConns)
	}
	if poolConfig.MinConns != database.MinConns {
		t.Errorf("MinConns = %d, want %d", poolConfig.MinConns, database.MinConns)
	}
	if poolConfig.MaxConnLifetime != database.MaxConnLifetime {
		t.Errorf("MaxConnLifetime = %v, want %v", poolConfig.MaxConnLifetime, database.MaxConnLifetime)
	}
	if poolConfig.MaxConnIdleTime != database.MaxConnIdleTime {
		t.Errorf("MaxConnIdleTime = %v, want %v", poolConfig.MaxConnIdleTime, database.MaxConnIdleTime)
	}
	if poolConfig.HealthCheckPeriod != database.HealthCheckPeriod {
		t.Errorf("HealthCheckPeriod = %v, want %v", poolConfig.HealthCheckPeriod, database.HealthCheckPeriod)
	}
}

func TestNewDatabasePoolConfigRejectsInvalidURL(t *testing.T) {
	_, err := newDatabasePoolConfig(config.DatabaseConfig{URL: "://invalid"})
	if err == nil {
		t.Fatal("newDatabasePoolConfig() returned nil error")
	}
}

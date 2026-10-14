package handler

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/khangtran2403/ryoko/internal/middleware"
)

type databasePinger interface {
	Ping(ctx context.Context) error
}

type HealthHandler struct {
	database databasePinger
	timeout  time.Duration
	logger   *slog.Logger
}

func NewHealthHandler(database databasePinger, timeout time.Duration, logger *slog.Logger) *HealthHandler {
	if database == nil {
		panic("health database pinger must not be nil")
	}
	if timeout <= 0 {
		panic("health readiness timeout must be positive")
	}
	if logger == nil {
		panic("health logger must not be nil")
	}
	return &HealthHandler{database: database, timeout: timeout, logger: logger}
}

// Liveness reports whether the API process can serve HTTP requests. It must
// not depend on PostgreSQL or another downstream service.
func (h *HealthHandler) Liveness(w http.ResponseWriter, _ *http.Request) {
	writeHealthStatus(w, http.StatusOK, "alive")
}

// Readiness reports whether the API can currently reach PostgreSQL and should
// receive traffic.
func (h *HealthHandler) Readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	if err := h.database.Ping(ctx); err != nil {
		requestID, _ := middleware.RequestIDFromContext(r.Context())
		h.logger.ErrorContext(
			r.Context(),
			"database readiness check failed",
			slog.String("request_id", requestID),
			slog.Any("error", err),
		)
		writeHealthStatus(w, http.StatusServiceUnavailable, "not ready")
		return
	}

	writeHealthStatus(w, http.StatusOK, "ready")
}

func writeHealthStatus(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message + "\n"))
}

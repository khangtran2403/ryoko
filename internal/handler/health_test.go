package handler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/khangtran2403/ryoko/internal/middleware"
)

type fakeHealthDatabase struct {
	pingCalled bool
	ping       func(context.Context) error
}

func (f *fakeHealthDatabase) Ping(ctx context.Context) error {
	f.pingCalled = true
	if f.ping != nil {
		return f.ping(ctx)
	}
	return nil
}

func TestHealthLivenessDoesNotPingDatabase(t *testing.T) {
	database := &fakeHealthDatabase{}
	handler := NewHealthHandler(database, time.Second, discardTestLogger())
	recorder := httptest.NewRecorder()

	handler.Liveness(recorder, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	assertHealthResponse(t, recorder, http.StatusOK, "alive\n")
	if database.pingCalled {
		t.Error("liveness check pinged the database")
	}
}

func TestHealthReadinessSucceedsWhenDatabaseResponds(t *testing.T) {
	database := &fakeHealthDatabase{}
	handler := NewHealthHandler(database, time.Second, discardTestLogger())
	recorder := httptest.NewRecorder()

	handler.Readiness(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	assertHealthResponse(t, recorder, http.StatusOK, "ready\n")
	if !database.pingCalled {
		t.Error("readiness check did not ping the database")
	}
}

func TestHealthReadinessReturnsGenericFailureAndLogsCause(t *testing.T) {
	var logs bytes.Buffer
	database := &fakeHealthDatabase{ping: func(context.Context) error {
		return errors.New("database host secret.internal is unavailable")
	}}
	handler := NewHealthHandler(
		database,
		time.Second,
		slog.New(slog.NewJSONHandler(&logs, nil)),
	)
	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set("X-Request-ID", "health-request-1")
	recorder := httptest.NewRecorder()

	middleware.RequestID(http.HandlerFunc(handler.Readiness)).ServeHTTP(recorder, request)

	assertHealthResponse(t, recorder, http.StatusServiceUnavailable, "not ready\n")
	if strings.Contains(recorder.Body.String(), "secret.internal") {
		t.Error("readiness response leaked the database error")
	}
	if !strings.Contains(logs.String(), "database host secret.internal is unavailable") ||
		!strings.Contains(logs.String(), "health-request-1") {
		t.Errorf("readiness log = %q, want database cause and request ID", logs.String())
	}
}

func TestHealthReadinessTimesOut(t *testing.T) {
	const timeout = 10 * time.Millisecond
	database := &fakeHealthDatabase{ping: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	handler := NewHealthHandler(database, timeout, discardTestLogger())
	recorder := httptest.NewRecorder()
	startedAt := time.Now()

	handler.Readiness(recorder, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	assertHealthResponse(t, recorder, http.StatusServiceUnavailable, "not ready\n")
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Errorf("readiness timeout took %v, want under 500ms", elapsed)
	}
}

func TestNewHealthHandlerRejectsInvalidDependencies(t *testing.T) {
	tests := []struct {
		name     string
		database databasePinger
		timeout  time.Duration
		logger   *slog.Logger
	}{
		{name: "nil database", timeout: time.Second, logger: discardTestLogger()},
		{name: "invalid timeout", database: &fakeHealthDatabase{}, logger: discardTestLogger()},
		{name: "nil logger", database: &fakeHealthDatabase{}, timeout: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("NewHealthHandler() did not panic")
				}
			}()
			NewHealthHandler(tt.database, tt.timeout, tt.logger)
		})
	}
}

func assertHealthResponse(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantBody string) {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Errorf("status = %d, want %d", recorder.Code, wantStatus)
	}
	if got := recorder.Body.String(); got != wantBody {
		t.Errorf("body = %q, want %q", got, wantBody)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
}

func discardTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

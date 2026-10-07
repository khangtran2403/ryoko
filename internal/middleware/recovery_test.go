package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverPanicReturnsGenericErrorAndLogsDetails(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := RequestID(RecoverPanic(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("database password must stay private")
	})))
	request := httptest.NewRequest(http.MethodPost, "/bookings?token=secret", nil)
	request.Header.Set(requestIDHeader, "request-456")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if got := recorder.Body.String(); got != "Internal Server Error\n" {
		t.Errorf("body = %q, want generic error", got)
	}
	if strings.Contains(recorder.Body.String(), "database password") {
		t.Error("response leaked panic details")
	}

	record := decodeAccessLogRecord(t, &output)
	assertLogValue(t, record, "level", "ERROR")
	assertLogValue(t, record, "msg", "HTTP handler panic")
	assertLogValue(t, record, "request_id", "request-456")
	assertLogValue(t, record, "method", http.MethodPost)
	assertLogValue(t, record, "path", "/bookings")
	assertLogValue(t, record, "panic", "database password must stay private")
	assertLogValue(t, record, "response_committed", false)
	stack, ok := record["stack"].(string)
	if !ok || !strings.Contains(stack, "TestRecoverPanicReturnsGenericErrorAndLogsDetails") {
		t.Error("log record does not contain the panic stack")
	}
	if strings.Contains(output.String(), "token=secret") {
		t.Error("log record leaked query parameters")
	}
}

func TestRecoverPanicDoesNotRewriteCommittedResponse(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := RecoverPanic(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		if _, err := w.Write([]byte("partial")); err != nil {
			t.Fatalf("write response: %v", err)
		}
		panic("too late")
	}))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stream", nil))

	if recorder.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusAccepted)
	}
	if got := recorder.Body.String(); got != "partial" {
		t.Errorf("body = %q, want existing partial response", got)
	}
	record := decodeAccessLogRecord(t, &output)
	assertLogValue(t, record, "response_committed", true)
}

func TestRecoverPanicPassesThroughNormalResponse(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := RecoverPanic(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	if recorder.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if output.Len() != 0 {
		t.Errorf("normal request produced recovery log: %s", output.String())
	}
}

func TestRecoverPanicRepanicsAbortHandler(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := RecoverPanic(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	defer func() {
		if recovered := recover(); recovered != http.ErrAbortHandler {
			t.Fatalf("recovered value = %#v, want http.ErrAbortHandler", recovered)
		}
		if output.Len() != 0 {
			t.Errorf("ErrAbortHandler produced recovery log: %s", output.String())
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestRecoverPanicPanicsWithNilLogger(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RecoverPanic(nil, ...) did not panic")
		}
	}()
	RecoverPanic(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

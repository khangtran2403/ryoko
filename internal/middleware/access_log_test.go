package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessLogRecordsCompletedRequest(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Fatalf("write response: %v", err)
		}
	})
	handler := RequestID(AccessLog(logger, next))
	request := httptest.NewRequest(http.MethodPost, "/hotels/search?city=Da+Nang", nil)
	request.RemoteAddr = "203.0.113.8:54321"
	request.Header.Set(requestIDHeader, "request-123")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	record := decodeAccessLogRecord(t, &output)
	assertLogValue(t, record, "msg", "HTTP request")
	assertLogValue(t, record, "request_id", "request-123")
	assertLogValue(t, record, "method", http.MethodPost)
	assertLogValue(t, record, "path", "/hotels/search")
	assertLogValue(t, record, "status", float64(http.StatusCreated))
	assertLogValue(t, record, "response_bytes", float64(len("hello")))
	assertLogValue(t, record, "client_ip", "203.0.113.8")
	if _, ok := record["duration"]; !ok {
		t.Error("log record does not contain duration")
	}
	if _, ok := record["query"]; ok {
		t.Error("log record unexpectedly contains query parameters")
	}
}

func TestAccessLogRecordsImplicitOKAndGeneratedRequestID(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := RequestID(AccessLog(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("OK")); err != nil {
			t.Fatalf("write response: %v", err)
		}
	})))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	record := decodeAccessLogRecord(t, &output)
	assertLogValue(t, record, "status", float64(http.StatusOK))
	assertLogValue(t, record, "response_bytes", float64(2))
	requestID, ok := record["request_id"].(string)
	if !ok || !uuidV4Pattern.MatchString(requestID) {
		t.Errorf("logged request ID = %q, want generated UUIDv4", requestID)
	}
	if got := recorder.Header().Get(requestIDHeader); got != requestID {
		t.Errorf("response request ID = %q, logged request ID = %q", got, requestID)
	}
}

func TestAccessLogPreservesFirstStatusCode(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := RequestID(AccessLog(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.WriteHeader(http.StatusInternalServerError)
	})))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	record := decodeAccessLogRecord(t, &output)
	assertLogValue(t, record, "status", float64(http.StatusAccepted))
}

func TestAccessLogPanicsWithNilLogger(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("AccessLog(nil, ...) did not panic")
		}
	}()
	AccessLog(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}

func decodeAccessLogRecord(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatalf("decode access log %q: %v", output.String(), err)
	}
	return record
}

func assertLogValue(t *testing.T, record map[string]any, key string, want any) {
	t.Helper()
	if got := record[key]; got != want {
		t.Errorf("log field %q = %#v, want %#v", key, got, want)
	}
}

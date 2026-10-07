package middleware

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var uuidV4Pattern = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

func TestRequestIDPreservesValidIncomingID(t *testing.T) {
	const incomingID = "gateway-request_123.abc:1"

	var contextID string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		contextID, ok = RequestIDFromContext(r.Context())
		if !ok {
			t.Fatal("request ID is missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set(requestIDHeader, incomingID)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get(requestIDHeader); got != incomingID {
		t.Errorf("response request ID = %q, want %q", got, incomingID)
	}
	if contextID != incomingID {
		t.Errorf("context request ID = %q, want %q", contextID, incomingID)
	}
}

func TestRequestIDGeneratesUUIDWhenHeaderIsMissingOrInvalid(t *testing.T) {
	invalidValues := []string{
		"",
		"contains a space",
		"contains/slash",
		strings.Repeat("a", maxRequestIDLength+1),
	}

	for _, value := range invalidValues {
		name := value
		if name == "" {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			var contextID string
			handler := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				contextID, _ = RequestIDFromContext(r.Context())
			}))
			request := httptest.NewRequest(http.MethodGet, "/health", nil)
			if value != "" {
				request.Header.Set(requestIDHeader, value)
			}
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			responseID := recorder.Header().Get(requestIDHeader)
			if !uuidV4Pattern.MatchString(responseID) {
				t.Errorf("generated request ID = %q, want UUIDv4", responseID)
			}
			if contextID != responseID {
				t.Errorf("context request ID = %q, response request ID = %q", contextID, responseID)
			}
		})
	}
}

func TestRequestIDGeneratesDifferentIDs(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))

	firstID := first.Header().Get(requestIDHeader)
	secondID := second.Header().Get(requestIDHeader)
	if firstID == secondID {
		t.Fatalf("generated duplicate request IDs: %q", firstID)
	}
}

func TestRequestIDFromContextReturnsFalseWithoutMiddleware(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if requestID, ok := RequestIDFromContext(request.Context()); ok || requestID != "" {
		t.Fatalf("RequestIDFromContext() = %q, %v; want empty, false", requestID, ok)
	}
}

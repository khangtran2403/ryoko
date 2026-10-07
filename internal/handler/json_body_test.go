package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRequestAcceptsBodyWithinLimit(t *testing.T) {
	var destination struct {
		Name string `json:"name"`
	}
	request := httptest.NewRequest(http.MethodPost, "/hotels", strings.NewReader(`{"name":"Ryoko"}`))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	recorder := httptest.NewRecorder()

	ok := decodeJSONRequest(recorder, request, &destination, false)

	if !ok {
		t.Fatalf("decodeJSONRequest() = false, status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if destination.Name != "Ryoko" {
		t.Errorf("decoded name = %q, want Ryoko", destination.Name)
	}
}

func TestDecodeJSONRequestRejectsKnownOversizedBody(t *testing.T) {
	body := strings.Repeat("x", int(maxJSONRequestBodyBytes)+1)
	request := httptest.NewRequest(http.MethodPost, "/hotels", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	ok := decodeJSONRequest(recorder, request, &struct{}{}, false)

	if ok {
		t.Fatal("decodeJSONRequest() = true, want false")
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestDecodeJSONRequestRejectsChunkedOversizedBody(t *testing.T) {
	body := `{"value":"` + strings.Repeat("x", int(maxJSONRequestBodyBytes)) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/hotels", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.ContentLength = -1
	recorder := httptest.NewRecorder()

	ok := decodeJSONRequest(recorder, request, &struct{}{}, false)

	if ok {
		t.Fatal("decodeJSONRequest() = true, want false")
	}
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestDecodeJSONRequestRejectsMultipleValues(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/hotels", strings.NewReader(`{} {}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	ok := decodeJSONRequest(recorder, request, &struct{}{}, false)

	if ok {
		t.Fatal("decodeJSONRequest() = true, want false")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestDecodeJSONRequestPreservesStrictOption(t *testing.T) {
	for _, tt := range []struct {
		name   string
		strict bool
		wantOK bool
	}{
		{name: "lenient", strict: false, wantOK: true},
		{name: "strict", strict: true, wantOK: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"unknown":true}`))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			ok := decodeJSONRequest(recorder, request, &struct{}{}, tt.strict)
			if ok != tt.wantOK {
				t.Errorf("decodeJSONRequest() = %v, want %v", ok, tt.wantOK)
			}
		})
	}
}

func TestDecodeJSONRequestRejectsUnsupportedContentType(t *testing.T) {
	for _, contentType := range []string{"", "text/plain", "application/xml", "not a media type"} {
		name := contentType
		if name == "" {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
			if contentType != "" {
				request.Header.Set("Content-Type", contentType)
			}
			recorder := httptest.NewRecorder()

			ok := decodeJSONRequest(recorder, request, &struct{}{}, false)

			if ok {
				t.Fatal("decodeJSONRequest() = true, want false")
			}
			if recorder.Code != http.StatusUnsupportedMediaType {
				t.Errorf("status = %d, want %d", recorder.Code, http.StatusUnsupportedMediaType)
			}
		})
	}
}

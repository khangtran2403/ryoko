package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

const maxJSONRequestBodyBytes int64 = 64 << 10

func decodeJSONRequest(
	w http.ResponseWriter,
	r *http.Request,
	destination any,
	disallowUnknownFields bool,
) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}

	if r.ContentLength > maxJSONRequestBodyBytes {
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return false
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(destination); err != nil {
		writeJSONDecodeError(w, err)
		return false
	}

	// Require exactly one JSON value. Besides rejecting ambiguous requests, this
	// also makes MaxBytesReader inspect oversized trailing data for chunked bodies.
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeJSONDecodeError(w, err)
		return false
	}

	return true
}

func writeJSONDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "Invalid request body", http.StatusBadRequest)
}

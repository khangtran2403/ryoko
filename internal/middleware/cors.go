package middleware

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const (
	corsAllowedMethods = "GET, POST, PUT, DELETE, OPTIONS"
	corsAllowedHeaders = "Authorization, Content-Type, Idempotency-Key"
	corsMaxAge         = "600"
)

var (
	allowedCORSMethods = map[string]struct{}{
		http.MethodGet: {}, http.MethodPost: {}, http.MethodPut: {},
		http.MethodDelete: {}, http.MethodOptions: {},
	}
	allowedCORSHeaders = map[string]struct{}{
		"Authorization": {}, "Content-Type": {}, "Idempotency-Key": {},
	}
)

type CORSMiddleware struct {
	allowedOrigin string
}

func NewCORSMiddleware(allowedOrigin string) (*CORSMiddleware, error) {
	origin, err := normalizeOrigin(allowedOrigin)
	if err != nil {
		return nil, err
	}
	return &CORSMiddleware{allowedOrigin: origin}, nil
}

func (m *CORSMiddleware) Allow(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appendVary(w.Header(), "Origin")
		requestOrigin := strings.TrimSpace(r.Header.Get("Origin"))
		if requestOrigin == "" {
			next.ServeHTTP(w, r)
			return
		}

		normalizedRequestOrigin, err := normalizeOrigin(requestOrigin)
		if err != nil || normalizedRequestOrigin != m.allowedOrigin {
			http.Error(w, "CORS origin is not allowed", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", m.allowedOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method != http.MethodOptions || r.Header.Get("Access-Control-Request-Method") == "" {
			next.ServeHTTP(w, r)
			return
		}

		appendVary(w.Header(), "Access-Control-Request-Method")
		appendVary(w.Header(), "Access-Control-Request-Headers")
		requestedMethod := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
		if _, ok := allowedCORSMethods[requestedMethod]; !ok {
			http.Error(w, "CORS method is not allowed", http.StatusForbidden)
			return
		}
		if !corsHeadersAllowed(r.Header.Get("Access-Control-Request-Headers")) {
			http.Error(w, "CORS headers are not allowed", http.StatusForbidden)
			return
		}

		w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
		w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
		w.Header().Set("Access-Control-Max-Age", corsMaxAge)
		w.WriteHeader(http.StatusNoContent)
	})
}

// RequireAllowedOrigin protects cookie-authenticated state-changing endpoints
// from cross-site requests. Unlike Allow, absence of Origin is rejected.
func (m *CORSMiddleware) RequireAllowedOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, err := normalizeOrigin(r.Header.Get("Origin"))
		if err != nil || origin != m.allowedOrigin {
			http.Error(w, "Request origin is not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func normalizeOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("CORS origin must be an absolute origin")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("CORS origin must use http or https")
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("CORS origin must not contain credentials, a path, query, or fragment")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func corsHeadersAllowed(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	for _, header := range strings.Split(raw, ",") {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(header))
		if _, ok := allowedCORSHeaders[canonical]; !ok {
			return false
		}
	}
	return true
}

func appendVary(header http.Header, value string) {
	for _, line := range header.Values("Vary") {
		for _, existing := range strings.Split(line, ",") {
			if strings.EqualFold(strings.TrimSpace(existing), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

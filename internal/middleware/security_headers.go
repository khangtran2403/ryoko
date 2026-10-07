package middleware

import "net/http"

const (
	contentSecurityPolicy = "default-src 'none'; frame-ancestors 'none'"
	referrerPolicy        = "no-referrer"
)

// SecurityHeaders adds browser-facing protections that are safe for every API
// response.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("Referrer-Policy", referrerPolicy)
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "DENY")

		next.ServeHTTP(w, r)
	})
}

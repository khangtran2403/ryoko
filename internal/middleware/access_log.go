package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

type accessLogResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

// AccessLog records one structured log entry after each completed request.
func AccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		panic("access logger must not be nil")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		writer := &accessLogResponseWriter{ResponseWriter: w}

		next.ServeHTTP(writer, r)

		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		requestID, _ := RequestIDFromContext(r.Context())
		logger.InfoContext(
			r.Context(),
			"HTTP request",
			slog.String("request_id", requestID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.EscapedPath()),
			slog.Int("status", status),
			slog.Int("response_bytes", writer.bytes),
			slog.Duration("duration", time.Since(startedAt)),
			slog.String("client_ip", clientPeerIP(r.RemoteAddr)),
		)
	})
}

func (w *accessLogResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *accessLogResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach optional interfaces implemented by
// the original response writer, such as flushing and hijacking.
func (w *accessLogResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
)

type recoveryResponseWriter struct {
	http.ResponseWriter
	committed bool
}

// RecoverPanic prevents an application panic from terminating the HTTP
// connection. Panic details stay in server logs; clients receive only a
// generic error when the response has not already been committed.
func RecoverPanic(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		panic("recovery logger must not be nil")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer := &recoveryResponseWriter{ResponseWriter: w}

		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}

			requestID, _ := RequestIDFromContext(r.Context())
			logger.ErrorContext(
				r.Context(),
				"HTTP handler panic",
				slog.String("request_id", requestID),
				slog.String("method", r.Method),
				slog.String("path", r.URL.EscapedPath()),
				slog.String("panic", fmt.Sprint(recovered)),
				slog.Bool("response_committed", writer.committed),
				slog.String("stack", string(debug.Stack())),
			)

			if !writer.committed {
				http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(writer, r)
	})
}

func (w *recoveryResponseWriter) WriteHeader(status int) {
	if status >= 200 {
		w.committed = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *recoveryResponseWriter) Write(data []byte) (int, error) {
	w.committed = true
	return w.ResponseWriter.Write(data)
}

// Unwrap lets http.ResponseController reach the original response writer.
func (w *recoveryResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

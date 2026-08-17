package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/khangtran2403/ryoko/internal/auth"
)

type principalContextKey struct{}

type AuthMiddleware struct {
	tokens *auth.TokenManager
}

func NewAuthMiddleware(tokens *auth.TokenManager) *AuthMiddleware {
	return &AuthMiddleware{
		tokens: tokens,
	}
}

func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))

		if len(parts) != 2 ||
			!strings.EqualFold(parts[0], "Bearer") ||
			parts[1] == "" {
			unauthorized(w)
			return
		}

		principal, err := m.tokens.ParseToken(parts[1])
		if err != nil {
			unauthorized(w)
			return
		}

		ctx := context.WithValue(
			r.Context(),
			principalContextKey{},
			*principal,
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
func PrincipalFromContext(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(auth.Principal)
	return principal, ok
}
func RequireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			unauthorized(w)
			return
		}

		if principal.Role != role {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type contextKey string

const claimsKey contextKey = "auth_claims"

// Middleware extracts and validates the JWT from the Authorization header.
// On success, the Claims are stored in the request context.
//
// As a fallback for clients that cannot set custom headers (notably the SSE
// EventSource API), the middleware also accepts the token via the `token`
// query parameter. The Authorization header takes precedence when both are set.
func Middleware(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString := ""
			if authHeader := r.Header.Get("Authorization"); authHeader != "" {
				tokenString = strings.TrimPrefix(authHeader, "Bearer ")
				if tokenString == authHeader {
					http.Error(w, `{"error":"invalid authorization header"}`, http.StatusUnauthorized)
					return
				}
			} else if qsToken := r.URL.Query().Get("token"); qsToken != "" {
				tokenString = qsToken
			}

			if tokenString == "" {
				http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
				return
			}

			claims, err := ParseToken(secret, tokenString)
			if err != nil {
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRole returns middleware that checks the user has the required role.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := UserFromContext(r.Context())
			if claims == nil || claims.Role != role {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// UserFromContext returns the authenticated user's claims from the context, or nil.
func UserFromContext(ctx context.Context) *Claims {
	claims, _ := ctx.Value(claimsKey).(*Claims)
	return claims
}

// ContextWithClaims returns a copy of ctx with the given claims attached.
// Intended for tests that need to bypass the HTTP middleware.
func ContextWithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// UserIDFromContext returns the authenticated user's ID from the context.
func UserIDFromContext(ctx context.Context) uuid.UUID {
	claims := UserFromContext(ctx)
	if claims == nil {
		return uuid.Nil
	}
	return claims.UserID
}

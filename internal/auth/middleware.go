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
// On success, the Claims are stored in the request context. It does NOT accept
// the token via query string — putting a bearer credential in a URL leaks it
// into proxy access logs, browser history, and Referer headers. Endpoints that
// must support the header-less EventSource API use SSEMiddleware instead.
func Middleware(secret []byte) func(http.Handler) http.Handler {
	return authMiddleware(secret, false)
}

// SSEMiddleware is like Middleware but additionally accepts the token via the
// `token` query parameter, for the browser EventSource API which cannot set
// request headers. Restricted to the SSE GET endpoints so the URL-borne-token
// exposure stays scoped to exactly the routes that need it.
func SSEMiddleware(secret []byte) func(http.Handler) http.Handler {
	return authMiddleware(secret, true)
}

// bearerOrQueryToken extracts the JWT from the Authorization header
// ("Bearer <tok>"), falling back to the ?token= query parameter when
// allowQueryToken is set. ok is false only when an Authorization header was
// present but not a well-formed Bearer token.
func bearerOrQueryToken(r *http.Request, allowQueryToken bool) (token string, ok bool) {
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		t := strings.TrimPrefix(authHeader, "Bearer ")
		if t == authHeader {
			return "", false
		}
		return t, true
	}
	if allowQueryToken {
		return r.URL.Query().Get("token"), true
	}
	return "", true
}

func authMiddleware(secret []byte, allowQueryToken bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString, ok := bearerOrQueryToken(r, allowQueryToken)
			if !ok {
				http.Error(w, `{"error":"invalid authorization header"}`, http.StatusUnauthorized)
				return
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

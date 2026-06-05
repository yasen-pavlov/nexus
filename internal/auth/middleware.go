package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type contextKey string

const claimsKey contextKey = "auth_claims"
const apiTokenKey contextKey = "auth_is_api_token"

// Middleware extracts and validates the JWT from the Authorization header.
// On success, the Claims are stored in the request context. It does NOT accept
// the token via query string — putting a bearer credential in a URL leaks it
// into proxy access logs, browser history, and Referer headers. Endpoints that
// must support the header-less EventSource API use SSEMiddleware instead.
func Middleware(secret []byte) func(http.Handler) http.Handler {
	return authMiddleware(secret, false, nil)
}

// MiddlewareWithTokens is like Middleware but also accepts long-lived API
// (personal access) tokens: a bearer credential carrying the APITokenPrefix is
// routed to apiTokens instead of the JWT parser. On success it deposits the
// same *Claims as a JWT would (the token acts as its owning user), so every
// downstream handler and RequireRole check works unchanged. When apiTokens is
// nil it behaves exactly like Middleware.
func MiddlewareWithTokens(secret []byte, apiTokens APITokenValidator) func(http.Handler) http.Handler {
	return authMiddleware(secret, false, apiTokens)
}

// SSEMiddleware is like Middleware but additionally accepts the token via the
// `token` query parameter, for the browser EventSource API which cannot set
// request headers. Restricted to the SSE GET endpoints so the URL-borne-token
// exposure stays scoped to exactly the routes that need it.
func SSEMiddleware(secret []byte) func(http.Handler) http.Handler {
	return authMiddleware(secret, true, nil)
}

// SSEMiddlewareWithTokens is SSEMiddleware that also accepts API tokens (header
// or query param), so an agent can consume the SSE streams.
func SSEMiddlewareWithTokens(secret []byte, apiTokens APITokenValidator) func(http.Handler) http.Handler {
	return authMiddleware(secret, true, apiTokens)
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

func authMiddleware(secret []byte, allowQueryToken bool, apiTokens APITokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ok := resolveAuthContext(w, r, secret, allowQueryToken, apiTokens)
			if !ok {
				return
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// resolveAuthContext validates the request's bearer credential and returns a
// context carrying the resolved Claims. On failure it writes the 401 response
// and returns ok=false. Kept as a flat top-level function (rather than inline
// in the middleware's nested closure) so the auth-vs-token branching stays
// readable and within the cognitive-complexity budget.
func resolveAuthContext(w http.ResponseWriter, r *http.Request, secret []byte, allowQueryToken bool, apiTokens APITokenValidator) (context.Context, bool) {
	tokenString, ok := bearerOrQueryToken(r, allowQueryToken)
	if !ok {
		http.Error(w, `{"error":"invalid authorization header"}`, http.StatusUnauthorized)
		return nil, false
	}
	if tokenString == "" {
		http.Error(w, `{"error":"authentication required"}`, http.StatusUnauthorized)
		return nil, false
	}

	// Long-lived API tokens carry a distinct prefix and are validated against
	// the api_tokens table (their own revocation = row existence + expiry), so
	// they bypass the token_version path that RevocationMiddleware applies to
	// JWTs.
	if apiTokens != nil && LooksLikeAPIToken(tokenString) {
		claims, err := apiTokens(r.Context(), tokenString)
		if err != nil {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return nil, false
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		return context.WithValue(ctx, apiTokenKey, true), true
	}

	claims, err := ParseToken(secret, tokenString)
	if err != nil {
		http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
		return nil, false
	}
	return context.WithValue(r.Context(), claimsKey, claims), true
}

// IsAPIToken reports whether the request was authenticated with a long-lived
// API token (rather than an interactive JWT session). RevocationMiddleware
// uses it to skip the token_version check, which doesn't apply to API tokens.
func IsAPIToken(ctx context.Context) bool {
	v, _ := ctx.Value(apiTokenKey).(bool)
	return v
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

// RequireInteractiveSession rejects requests authenticated with a long-lived
// API token, restricting a route to interactive (JWT) sessions. Use it to keep
// a leaked agent token from escalating its own privilege — e.g. minting more
// tokens or revoking the user's others. Mount AFTER the auth middleware.
func RequireInteractiveSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsAPIToken(r.Context()) {
			http.Error(w, `{"error":"this action requires an interactive session"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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

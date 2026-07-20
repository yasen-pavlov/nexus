package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

// maxRequestBodyBytes caps how much of a request body the API will read into
// memory. 1 MiB is far above any legitimate Nexus request (the largest is a
// Telegram session string or a chat question — a few KB) yet small enough that
// an unauthenticated client can't drive the process toward OOM by streaming a
// huge body. Enforced globally (see maxBytesMiddleware) so it also covers the
// pre-auth /auth/login and /auth/register endpoints, which json-decode their
// body before authenticating.
const maxRequestBodyBytes int64 = 1 << 20

// maxBytesMiddleware wraps every request body in an http.MaxBytesReader so a
// single request can never buffer more than limit bytes. It only bounds the
// request-body read; it does not touch the long-lived SSE response streams on
// /api/sync/progress or /api/chats/{id}/messages.
func maxBytesMiddleware(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// decodeJSONBody decodes r.Body into dst as JSON. On failure it writes the
// appropriate error envelope and returns false, so callers do `if
// !decodeJSONBody(w, r, &req) { return }`. A body larger than the
// maxBytesMiddleware limit surfaces as 413; any other decode error is 400.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, errInvalidRequestBody)
		return false
	}
	return true
}

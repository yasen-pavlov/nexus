package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// APITokenPrefix marks a string as a Nexus personal access token. The auth
// middleware uses it to route a bearer credential to the API-token validator
// instead of the JWT parser, and it makes leaked tokens greppable in logs and
// secret scanners.
const APITokenPrefix = "nexus_pat_"

// apiTokenBytes is the number of random bytes in a token (256 bits of
// entropy — far beyond brute-force, so a fast hash for lookup is safe).
const apiTokenBytes = 32

// GenerateAPIToken mints a new personal access token. It returns the plaintext
// (shown to the user exactly once) and the SHA-256 hex hash that gets stored.
// The plaintext is the only copy of the secret; it is never persisted.
func GenerateAPIToken() (plaintext, hash string, err error) {
	buf := make([]byte, apiTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("auth: generate api token: %w", err)
	}
	plaintext = APITokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return plaintext, HashAPIToken(plaintext), nil
}

// HashAPIToken returns the SHA-256 hex digest of a token plaintext. Used both
// at creation (to store) and at validation (to look up). A plain fast hash is
// appropriate here — tokens are high-entropy random strings, not low-entropy
// passwords, so bcrypt's deliberate slowness buys nothing and would forfeit
// the O(1) indexed lookup.
func HashAPIToken(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// LooksLikeAPIToken reports whether a bearer credential is a Nexus personal
// access token (vs a JWT session token).
func LooksLikeAPIToken(token string) bool {
	return strings.HasPrefix(token, APITokenPrefix)
}

// APITokenValidator resolves an opaque API token to the Claims of its owning
// user. It returns an error for unknown, expired, or otherwise invalid tokens.
// The api package wires this to a store-backed implementation so the auth
// package stays free of any database dependency.
type APITokenValidator func(ctx context.Context, token string) (*Claims, error)

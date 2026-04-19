// Package auth provides authentication and authorization utilities.
package auth

import (
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// HashPassword hashes a plaintext password using bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// CheckPassword compares a bcrypt hash with a plaintext password.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

var (
	dummyOnce sync.Once
	dummyHash []byte
)

// dummyPasswordHash returns a precomputed bcrypt hash to compare against
// when the requested user doesn't exist. Used by Login to keep timing
// constant between "no such user" and "user exists, wrong password" so
// an attacker can't enumerate usernames by measuring response latency.
//
// The first call pays the bcrypt cost (~200ms at cost 12); subsequent
// calls reuse the cached hash. We don't care what plaintext it hashes,
// only that the resulting comparison takes the same wall-clock time as
// a real bcrypt check.
func dummyPasswordHash() []byte {
	dummyOnce.Do(func() {
		// "*" can't appear in a real password input that bcrypt would
		// accept here — but more importantly, this hash will never match
		// any password the caller sends.
		h, err := bcrypt.GenerateFromPassword([]byte("nexus-dummy-password-do-not-use"), bcryptCost)
		if err != nil {
			// bcrypt with a normal password and supported cost shouldn't
			// fail; if it does, fall back to a dummy that always rejects.
			dummyHash = []byte("$2a$12$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidinvali")
			return
		}
		dummyHash = h
	})
	return dummyHash
}

// CheckPasswordConstantTime runs a bcrypt comparison even when hash is
// empty (e.g. the user doesn't exist), so the failure timing matches
// the "wrong password" path. Returns false in both error cases.
func CheckPasswordConstantTime(hash, password string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(password))
		return false
	}
	return CheckPassword(hash, password)
}

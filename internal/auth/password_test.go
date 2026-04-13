package auth

import "testing"

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("mysecret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash == "mysecret" {
		t.Fatal("hash should not equal plaintext")
	}
}

func TestCheckPassword(t *testing.T) {
	hash, _ := HashPassword("correct")

	if !CheckPassword(hash, "correct") {
		t.Error("expected correct password to match")
	}
	if CheckPassword(hash, "wrong") {
		t.Error("expected wrong password to not match")
	}
}

func TestHashPassword_DifferentHashes(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Error("bcrypt should produce different hashes for same input (random salt)")
	}
}

func TestHashPassword_TooLong(t *testing.T) {
	// bcrypt rejects passwords longer than 72 bytes. HashPassword
	// propagates that error so the handler can return a clear 400
	// instead of silently truncating.
	hash, err := HashPassword(string(make([]byte, 100)))
	if err == nil {
		t.Errorf("expected error for >72-byte password, got hash %q", hash)
	}
}

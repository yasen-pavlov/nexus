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

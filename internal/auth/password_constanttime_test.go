package auth

import "testing"

func TestCheckPasswordConstantTime_MissingHashStillRejects(t *testing.T) {
	if CheckPasswordConstantTime("", "anything") {
		t.Error("empty hash must never authenticate")
	}
}

func TestCheckPasswordConstantTime_RealHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("hunter22-hunter22")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPasswordConstantTime(hash, "hunter22-hunter22") {
		t.Error("constant-time check should accept correct password")
	}
	if CheckPasswordConstantTime(hash, "wrong") {
		t.Error("constant-time check should reject wrong password")
	}
}

func TestDummyPasswordHash_Cached(t *testing.T) {
	h1 := dummyPasswordHash()
	h2 := dummyPasswordHash()
	if string(h1) != string(h2) {
		t.Error("dummy hash should be cached after first call")
	}
}

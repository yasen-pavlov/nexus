package crypto

import (
	"encoding/hex"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key, err := NewKey("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NewKey failed: %v", err)
	}
	return key
}

func TestNewKey_Valid(t *testing.T) {
	key := testKey(t)
	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}
}

func TestNewKey_InvalidHex(t *testing.T) {
	_, err := NewKey("not-hex")
	if err == nil || !strings.Contains(err.Error(), "invalid hex") {
		t.Errorf("error = %v, want 'invalid hex'", err)
	}
}

func TestNewKey_WrongLength(t *testing.T) {
	_, err := NewKey(hex.EncodeToString([]byte("short")))
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("error = %v, want '32 bytes'", err)
	}
}

func TestEncryptDecrypt_Roundtrip(t *testing.T) {
	key := testKey(t)
	plaintext := "my-secret-password"

	encrypted, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if !IsEncrypted(encrypted) {
		t.Errorf("encrypted value should have enc: prefix, got %q", encrypted)
	}

	if encrypted == plaintext {
		t.Error("encrypted should differ from plaintext")
	}

	decrypted, err := Decrypt(key, encrypted)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if decrypted != plaintext {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncrypt_DifferentNonces(t *testing.T) {
	key := testKey(t)
	a, _ := Encrypt(key, "same")
	b, _ := Encrypt(key, "same")
	if a == b {
		t.Error("two encryptions of same plaintext should produce different ciphertexts (random nonce)")
	}
}

func TestDecrypt_NotEncrypted(t *testing.T) {
	key := testKey(t)
	_, err := Decrypt(key, "plain-value")
	if err == nil || !strings.Contains(err.Error(), "not an encrypted") {
		t.Errorf("error = %v, want 'not an encrypted'", err)
	}
}

func TestDecrypt_InvalidBase64(t *testing.T) {
	key := testKey(t)
	_, err := Decrypt(key, "enc:not-base64!!!")
	if err == nil || !strings.Contains(err.Error(), "invalid base64") {
		t.Errorf("error = %v, want 'invalid base64'", err)
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	key1 := testKey(t)
	key2, _ := NewKey("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")

	encrypted, _ := Encrypt(key1, "secret")
	_, err := Decrypt(key2, encrypted)
	if err == nil || !strings.Contains(err.Error(), "decryption failed") {
		t.Errorf("error = %v, want 'decryption failed'", err)
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	key := testKey(t)
	_, err := Decrypt(key, "enc:AQID") // very short base64
	if err == nil || !strings.Contains(err.Error(), "ciphertext too short") {
		t.Errorf("error = %v, want 'ciphertext too short'", err)
	}
}

func TestIsEncrypted(t *testing.T) {
	if IsEncrypted("plain") {
		t.Error("plain should not be encrypted")
	}
	if !IsEncrypted("enc:something") {
		t.Error("enc:something should be encrypted")
	}
}

// --- Sensitive field tests ---

func TestEncryptConfig_EncryptsSensitiveFields(t *testing.T) {
	key := testKey(t)
	config := map[string]any{
		"server":   "imap.example.com",
		"username": "user@example.com",
		"password": "secret123",
	}

	result, err := EncryptConfig(key, "imap", config)
	if err != nil {
		t.Fatalf("EncryptConfig failed: %v", err)
	}

	// server and username should be unchanged
	if result["server"] != "imap.example.com" {
		t.Errorf("server changed: %v", result["server"])
	}
	if result["username"] != "user@example.com" {
		t.Errorf("username changed: %v", result["username"])
	}

	// password should be encrypted
	pw, ok := result["password"].(string)
	if !ok || !IsEncrypted(pw) {
		t.Errorf("password should be encrypted, got %v", result["password"])
	}

	// Original map should not be modified
	if config["password"] != "secret123" {
		t.Error("original map was modified")
	}
}

func TestEncryptConfig_NilKey(t *testing.T) {
	config := map[string]any{"password": "secret"}
	result, err := EncryptConfig(nil, "imap", config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["password"] != "secret" {
		t.Error("nil key should leave config unchanged")
	}
}

func TestEncryptConfig_UnknownType(t *testing.T) {
	key := testKey(t)
	config := map[string]any{"password": "secret"}
	result, err := EncryptConfig(key, "filesystem", config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["password"] != "secret" {
		t.Error("filesystem has no sensitive fields, should be unchanged")
	}
}

func TestEncryptConfig_SkipsAlreadyEncrypted(t *testing.T) {
	key := testKey(t)
	encrypted, _ := Encrypt(key, "original")
	config := map[string]any{"password": encrypted}

	result, err := EncryptConfig(key, "imap", config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["password"] != encrypted {
		t.Error("already encrypted value should not be re-encrypted")
	}
}

func TestDecryptConfig_DecryptsSensitiveFields(t *testing.T) {
	key := testKey(t)
	encrypted, _ := Encrypt(key, "secret123")
	config := map[string]any{
		"server":   "imap.example.com",
		"password": encrypted,
	}

	result, err := DecryptConfig(key, "imap", config)
	if err != nil {
		t.Fatalf("DecryptConfig failed: %v", err)
	}

	if result["server"] != "imap.example.com" {
		t.Errorf("server changed: %v", result["server"])
	}
	if result["password"] != "secret123" {
		t.Errorf("password = %v, want secret123", result["password"])
	}
}

func TestDecryptConfig_NilKey(t *testing.T) {
	config := map[string]any{"password": "enc:something"}
	result, err := DecryptConfig(nil, "imap", config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["password"] != "enc:something" {
		t.Error("nil key should leave config unchanged")
	}
}

func TestDecryptConfig_SkipsPlaintext(t *testing.T) {
	key := testKey(t)
	config := map[string]any{"password": "plaintext"}

	result, err := DecryptConfig(key, "imap", config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["password"] != "plaintext" {
		t.Error("plaintext value should not be decrypted")
	}
}

func TestMaskConfig(t *testing.T) {
	config := map[string]any{
		"server":   "imap.example.com",
		"password": "secret123",
	}

	result := MaskConfig("imap", config)
	if result["server"] != "imap.example.com" {
		t.Errorf("server should be unchanged")
	}
	pw, _ := result["password"].(string)
	if pw != "****t123" {
		t.Errorf("masked password = %q, want ****t123", pw)
	}

	// Original unchanged
	if config["password"] != "secret123" {
		t.Error("original map was modified")
	}
}

func TestMaskConfig_ShortValue(t *testing.T) {
	result := MaskConfig("imap", map[string]any{"password": "ab"})
	if result["password"] != "****" {
		t.Errorf("short value mask = %q, want ****", result["password"])
	}
}

func TestMaskConfig_NoSensitiveType(t *testing.T) {
	config := map[string]any{"root_path": "/data"}
	result := MaskConfig("filesystem", config)
	if result["root_path"] != "/data" {
		t.Error("filesystem should have no masking")
	}
}

func TestIsMasked(t *testing.T) {
	if IsMasked("plain") {
		t.Error("plain should not be masked")
	}
	if !IsMasked("****1234") {
		t.Error("****1234 should be masked")
	}
	if !IsMasked("****") {
		t.Error("**** should be masked")
	}
}

func TestRestoreMaskedFields(t *testing.T) {
	oldConfig := map[string]any{"password": "real-secret", "server": "imap.example.com"}
	newConfig := map[string]any{"password": "****cret", "server": "new-server.com"}

	result := RestoreMaskedFields("imap", newConfig, oldConfig)
	if result["password"] != "real-secret" {
		t.Errorf("password = %v, want real-secret", result["password"])
	}
	if result["server"] != "new-server.com" {
		t.Errorf("server = %v, want new-server.com", result["server"])
	}
}

func TestRestoreMaskedFields_NewPassword(t *testing.T) {
	oldConfig := map[string]any{"password": "old-secret"}
	newConfig := map[string]any{"password": "new-secret"}

	result := RestoreMaskedFields("imap", newConfig, oldConfig)
	if result["password"] != "new-secret" {
		t.Errorf("password = %v, want new-secret (not masked, should keep new value)", result["password"])
	}
}

func TestEncryptDecryptConfig_Roundtrip(t *testing.T) {
	key := testKey(t)
	original := map[string]any{
		"server":   "imap.example.com",
		"password": "my-secret",
		"port":     "993",
	}

	encrypted, err := EncryptConfig(key, "imap", original)
	if err != nil {
		t.Fatalf("EncryptConfig: %v", err)
	}

	decrypted, err := DecryptConfig(key, "imap", encrypted)
	if err != nil {
		t.Fatalf("DecryptConfig: %v", err)
	}

	if decrypted["password"] != "my-secret" {
		t.Errorf("roundtrip password = %v, want my-secret", decrypted["password"])
	}
	if decrypted["server"] != "imap.example.com" {
		t.Errorf("roundtrip server = %v, want imap.example.com", decrypted["server"])
	}
}

func TestMaskConfig_Paperless(t *testing.T) {
	result := MaskConfig("paperless", map[string]any{"token": "abcdef123456", "url": "http://localhost"})
	if result["token"] != "****3456" {
		t.Errorf("masked token = %q, want ****3456", result["token"])
	}
	if result["url"] != "http://localhost" {
		t.Error("url should not be masked")
	}
}

func TestMaskConfig_Telegram(t *testing.T) {
	result := MaskConfig("telegram", map[string]any{"api_hash": "longhashvalue123", "api_id": "12345"})
	hash, _ := result["api_hash"].(string)
	if !IsMasked(hash) {
		t.Errorf("api_hash should be masked, got %q", hash)
	}
	if result["api_id"] != "12345" {
		t.Error("api_id should not be masked")
	}
}

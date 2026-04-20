// Package crypto provides field-level encryption for sensitive connector config values.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

const (
	encPrefix       = "enc:"
	cryptoErrFormat = "crypto: %w"
)

// NewKey parses an encryption key from a hex-encoded string.
// The key must be exactly 32 bytes (256 bits) for AES-256.
func NewKey(hexKey string) ([]byte, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("crypto: invalid hex key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}
	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM and returns an "enc:<base64>" string.
func Encrypt(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf(cryptoErrFormat, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf(cryptoErrFormat, err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf(cryptoErrFormat, err)
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a string produced by Encrypt. Returns an error if the
// string doesn't have the "enc:" prefix or if decryption fails.
func Decrypt(key []byte, ciphertext string) (string, error) {
	if !IsEncrypted(ciphertext) {
		return "", fmt.Errorf("crypto: not an encrypted value")
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encPrefix))
	if err != nil {
		return "", fmt.Errorf("crypto: invalid base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf(cryptoErrFormat, err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf(cryptoErrFormat, err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("crypto: ciphertext too short")
	}

	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: decryption failed: %w", err)
	}

	return string(plaintext), nil
}

// IsEncrypted returns true if the value has the encryption prefix.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encPrefix)
}

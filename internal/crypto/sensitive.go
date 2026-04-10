package crypto

import "strings"

// SensitiveFields maps connector types to their sensitive config field names.
var SensitiveFields = map[string][]string{
	"imap":      {"password"},
	"paperless": {"token"},
	"telegram":  {"api_hash"},
}

// sensitiveSettingsKeys lists exact keys in the settings table that hold
// secrets and must be encrypted at rest.
var sensitiveSettingsKeys = map[string]bool{
	"embedding_api_key": true,
	"rerank_api_key":    true,
}

// IsSensitiveSettingsKey returns true if the given settings key holds a
// secret that should be encrypted at rest. Keys with the
// "telegram_session_" prefix store full Telegram session blobs and are
// always treated as sensitive.
func IsSensitiveSettingsKey(key string) bool {
	if sensitiveSettingsKeys[key] {
		return true
	}
	return strings.HasPrefix(key, "telegram_session_")
}

// EncryptConfig encrypts sensitive fields in a connector config map.
// Returns a shallow copy with sensitive string values encrypted.
// If key is nil, returns the config unchanged.
func EncryptConfig(key []byte, connType string, config map[string]any) (map[string]any, error) {
	if key == nil {
		return config, nil
	}

	fields := SensitiveFields[connType]
	if len(fields) == 0 {
		return config, nil
	}

	result := make(map[string]any, len(config))
	for k, v := range config {
		result[k] = v
	}

	for _, field := range fields {
		val, ok := result[field].(string)
		if !ok || val == "" || IsEncrypted(val) {
			continue
		}
		encrypted, err := Encrypt(key, val)
		if err != nil {
			return nil, err
		}
		result[field] = encrypted
	}

	return result, nil
}

// DecryptConfig decrypts sensitive fields in a connector config map.
// Returns a shallow copy with sensitive values decrypted.
// If key is nil, returns the config unchanged.
func DecryptConfig(key []byte, connType string, config map[string]any) (map[string]any, error) {
	if key == nil {
		return config, nil
	}

	fields := SensitiveFields[connType]
	if len(fields) == 0 {
		return config, nil
	}

	result := make(map[string]any, len(config))
	for k, v := range config {
		result[k] = v
	}

	for _, field := range fields {
		val, ok := result[field].(string)
		if !ok || !IsEncrypted(val) {
			continue
		}
		decrypted, err := Decrypt(key, val)
		if err != nil {
			return nil, err
		}
		result[field] = decrypted
	}

	return result, nil
}

const maskPrefix = "****"

// MaskConfig returns a shallow copy of the config with sensitive values masked.
// Masked values show "****" followed by the last 4 characters of the original value.
func MaskConfig(connType string, config map[string]any) map[string]any {
	fields := SensitiveFields[connType]
	if len(fields) == 0 {
		return config
	}

	result := make(map[string]any, len(config))
	for k, v := range config {
		result[k] = v
	}

	for _, field := range fields {
		val, ok := result[field].(string)
		if !ok || val == "" {
			continue
		}
		if len(val) > 4 {
			result[field] = maskPrefix + val[len(val)-4:]
		} else {
			result[field] = maskPrefix
		}
	}

	return result
}

// IsMasked returns true if a value looks like a masked secret.
func IsMasked(value string) bool {
	return strings.HasPrefix(value, maskPrefix)
}

// RestoreMaskedFields replaces masked values in newConfig with the corresponding
// values from oldConfig. This allows clients to submit masked secrets on update
// without losing the original values.
func RestoreMaskedFields(connType string, newConfig, oldConfig map[string]any) map[string]any {
	fields := SensitiveFields[connType]
	if len(fields) == 0 {
		return newConfig
	}

	for _, field := range fields {
		newVal, ok := newConfig[field].(string)
		if !ok {
			continue
		}
		if IsMasked(newVal) {
			if oldVal, ok := oldConfig[field]; ok {
				newConfig[field] = oldVal
			}
		}
	}

	return newConfig
}

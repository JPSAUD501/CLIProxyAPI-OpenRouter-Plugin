package openrouter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const storageVersion = 1

type authStorage struct {
	Version      int    `json:"version"`
	EncryptedKey string `json:"encrypted_key"`
	KeyHash      string `json:"key_hash"`
	Label        string `json:"label"`
}

func newStorage(apiKey, label string) (authStorage, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return authStorage{}, statusError("invalid_api_key", "OpenRouter API key is required", 400, false)
	}
	encrypted, err := protectSecret([]byte(apiKey))
	if err != nil {
		return authStorage{}, fmt.Errorf("protect OpenRouter API key: %w", err)
	}
	hash := sha256.Sum256([]byte(apiKey))
	return authStorage{Version: storageVersion, EncryptedKey: encrypted, KeyHash: hex.EncodeToString(hash[:]), Label: normalizeLabel(label)}, nil
}

func parseStorage(raw []byte) (authStorage, error) {
	var storage authStorage
	if err := json.Unmarshal(raw, &storage); err != nil {
		return authStorage{}, fmt.Errorf("decode OpenRouter credential: %w", err)
	}
	if storage.Version != storageVersion || strings.TrimSpace(storage.EncryptedKey) == "" || len(storage.KeyHash) != 64 {
		return authStorage{}, statusError("invalid_credential", "OpenRouter credential storage is invalid", 400, false)
	}
	storage.Label = normalizeLabel(storage.Label)
	return storage, nil
}

func (s authStorage) apiKey() (string, error) {
	plain, err := unprotectSecret(s.EncryptedKey)
	if err != nil {
		return "", fmt.Errorf("unprotect OpenRouter API key: %w", err)
	}
	key := strings.TrimSpace(string(plain))
	for i := range plain {
		plain[i] = 0
	}
	if key == "" {
		return "", statusError("invalid_credential", "OpenRouter API key is empty", 400, false)
	}
	return key, nil
}

func normalizeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Account"
	}
	if len(value) > 80 {
		return value[:80]
	}
	return value
}

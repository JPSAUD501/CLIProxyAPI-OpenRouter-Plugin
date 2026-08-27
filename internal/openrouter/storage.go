package openrouter

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	legacyStorageVersion = 1
	storageVersion       = 2
	storageCipher        = "aes-256-gcm"
	masterKeyEnv         = "CLIPROXY_OPENROUTER_MASTER_KEY"
)

type authStorage struct {
	Version      int    `json:"version"`
	Cipher       string `json:"cipher,omitempty"`
	EncryptedKey string `json:"encrypted_key"`
	KeyHash      string `json:"key_hash"`
	Label        string `json:"label"`
	Note         string `json:"note,omitempty"`
}

func newStorage(apiKey, label string) (authStorage, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return authStorage{}, statusError("invalid_api_key", "OpenRouter API key is required", 400, false)
	}
	encrypted, err := protectPortableSecret([]byte(apiKey))
	if err != nil {
		return authStorage{}, fmt.Errorf("protect OpenRouter API key: %w", err)
	}
	hash := sha256.Sum256([]byte(apiKey))
	return authStorage{Version: storageVersion, Cipher: storageCipher, EncryptedKey: encrypted, KeyHash: hex.EncodeToString(hash[:]), Label: normalizeLabel(label)}, nil
}

func parseStorage(raw []byte) (authStorage, error) {
	var storage authStorage
	if err := json.Unmarshal(raw, &storage); err != nil {
		return authStorage{}, fmt.Errorf("decode OpenRouter credential: %w", err)
	}
	validVersion := storage.Version == legacyStorageVersion || storage.Version == storageVersion
	if !validVersion || strings.TrimSpace(storage.EncryptedKey) == "" || len(storage.KeyHash) != 64 {
		return authStorage{}, statusError("invalid_credential", "OpenRouter credential storage is invalid", 400, false)
	}
	if storage.Version == storageVersion && storage.Cipher != storageCipher {
		return authStorage{}, statusError("invalid_credential", "OpenRouter credential cipher is not supported", 400, false)
	}
	storage.Label = normalizeLabel(storage.Label)
	storage.Note = strings.TrimSpace(storage.Note)
	return storage, nil
}

func (s authStorage) apiKey() (string, error) {
	var plain []byte
	var err error
	switch s.Version {
	case legacyStorageVersion:
		plain, err = unprotectLegacySecret(s.EncryptedKey)
	case storageVersion:
		plain, err = unprotectPortableSecret(s.EncryptedKey)
	default:
		err = fmt.Errorf("unsupported credential version %d", s.Version)
	}
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

func migrateStorage(storage authStorage) (authStorage, error) {
	if storage.Version == storageVersion {
		return storage, nil
	}
	apiKey, err := storage.apiKey()
	if err != nil {
		return authStorage{}, err
	}
	migrated, err := newStorage(apiKey, storage.Label)
	apiKey = ""
	if err != nil {
		return authStorage{}, err
	}
	migrated.Note = storage.Note
	if migrated.KeyHash != storage.KeyHash {
		return authStorage{}, fmt.Errorf("credential hash changed during migration")
	}
	return migrated, nil
}

func protectPortableSecret(plain []byte) (string, error) {
	if len(plain) == 0 {
		return "", fmt.Errorf("secret is empty")
	}
	aead, err := portableAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("create credential nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, plain, []byte(storageCipher))
	payload := append(nonce, sealed...)
	return base64.RawStdEncoding.EncodeToString(payload), nil
}

func unprotectPortableSecret(encoded string) ([]byte, error) {
	aead, err := portableAEAD()
	if err != nil {
		return nil, err
	}
	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode encrypted credential: %w", err)
	}
	if len(payload) <= aead.NonceSize() {
		return nil, fmt.Errorf("encrypted credential is truncated")
	}
	plain, err := aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], []byte(storageCipher))
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: %w", err)
	}
	return plain, nil
}

func portableAEAD() (cipher.AEAD, error) {
	encoded := strings.TrimSpace(os.Getenv(masterKeyEnv))
	if encoded == "" {
		return nil, fmt.Errorf("%s must contain a base64-encoded 32-byte key", masterKeyEnv)
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%s must contain a base64-encoded 32-byte key", masterKeyEnv)
	}
	block, err := aes.NewCipher(key)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	return cipher.NewGCM(block)
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

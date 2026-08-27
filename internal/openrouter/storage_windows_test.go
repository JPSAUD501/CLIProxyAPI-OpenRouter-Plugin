//go:build windows

package openrouter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStorageUsesDPAPIAndStableDeduplicationHash(t *testing.T) {
	setTestMasterKey(t)
	const key = "test-openrouter-key-never-persist-plaintext"
	first, err := newStorage(key, "Account A")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newStorage(key, "Account B")
	if err != nil {
		t.Fatal(err)
	}
	if first.KeyHash != second.KeyHash {
		t.Fatal("equal keys must have the same deduplication hash")
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(key)) || strings.Contains(first.EncryptedKey, key) {
		t.Fatal("plaintext API key leaked into persisted storage")
	}
	parsed, err := parseStorage(raw)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := parsed.apiKey()
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != key {
		t.Fatal("portable round trip changed the key")
	}
	auth := authData(parsed)
	if auth.Attributes["priority"] != "-1" || auth.Label != "OpenRouter - Account A" {
		t.Fatalf("unexpected auth metadata: %#v", auth)
	}
}

func TestMigrateCredentialFileReplacesValidatedLegacyStorage(t *testing.T) {
	setTestMasterKey(t)
	const key = "test-openrouter-key-never-persist-plaintext"
	encrypted, err := protectLegacySecret([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(key))
	legacy := authStorage{
		Version:      legacyStorageVersion,
		EncryptedKey: encrypted,
		KeyHash:      hex.EncodeToString(hash[:]),
		Label:        "Account A",
		Note:         "Owner A",
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "openrouter-test.json")
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = MigrateCredentialFile(path); err != nil {
		t.Fatal(err)
	}
	migratedRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(migratedRaw, []byte(key)) {
		t.Fatal("migration persisted the plaintext API key")
	}
	migrated, err := parseStorage(migratedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Version != storageVersion || migrated.Cipher != storageCipher {
		t.Fatalf("unexpected migrated envelope: %#v", migrated)
	}
	if migrated.KeyHash != legacy.KeyHash || migrated.Label != legacy.Label || migrated.Note != legacy.Note {
		t.Fatalf("migration changed credential metadata: %#v", migrated)
	}
	decrypted, err := migrated.apiKey()
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != key {
		t.Fatal("migration changed the API key")
	}
}

func TestAuthDataPreservesProviderKeyAndCredentialNote(t *testing.T) {
	setTestMasterKey(t)
	storage, err := newStorage("test-openrouter-key-never-persist-plaintext", "Account A")
	if err != nil {
		t.Fatal(err)
	}
	storage.Note = "  JPSAUD501  "
	raw, err := json.Marshal(storage)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseStorage(raw)
	if err != nil {
		t.Fatal(err)
	}
	auth := authData(parsed)
	if auth.Provider != providerID {
		t.Fatalf("provider = %q, want %s", auth.Provider, providerID)
	}
	if auth.Metadata["note"] != "JPSAUD501" || auth.Attributes["note"] != "JPSAUD501" {
		t.Fatalf("credential note was not preserved: metadata=%#v attributes=%#v", auth.Metadata, auth.Attributes)
	}
}

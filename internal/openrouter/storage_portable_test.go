package openrouter

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	_ = os.Setenv(masterKeyEnv, base64.StdEncoding.EncodeToString(make([]byte, 32)))
	os.Exit(m.Run())
}

func setTestMasterKey(t *testing.T) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv(masterKeyEnv, base64.StdEncoding.EncodeToString(key))
}

func TestPortableStorageEncryptsAndAuthenticatesCredential(t *testing.T) {
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
	if first.Version != storageVersion || first.Cipher != storageCipher {
		t.Fatalf("unexpected storage envelope: %#v", first)
	}
	if first.KeyHash != second.KeyHash {
		t.Fatal("equal keys must have the same deduplication hash")
	}
	if first.EncryptedKey == second.EncryptedKey {
		t.Fatal("credential encryption must use a fresh nonce")
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

	payload, err := base64.RawStdEncoding.DecodeString(first.EncryptedKey)
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)-1] ^= 1
	first.EncryptedKey = base64.RawStdEncoding.EncodeToString(payload)
	if _, err = first.apiKey(); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestPortableStorageRequiresValidMasterKey(t *testing.T) {
	t.Setenv(masterKeyEnv, "")
	if _, err := newStorage("test-key", "Account"); err == nil || !strings.Contains(err.Error(), masterKeyEnv) {
		t.Fatalf("missing master key error = %v", err)
	}
	t.Setenv(masterKeyEnv, base64.StdEncoding.EncodeToString(make([]byte, 31)))
	if _, err := newStorage("test-key", "Account"); err == nil || !strings.Contains(err.Error(), "32-byte") {
		t.Fatalf("invalid master key error = %v", err)
	}
}

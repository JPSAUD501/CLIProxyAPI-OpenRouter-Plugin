//go:build windows

package openrouter

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestStorageUsesDPAPIAndStableDeduplicationHash(t *testing.T) {
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
		t.Fatal("DPAPI round trip changed the key")
	}
	auth := authData(parsed)
	if auth.Attributes["priority"] != "-1" || auth.Label != "OpenRouter - Account A" {
		t.Fatalf("unexpected auth metadata: %#v", auth)
	}
}

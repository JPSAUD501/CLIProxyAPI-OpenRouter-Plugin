package openrouter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// MigrateCredentialFile rewrites a legacy DPAPI credential as portable v2
// storage. The replacement happens only after the new file decrypts back to
// the same key hash.
func MigrateCredentialFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read credential: %w", err)
	}
	storage, err := parseStorage(raw)
	if err != nil {
		return err
	}
	if storage.Version == storageVersion {
		return nil
	}
	migrated, err := migrateStorage(storage)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(migrated, "", "  ")
	if err != nil {
		return fmt.Errorf("encode migrated credential: %w", err)
	}
	encoded = append(encoded, '\n')

	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".openrouter-migrate-*")
	if err != nil {
		return fmt.Errorf("create migration file: %w", err)
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		_ = temp.Close()
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(encoded)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write migration file: %w", err)
	}

	checkRaw, err := os.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}
	check, err := parseStorage(checkRaw)
	if err != nil {
		return fmt.Errorf("validate migration file: %w", err)
	}
	key, err := check.apiKey()
	if err != nil {
		return fmt.Errorf("validate migrated credential: %w", err)
	}
	if key == "" {
		return fmt.Errorf("migrated credential decrypted to an empty key")
	}
	key = ""
	if check.KeyHash != storage.KeyHash {
		return fmt.Errorf("credential hash changed during migration validation")
	}
	if err = os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace credential: %w", err)
	}
	keepTemp = false
	return nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JPSAUD501/CLIProxyAPI-OpenRouter-Plugin/internal/openrouter"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: openrouter-migrate <credential.json> [credential.json...]")
		os.Exit(2)
	}
	failed := false
	for _, path := range os.Args[1:] {
		path = strings.TrimSpace(path)
		if path == "" || strings.ToLower(filepath.Ext(path)) != ".json" {
			fmt.Fprintf(os.Stderr, "%s: expected a JSON credential file\n", path)
			failed = true
			continue
		}
		if err := openrouter.MigrateCredentialFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(path), err)
			failed = true
			continue
		}
		fmt.Printf("%s: migrated or already current\n", filepath.Base(path))
	}
	if failed {
		os.Exit(1)
	}
}

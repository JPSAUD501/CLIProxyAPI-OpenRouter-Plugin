//go:build !windows

package openrouter

import "fmt"

func protectSecret([]byte) (string, error) {
	return "", fmt.Errorf("OpenRouter credentials require Windows DPAPI")
}

func unprotectSecret(string) ([]byte, error) {
	return nil, fmt.Errorf("OpenRouter credentials require Windows DPAPI")
}

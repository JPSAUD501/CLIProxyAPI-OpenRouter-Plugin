//go:build !windows

package openrouter

import "fmt"

func unprotectLegacySecret(string) ([]byte, error) {
	return nil, fmt.Errorf("legacy OpenRouter credentials use Windows DPAPI; migrate them on the Windows user account that created them")
}

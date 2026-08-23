//go:build windows

package openrouter

import (
	"encoding/base64"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const cryptProtectUIForbidden = 0x1

func protectSecret(plain []byte) (string, error) {
	if len(plain) == 0 {
		return "", fmt.Errorf("secret is empty")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, cryptProtectUIForbidden, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	protected := unsafe.Slice(out.Data, out.Size)
	return base64.RawStdEncoding.EncodeToString(protected), nil
}

func unprotectSecret(encoded string) ([]byte, error) {
	protected, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(protected) == 0 {
		return nil, fmt.Errorf("decode protected secret: %w", err)
	}
	in := windows.DataBlob{Size: uint32(len(protected)), Data: &protected[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, cryptProtectUIForbidden, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

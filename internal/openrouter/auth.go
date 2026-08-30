package openrouter

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type loginFlow struct {
	PrivateKey *rsa.PrivateKey
	PublicKey  string
	ExpiresAt  time.Time
	Storage    *authStorage
	Err        string
}

type keyResponse struct {
	Data struct {
		Label string `json:"label"`
	} `json:"data"`
}

func (s *Service) StartLogin(_ context.Context, req pluginapi.AuthLoginStartRequest) (pluginapi.AuthLoginStartResponse, error) {
	loginBaseURL, err := s.loginBaseURL(req.BaseURL)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("resolve login base URL: %w", err)
	}
	stateBytes := make([]byte, 24)
	if _, err := rand.Read(stateBytes); err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("create login encryption key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, err
	}
	state := hex.EncodeToString(stateBytes)
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	s.mu.Lock()
	s.logins[state] = &loginFlow{PrivateKey: privateKey, PublicKey: base64.StdEncoding.EncodeToString(publicDER), ExpiresAt: expiresAt}
	s.mu.Unlock()
	base, err := url.Parse(loginBaseURL)
	if err != nil {
		return pluginapi.AuthLoginStartResponse{}, fmt.Errorf("parse login base URL: %w", err)
	}
	base.Path = "/v0/resource/plugins/openrouter/login"
	base.RawQuery = "state=" + url.QueryEscape(state)
	return pluginapi.AuthLoginStartResponse{Provider: providerID, URL: base.String(), State: state, ExpiresAt: expiresAt}, nil
}

func (s *Service) PollLogin(_ context.Context, req pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	s.mu.Lock()
	flow, found := s.logins[req.State]
	if found && time.Now().UTC().After(flow.ExpiresAt) {
		delete(s.logins, req.State)
		found = false
	}
	if !found {
		s.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: "OpenRouter login expired"}, nil
	}
	if flow.Err != "" {
		message := flow.Err
		delete(s.logins, req.State)
		s.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusError, Message: message}, nil
	}
	if flow.Storage == nil {
		s.mu.Unlock()
		return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending, Message: "Waiting for OpenRouter API key"}, nil
	}
	storage := *flow.Storage
	delete(s.logins, req.State)
	s.mu.Unlock()
	return pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Message: "OpenRouter API key connected", Auth: authData(storage)}, nil
}

func (s *Service) ParseAuth(_ context.Context, req pluginapi.AuthParseRequest) (pluginapi.AuthParseResponse, error) {
	if req.Provider != "" && !strings.EqualFold(req.Provider, providerID) {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	var shape map[string]json.RawMessage
	if json.Unmarshal(req.RawJSON, &shape) != nil {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	if _, encrypted := shape["encrypted_key"]; !encrypted {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	storage, err := parseStorage(req.RawJSON)
	if err != nil {
		return pluginapi.AuthParseResponse{}, err
	}
	if _, err = storage.apiKey(); err != nil {
		return pluginapi.AuthParseResponse{}, err
	}
	auth := authData(storage)
	if req.FileName != "" {
		auth.FileName = filepath.Base(req.FileName)
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: auth}, nil
}

func (s *Service) RefreshAuth(ctx context.Context, callbackID string, req pluginapi.AuthRefreshRequest) (pluginapi.AuthRefreshResponse, error) {
	storage, err := parseStorage(req.StorageJSON)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	apiKey, err := storage.apiKey()
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	label, err := s.validateKey(ctx, callbackID, apiKey)
	apiKey = ""
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	if label != "" && !strings.EqualFold(storage.Label, "Conexia") {
		storage.Label = label
	}
	if _, _, err = s.catalog(ctx, callbackID, req.AuthID, storage, true); err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	next := time.Now().UTC().Add(cacheTTL)
	auth := authData(storage)
	auth.NextRefreshAfter = next
	return pluginapi.AuthRefreshResponse{Auth: auth, NextRefreshAfter: next}, nil
}

func (s *Service) validateKey(ctx context.Context, callbackID, apiKey string) (string, error) {
	resp, err := s.request(ctx, callbackID, apiKey, http.MethodGet, apiBaseURL+"/key", nil)
	if err != nil {
		return "", fmt.Errorf("validate OpenRouter API key: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", upstreamError(resp.StatusCode, resp.Body)
	}
	var data keyResponse
	if err := json.Unmarshal(resp.Body, &data); err != nil {
		return "", fmt.Errorf("decode OpenRouter key metadata: %w", err)
	}
	return normalizeLabel(data.Data.Label), nil
}

func authData(storage authStorage) pluginapi.AuthData {
	raw, _ := json.Marshal(storage)
	id := "openrouter-" + storage.KeyHash[:12]
	metadata := map[string]any{"type": providerID, "priority": -1}
	attributes := map[string]string{"auth_kind": "api_key", "priority": "-1"}
	if note := strings.TrimSpace(storage.Note); note != "" {
		metadata["note"] = note
		attributes["note"] = note
	}
	return pluginapi.AuthData{
		Provider:         providerID,
		ID:               id,
		FileName:         id + ".json",
		Label:            "OpenRouter - " + normalizeLabel(storage.Label),
		StorageJSON:      raw,
		Metadata:         metadata,
		Attributes:       attributes,
		NextRefreshAfter: time.Now().UTC().Add(cacheTTL),
	}
}

func decryptLoginPayload(flow *loginFlow, payload string) (string, error) {
	if flow == nil || flow.PrivateKey == nil {
		return "", statusError("invalid_login", "OpenRouter login state is invalid", 400, false)
	}
	payload = strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimSpace(payload))
	if padding := len(payload) % 4; padding != 0 {
		payload += strings.Repeat("=", 4-padding)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", statusError("invalid_login", "OpenRouter login payload is invalid", 400, false)
	}
	plain, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, flow.PrivateKey, ciphertext, nil)
	if err != nil {
		return "", statusError("invalid_login", "OpenRouter login payload could not be decrypted", 400, false)
	}
	key := strings.TrimSpace(string(plain))
	for i := range plain {
		plain[i] = 0
	}
	return key, nil
}

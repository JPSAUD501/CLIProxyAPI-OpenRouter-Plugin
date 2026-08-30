package openrouter

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	providerID = "openrouter"
	apiBaseURL = "https://openrouter.ai/api/v1"
	cacheTTL   = 30 * time.Minute
)

type Service struct {
	host Host

	mu            sync.Mutex
	cache         map[string]catalogCache
	logins        map[string]*loginFlow
	publicBaseURL string
}

func New(host Host) *Service {
	return &Service{host: host, cache: make(map[string]catalogCache), logins: make(map[string]*loginFlow)}
}

func (s *Service) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = make(map[string]catalogCache)
	s.logins = make(map[string]*loginFlow)
}

type pluginConfig struct {
	PublicBaseURL string `yaml:"public-base-url"`
}

func (s *Service) Configure(raw []byte) error {
	var cfg pluginConfig
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("decode OpenRouter plugin config: %w", err)
		}
	}

	publicBaseURL := strings.TrimSpace(cfg.PublicBaseURL)
	if publicBaseURL != "" {
		normalized, err := normalizeLoginBaseURL(publicBaseURL, true)
		if err != nil {
			return fmt.Errorf("invalid public-base-url: %w", err)
		}
		publicBaseURL = normalized
	}

	s.mu.Lock()
	s.publicBaseURL = publicBaseURL
	s.mu.Unlock()
	return nil
}

func (s *Service) loginBaseURL(hostBaseURL string) (string, error) {
	s.mu.Lock()
	publicBaseURL := s.publicBaseURL
	s.mu.Unlock()
	if publicBaseURL != "" {
		return publicBaseURL, nil
	}
	return normalizeLoginBaseURL(hostBaseURL, false)
}

func normalizeLoginBaseURL(raw string, requireHTTPS bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("URL must be absolute")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("URL must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("URL must not contain a query or fragment")
	}
	if requireHTTPS && parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("URL must not contain a path")
	}
	if requireHTTPS {
		if !strings.EqualFold(parsed.Scheme, "https") {
			return "", fmt.Errorf("URL must use HTTPS")
		}
	} else if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("URL must use HTTP or HTTPS")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Path = ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func (s *Service) request(ctx context.Context, callbackID, apiKey, method, target string, body []byte) (Response, error) {
	return s.host.Do(ctx, callbackID, Request{Method: method, URL: target, Headers: requestHeaders(apiKey, false), Body: body})
}

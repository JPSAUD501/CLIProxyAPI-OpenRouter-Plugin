package openrouter

import (
	"context"
	"sync"
	"time"
)

const (
	providerID = "openrouter"
	apiBaseURL = "https://openrouter.ai/api/v1"
	cacheTTL   = 30 * time.Minute
)

type Service struct {
	host Host

	mu     sync.Mutex
	cache  map[string]catalogCache
	logins map[string]*loginFlow
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

func (s *Service) Configure([]byte) error { return nil }

func (s *Service) request(ctx context.Context, callbackID, apiKey, method, target string, body []byte) (Response, error) {
	return s.host.Do(ctx, callbackID, Request{Method: method, URL: target, Headers: requestHeaders(apiKey, false), Body: body})
}

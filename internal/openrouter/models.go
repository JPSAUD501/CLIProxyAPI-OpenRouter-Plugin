package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type modelsResponse struct {
	Data       []upstreamModel `json:"data"`
	TotalCount int             `json:"total_count"`
	Links      struct {
		Next *string `json:"next"`
	} `json:"links"`
}

type upstreamModel struct {
	ID            string            `json:"id"`
	CanonicalSlug string            `json:"canonical_slug"`
	Name          string            `json:"name"`
	Created       int64             `json:"created"`
	Description   string            `json:"description"`
	ContextLength int64             `json:"context_length"`
	Architecture  modelArchitecture `json:"architecture"`
	TopProvider   topProvider       `json:"top_provider"`
	Parameters    []string          `json:"supported_parameters"`
	Reasoning     reasoningInfo     `json:"reasoning"`
}

type modelArchitecture struct {
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

type topProvider struct {
	MaxCompletionTokens int64 `json:"max_completion_tokens"`
}

type reasoningInfo struct {
	Mandatory        bool     `json:"mandatory"`
	DefaultEnabled   bool     `json:"default_enabled"`
	SupportedEfforts []string `json:"supported_efforts"`
	DefaultEffort    string   `json:"default_effort"`
}

type catalogCache struct {
	ExpiresAt time.Time
	Models    []pluginapi.ModelInfo
	Aliases   map[string]string
}

func (s *Service) StaticModels() pluginapi.ModelResponse {
	return pluginapi.ModelResponse{Provider: providerID}
}

func (s *Service) ModelsForAuth(ctx context.Context, callbackID string, req pluginapi.AuthModelRequest) (pluginapi.ModelResponse, error) {
	storage, err := parseStorage(req.StorageJSON)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	models, _, err := s.catalog(ctx, callbackID, req.AuthID, storage, false)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	return pluginapi.ModelResponse{Provider: providerID, Models: models}, nil
}

func (s *Service) catalog(ctx context.Context, callbackID, authID string, storage authStorage, force bool) ([]pluginapi.ModelInfo, map[string]string, error) {
	now := time.Now().UTC()
	s.mu.Lock()
	cached, found := s.cache[authID]
	if found && !force && now.Before(cached.ExpiresAt) {
		models, aliases := cloneModels(cached.Models), cloneAliases(cached.Aliases)
		s.mu.Unlock()
		return models, aliases, nil
	}
	s.mu.Unlock()

	apiKey, err := storage.apiKey()
	if err != nil {
		return nil, nil, err
	}
	upstream, status, err := s.fetchModels(ctx, callbackID, apiKey)
	apiKey = ""
	if err != nil {
		if found && (status == http.StatusTooManyRequests || status >= 500 || status == 0) {
			return cloneModels(cached.Models), cloneAliases(cached.Aliases), nil
		}
		return nil, nil, err
	}
	models, aliases := normalizeModels(upstream)
	entry := catalogCache{ExpiresAt: now.Add(cacheTTL), Models: models, Aliases: aliases}
	s.mu.Lock()
	s.cache[authID] = entry
	s.mu.Unlock()
	return cloneModels(models), cloneAliases(aliases), nil
}

func (s *Service) fetchModels(ctx context.Context, callbackID, apiKey string) ([]upstreamModel, int, error) {
	next := apiBaseURL + "/models/user"
	base, _ := url.Parse(apiBaseURL + "/")
	out := make([]upstreamModel, 0, 512)
	seenPages := make(map[string]struct{})
	for page := 0; next != ""; page++ {
		if page >= 20 {
			return nil, 0, statusError("invalid_catalog", "OpenRouter model pagination exceeded 20 pages", 502, false)
		}
		parsed, errURL := url.Parse(next)
		if errURL != nil {
			return nil, 0, statusError("invalid_catalog", "OpenRouter returned an invalid model pagination URL", 502, false)
		}
		if !parsed.IsAbs() {
			parsed = base.ResolveReference(parsed)
		}
		next = parsed.String()
		if _, exists := seenPages[next]; exists {
			return nil, 0, statusError("invalid_catalog", "OpenRouter model pagination contains a cycle", 502, false)
		}
		seenPages[next] = struct{}{}
		if parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "openrouter.ai") || !strings.HasPrefix(parsed.Path, "/api/v1/") {
			return nil, 0, statusError("invalid_catalog", "OpenRouter returned an invalid model pagination URL", 502, false)
		}
		resp, errDo := s.request(ctx, callbackID, apiKey, http.MethodGet, next, nil)
		if errDo != nil {
			return nil, 0, fmt.Errorf("request OpenRouter model catalog: %w", errDo)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, resp.StatusCode, upstreamError(resp.StatusCode, resp.Body)
		}
		var pageData modelsResponse
		if errDecode := json.Unmarshal(resp.Body, &pageData); errDecode != nil {
			return nil, resp.StatusCode, fmt.Errorf("decode OpenRouter model catalog: %w", errDecode)
		}
		out = append(out, pageData.Data...)
		next = ""
		if pageData.Links.Next != nil {
			next = strings.TrimSpace(*pageData.Links.Next)
		}
	}
	return out, http.StatusOK, nil
}

func normalizeModels(upstream []upstreamModel) ([]pluginapi.ModelInfo, map[string]string) {
	type candidate struct {
		model upstreamModel
		alias string
	}
	candidates := make([]candidate, 0, len(upstream))
	counts := make(map[string]int)
	seenNative := make(map[string]struct{}, len(upstream))
	for _, model := range upstream {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" || !containsFold(model.Architecture.OutputModalities, "text") {
			continue
		}
		nativeKey := strings.ToLower(model.ID)
		if _, exists := seenNative[nativeKey]; exists {
			continue
		}
		seenNative[nativeKey] = struct{}{}
		alias := modelAlias(model.ID)
		if alias == "" {
			continue
		}
		key := strings.ToLower(alias)
		counts[key]++
		candidates = append(candidates, candidate{model: model, alias: alias})
	}
	models := make([]pluginapi.ModelInfo, 0, len(candidates))
	aliases := make(map[string]string, len(candidates))
	for _, item := range candidates {
		if counts[strings.ToLower(item.alias)] != 1 {
			continue
		}
		model := item.model
		owner := strings.SplitN(model.ID, "/", 2)[0]
		display := strings.TrimSpace(model.Name)
		if display == "" {
			display = item.alias
		}
		var thinking *pluginapi.ThinkingSupport
		if len(model.Reasoning.SupportedEfforts) > 0 || model.Reasoning.Mandatory || model.Reasoning.DefaultEnabled {
			levels := uniqueStrings(model.Reasoning.SupportedEfforts)
			thinking = &pluginapi.ThinkingSupport{
				ZeroAllowed:    containsFold(levels, "none") && !model.Reasoning.Mandatory,
				DynamicAllowed: strings.TrimSpace(model.Reasoning.DefaultEffort) != "",
				Levels:         levels,
			}
		}
		models = append(models, pluginapi.ModelInfo{
			ID:                         item.alias,
			Object:                     "model",
			Created:                    model.Created,
			OwnedBy:                    owner,
			Type:                       providerID,
			DisplayName:                display,
			Name:                       model.ID,
			Version:                    model.CanonicalSlug,
			Description:                strings.TrimSpace(model.Description),
			OutputTokenLimit:           model.TopProvider.MaxCompletionTokens,
			SupportedGenerationMethods: []string{"chat", "responses", "messages"},
			ContextLength:              model.ContextLength,
			MaxCompletionTokens:        model.TopProvider.MaxCompletionTokens,
			SupportedParameters:        uniqueStrings(model.Parameters),
			SupportedInputModalities:   uniqueStrings(model.Architecture.InputModalities),
			SupportedOutputModalities:  uniqueStrings(model.Architecture.OutputModalities),
			Thinking:                   thinking,
		})
		aliases[strings.ToLower(item.alias)] = model.ID
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ID) < strings.ToLower(models[j].ID) })
	return models, aliases
}

func modelAlias(nativeID string) string {
	nativeID = strings.TrimSpace(nativeID)
	parts := strings.SplitN(nativeID, "/", 2)
	if len(parts) != 2 || strings.EqualFold(parts[0], providerID) {
		return nativeID
	}
	return strings.TrimSpace(parts[1])
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func cloneModels(in []pluginapi.ModelInfo) []pluginapi.ModelInfo {
	out := make([]pluginapi.ModelInfo, len(in))
	copy(out, in)
	for i := range out {
		out[i].SupportedGenerationMethods = append([]string(nil), in[i].SupportedGenerationMethods...)
		out[i].SupportedParameters = append([]string(nil), in[i].SupportedParameters...)
		out[i].SupportedInputModalities = append([]string(nil), in[i].SupportedInputModalities...)
		out[i].SupportedOutputModalities = append([]string(nil), in[i].SupportedOutputModalities...)
		if in[i].Thinking != nil {
			copyThinking := *in[i].Thinking
			copyThinking.Levels = append([]string(nil), in[i].Thinking.Levels...)
			out[i].Thinking = &copyThinking
		}
	}
	return out
}

func cloneAliases(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

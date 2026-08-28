package openrouter

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type ExecuteRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type HTTPRequest struct {
	pluginapi.ExecutorHTTPRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func (s *Service) Execute(ctx context.Context, req ExecuteRequest) (pluginapi.ExecutorResponse, error) {
	endpoint, err := endpointForFormat(firstNonEmpty(req.SourceFormat, req.Format))
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	storage, apiKey, native, body, err := s.prepareExecution(ctx, req)
	if err != nil {
		return pluginapi.ExecutorResponse{}, err
	}
	_ = storage
	resp, err := s.host.Do(ctx, req.HostCallbackID, Request{
		Method: http.MethodPost, URL: apiBaseURL + endpoint,
		Headers: modelRequestHeaders(req.Headers, apiKey, false), Body: body,
	})
	apiKey = ""
	if err != nil {
		return pluginapi.ExecutorResponse{}, fmt.Errorf("call OpenRouter: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return pluginapi.ExecutorResponse{}, upstreamError(resp.StatusCode, resp.Body)
	}
	payload, _ := rewriteResponseModels(resp.Body)
	return pluginapi.ExecutorResponse{Payload: payload, Headers: filterResponseHeaders(resp.Headers), Metadata: map[string]any{"openrouter_model": native}}, nil
}

func (s *Service) ExecuteStream(ctx context.Context, req ExecuteRequest) (http.Header, error) {
	if strings.TrimSpace(req.StreamID) == "" {
		return nil, statusError("invalid_request", "stream_id is required", 400, false)
	}
	format := firstNonEmpty(req.SourceFormat, req.Format)
	endpoint, err := endpointForFormat(format)
	if err != nil {
		return nil, err
	}
	_, apiKey, _, body, err := s.prepareExecution(ctx, req)
	if err != nil {
		return nil, err
	}
	upstream, err := s.host.OpenStream(ctx, req.HostCallbackID, Request{
		Method: http.MethodPost, URL: apiBaseURL + endpoint,
		Headers: modelRequestHeaders(req.Headers, apiKey, true), Body: body,
	})
	apiKey = ""
	if err != nil {
		return nil, fmt.Errorf("open OpenRouter stream: %w", err)
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 {
		body, collectErr := s.collectStreamError(ctx, upstream)
		if collectErr != nil {
			return nil, collectErr
		}
		return nil, upstreamError(upstream.StatusCode, body)
	}
	go s.pumpStream(req.StreamID, upstream, format)
	headers := filterResponseHeaders(upstream.Headers)
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	return headers, nil
}

func (s *Service) prepareExecution(ctx context.Context, req ExecuteRequest) (authStorage, string, string, []byte, error) {
	storage, err := parseStorage(req.StorageJSON)
	if err != nil {
		return authStorage{}, "", "", nil, err
	}
	apiKey, err := storage.apiKey()
	if err != nil {
		return authStorage{}, "", "", nil, err
	}
	models, aliases, err := s.catalog(ctx, req.HostCallbackID, req.AuthID, storage, false)
	if err != nil {
		apiKey = ""
		return authStorage{}, "", "", nil, err
	}
	modelID, effort, hasEffort := splitEffortSuffix(req.Model)
	native := aliases[strings.ToLower(modelID)]
	if native == "" {
		apiKey = ""
		return authStorage{}, "", "", nil, statusError("model_not_found", "OpenRouter model is not available for the selected credential", 404, false)
	}
	if hasEffort && !modelSupportsEffort(models, modelID, effort) {
		apiKey = ""
		return authStorage{}, "", "", nil, statusError("effort_not_supported", "OpenRouter reasoning effort is not supported for the selected model", 422, false)
	}
	body := req.Payload
	if len(body) == 0 {
		body = req.OriginalRequest
	}
	body, err = rewriteRequestModel(body, native)
	if err != nil {
		apiKey = ""
		return authStorage{}, "", "", nil, err
	}
	if hasEffort {
		body, err = rewriteRequestEffort(body, effort)
		if err != nil {
			apiKey = ""
			return authStorage{}, "", "", nil, err
		}
	}
	return storage, apiKey, native, body, nil
}

func (s *Service) CountTokens() (pluginapi.ExecutorResponse, error) {
	return pluginapi.ExecutorResponse{}, statusError("not_implemented", "OpenRouter does not document a token counting endpoint", http.StatusNotImplemented, false)
}

func (s *Service) HTTP(ctx context.Context, req HTTPRequest) (pluginapi.ExecutorHTTPResponse, error) {
	storage, err := parseStorage(req.StorageJSON)
	if err != nil {
		return pluginapi.ExecutorHTTPResponse{}, err
	}
	apiKey, err := storage.apiKey()
	if err != nil {
		return pluginapi.ExecutorHTTPResponse{}, err
	}
	target, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || target.Scheme != "https" || !strings.EqualFold(target.Host, "openrouter.ai") || !strings.HasPrefix(target.Path, "/api/v1/") {
		apiKey = ""
		return pluginapi.ExecutorHTTPResponse{}, statusError("forbidden_url", "executor HTTP URL is outside the OpenRouter API origin", http.StatusForbidden, false)
	}
	resp, err := s.host.Do(ctx, req.HostCallbackID, Request{Method: req.Method, URL: target.String(), Headers: modelRequestHeaders(req.Headers, apiKey, false), Body: append([]byte(nil), req.Body...)})
	apiKey = ""
	if err != nil {
		return pluginapi.ExecutorHTTPResponse{}, err
	}
	return pluginapi.ExecutorHTTPResponse{StatusCode: resp.StatusCode, Headers: filterResponseHeaders(resp.Headers), Body: resp.Body}, nil
}

func (s *Service) collectStreamError(ctx context.Context, stream Stream) ([]byte, error) {
	defer s.host.CloseStream(context.Background(), stream.ID)
	var body []byte
	for {
		chunk, err := s.host.ReadStream(ctx, stream.ID)
		if err != nil {
			return nil, err
		}
		body = append(body, chunk.Payload...)
		if chunk.Error != "" {
			return nil, fmt.Errorf("OpenRouter stream error: %s", chunk.Error)
		}
		if chunk.Done {
			return body, nil
		}
	}
}

func (s *Service) pumpStream(outputID string, upstream Stream, format string) {
	ctx := context.Background()
	var terminalErr error
	defer func() {
		_ = s.host.CloseStream(ctx, upstream.ID)
		message := ""
		if terminalErr != nil {
			message = terminalErr.Error()
		}
		s.host.CloseOutput(ctx, outputID, message)
	}()
	decoder := &sseDecoder{}
	for {
		chunk, err := s.host.ReadStream(ctx, upstream.ID)
		if err != nil {
			terminalErr = fmt.Errorf("read OpenRouter stream: %w", err)
			return
		}
		if chunk.Error != "" {
			terminalErr = fmt.Errorf("OpenRouter stream error: %s", chunk.Error)
			return
		}
		for _, frame := range decoder.Feed(chunk.Payload) {
			payload, emit := outputStreamPayload(frame, format)
			if !emit {
				continue
			}
			if err := s.host.Emit(ctx, outputID, payload); err != nil {
				terminalErr = err
				return
			}
		}
		if chunk.Done {
			if trailing := decoder.Flush(); len(trailing) > 0 {
				payload, emit := outputStreamPayload(trailing, format)
				if emit {
					if err := s.host.Emit(ctx, outputID, payload); err != nil {
						terminalErr = err
					}
				}
				if terminalErr != nil {
					return
				}
			}
			return
		}
	}
}

func outputStreamPayload(frame []byte, format string) ([]byte, bool) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "openai", "chat-completions":
		return openAIStreamPayload(frame)
	default:
		return rewriteSSEFrame(frame), true
	}
}

func endpointForFormat(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "openai", "chat-completions":
		return "/chat/completions", nil
	case "openai-response", "openai-responses", "responses":
		return "/responses", nil
	case "claude", "anthropic", "anthropic-messages":
		return "/messages", nil
	default:
		return "", statusError("unsupported_format", fmt.Sprintf("OpenRouter does not support executor format %q", format), 422, false)
	}
}

func requestHeaders(apiKey string, stream bool) http.Header {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+apiKey)
	headers.Set("Accept", "application/json")
	headers.Set("Accept-Encoding", "identity")
	if stream {
		headers.Set("Accept", "text/event-stream")
	}
	headers.Set("Content-Type", "application/json")
	return headers
}

func modelRequestHeaders(in http.Header, apiKey string, stream bool) http.Header {
	headers := http.Header{}
	for key, values := range in {
		switch strings.ToLower(key) {
		case "authorization", "x-api-key", "host", "content-length", "connection", "proxy-authorization", "transfer-encoding", "accept-encoding":
			continue
		}
		for _, value := range values {
			headers.Add(key, value)
		}
	}
	base := requestHeaders(apiKey, stream)
	for key, values := range base {
		headers[key] = values
	}
	return headers
}

func filterResponseHeaders(in http.Header) http.Header {
	out := http.Header{}
	for key, values := range in {
		switch strings.ToLower(key) {
		case "content-type", "content-encoding", "cache-control", "retry-after", "x-request-id", "x-openrouter-request-id":
			out[key] = append([]string(nil), values...)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

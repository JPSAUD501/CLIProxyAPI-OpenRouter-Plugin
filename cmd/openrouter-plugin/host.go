package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JPSAUD501/CLIProxyAPI-OpenRouter-Plugin/internal/openrouter"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

type hostTransport struct{}

type hostHTTPRequest struct {
	HostCallbackID string              `json:"host_callback_id,omitempty"`
	Method         string              `json:"method"`
	URL            string              `json:"url"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           []byte              `json:"body,omitempty"`
}

type hostHTTPResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       []byte              `json:"Body"`
}

type hostHTTPStreamResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers,omitempty"`
	StreamID   string              `json:"stream_id"`
}

type hostHTTPStreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

func (hostTransport) Do(_ context.Context, callbackID string, req openrouter.Request) (openrouter.Response, error) {
	raw, err := callHost(pluginabi.MethodHostHTTPDo, hostHTTPRequest{HostCallbackID: callbackID, Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body})
	if err != nil {
		return openrouter.Response{}, err
	}
	var resp hostHTTPResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return openrouter.Response{}, fmt.Errorf("decode host HTTP response: %w", err)
	}
	return openrouter.Response{StatusCode: resp.StatusCode, Headers: resp.Headers, Body: resp.Body}, nil
}

func (hostTransport) OpenStream(_ context.Context, callbackID string, req openrouter.Request) (openrouter.Stream, error) {
	raw, err := callHost(pluginabi.MethodHostHTTPDoStream, hostHTTPRequest{HostCallbackID: callbackID, Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body})
	if err != nil {
		return openrouter.Stream{}, err
	}
	var resp hostHTTPStreamResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return openrouter.Stream{}, fmt.Errorf("decode host HTTP stream response: %w", err)
	}
	if strings.TrimSpace(resp.StreamID) == "" {
		return openrouter.Stream{}, fmt.Errorf("host HTTP stream response has no stream_id")
	}
	return openrouter.Stream{StatusCode: resp.StatusCode, Headers: resp.Headers, ID: resp.StreamID}, nil
}

func (hostTransport) ReadStream(_ context.Context, streamID string) (openrouter.StreamChunk, error) {
	raw, err := callHost(pluginabi.MethodHostHTTPStreamRead, map[string]string{"stream_id": streamID})
	if err != nil {
		return openrouter.StreamChunk{}, err
	}
	var resp hostHTTPStreamReadResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return openrouter.StreamChunk{}, fmt.Errorf("decode host HTTP stream chunk: %w", err)
	}
	return openrouter.StreamChunk{Payload: resp.Payload, Error: resp.Error, Done: resp.Done}, nil
}

func (hostTransport) CloseStream(_ context.Context, streamID string) error {
	_, err := callHost(pluginabi.MethodHostHTTPStreamClose, map[string]string{"stream_id": streamID})
	return err
}

func (hostTransport) Emit(_ context.Context, streamID string, payload []byte) error {
	_, err := callHost(pluginabi.MethodHostStreamEmit, map[string]any{"stream_id": streamID, "payload": payload})
	return err
}

func (hostTransport) CloseOutput(_ context.Context, streamID, message string) {
	_, _ = callHost(pluginabi.MethodHostStreamClose, map[string]string{"stream_id": streamID, "error": message})
}

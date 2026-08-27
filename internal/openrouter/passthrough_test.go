package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

type mockHost struct {
	mu          sync.Mutex
	do          func(context.Context, string, Request) (Response, error)
	open        func(context.Context, string, Request) (Stream, error)
	chunks      []StreamChunk
	emitted     bytes.Buffer
	outputError string
	closed      chan struct{}
}

func (h *mockHost) Do(ctx context.Context, callback string, req Request) (Response, error) {
	if h.do == nil {
		return Response{}, errors.New("unexpected request")
	}
	return h.do(ctx, callback, req)
}

func (h *mockHost) OpenStream(ctx context.Context, callback string, req Request) (Stream, error) {
	if h.open == nil {
		return Stream{}, errors.New("unexpected stream")
	}
	return h.open(ctx, callback, req)
}

func (h *mockHost) ReadStream(context.Context, string) (StreamChunk, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.chunks) == 0 {
		return StreamChunk{}, errors.New("no stream chunk")
	}
	chunk := h.chunks[0]
	h.chunks = h.chunks[1:]
	return chunk, nil
}

func (h *mockHost) CloseStream(context.Context, string) error { return nil }

func (h *mockHost) Emit(_ context.Context, _ string, payload []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, _ = h.emitted.Write(payload)
	return nil
}

func (h *mockHost) CloseOutput(_ context.Context, _ string, message string) {
	h.mu.Lock()
	h.outputError = message
	h.mu.Unlock()
	if h.closed != nil {
		close(h.closed)
	}
}

func TestExecuteChangesOnlyModelAcrossNativeProtocols(t *testing.T) {
	storage, err := newStorage("test-key-not-a-secret", "test")
	if err != nil {
		t.Fatal(err)
	}
	rawStorage, _ := json.Marshal(storage)
	original := []byte(`{"model":"claude-opus-5","messages":[{"role":"developer","content":"keep developer"},{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}],"tools":[{"type":"function","function":{"name":"lookup","description":"Lookup","parameters":{"type":"object"}}}],"reasoning":{"effort":"high"},"provider":{"allow_fallbacks":false},"custom":{"nested":true}}`)

	for _, test := range []struct {
		format string
		path   string
	}{{"openai", "/chat/completions"}, {"openai-response", "/responses"}, {"claude", "/messages"}} {
		t.Run(test.format, func(t *testing.T) {
			var captured Request
			host := &mockHost{do: func(_ context.Context, _ string, req Request) (Response, error) {
				if req.URL == apiBaseURL+"/models/user" {
					return modelCatalogResponse("anthropic/claude-opus-5"), nil
				}
				captured = req
				return Response{StatusCode: http.StatusOK, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: []byte(`{"id":"result","model":"anthropic/claude-opus-5","output_text":"ok"}`)}, nil
			}}
			service := New(host)
			response, errExecute := service.Execute(context.Background(), ExecuteRequest{ExecutorRequest: pluginapi.ExecutorRequest{
				AuthID: "auth", Model: "claude-opus-5", SourceFormat: test.format, Payload: original,
				StorageJSON: rawStorage, Headers: http.Header{"Authorization": []string{"Bearer client-key"}, "Accept-Encoding": []string{"gzip"}, "X-OpenRouter-Metadata": []string{"enabled"}},
			}})
			if errExecute != nil {
				t.Fatal(errExecute)
			}
			if captured.URL != apiBaseURL+test.path {
				t.Fatalf("request URL = %q", captured.URL)
			}
			if captured.Headers.Get("Authorization") != "Bearer test-key-not-a-secret" || captured.Headers.Get("X-OpenRouter-Metadata") != "enabled" {
				t.Fatalf("unexpected forwarded headers: %#v", captured.Headers)
			}
			if captured.Headers.Get("Accept-Encoding") != "identity" {
				t.Fatalf("upstream content encoding was not pinned to identity: %#v", captured.Headers)
			}
			assertOnlyModelChanged(t, original, captured.Body, "anthropic/claude-opus-5")
			var responseBody map[string]any
			if err := json.Unmarshal(response.Payload, &responseBody); err != nil {
				t.Fatal(err)
			}
			if responseBody["model"] != "claude-opus-5" {
				t.Fatalf("response model = %#v", responseBody["model"])
			}
		})
	}
}

func TestExecuteAppliesSupportedEffortSuffixAcrossNativeProtocols(t *testing.T) {
	storage, err := newStorage("test-key-not-a-secret", "test")
	if err != nil {
		t.Fatal(err)
	}
	rawStorage, _ := json.Marshal(storage)
	original := []byte(`{"model":"reasoning-model(high)","messages":[{"role":"user","content":"hello"}],"custom":{"keep":true}}`)

	for _, format := range []string{"openai", "openai-response", "claude"} {
		t.Run(format, func(t *testing.T) {
			var captured Request
			host := &mockHost{do: func(_ context.Context, _ string, req Request) (Response, error) {
				if req.URL == apiBaseURL+"/models/user" {
					body, _ := json.Marshal(modelsResponse{Data: []upstreamModel{{
						ID: "vendor/reasoning-model", Architecture: modelArchitecture{OutputModalities: []string{"text"}},
						Reasoning: reasoningInfo{SupportedEfforts: []string{"low", "high"}},
					}}})
					return Response{StatusCode: http.StatusOK, Body: body}, nil
				}
				captured = req
				return Response{StatusCode: http.StatusOK, Body: []byte(`{"model":"vendor/reasoning-model","content":[]}`)}, nil
			}}
			_, errExecute := New(host).Execute(context.Background(), ExecuteRequest{ExecutorRequest: pluginapi.ExecutorRequest{
				AuthID: "auth", Model: "reasoning-model(high)", SourceFormat: format, Payload: original, StorageJSON: rawStorage,
			}})
			if errExecute != nil {
				t.Fatal(errExecute)
			}
			if got := gjson.GetBytes(captured.Body, "model").String(); got != "vendor/reasoning-model" {
				t.Fatalf("upstream model = %q", got)
			}
			if got := gjson.GetBytes(captured.Body, "reasoning.effort").String(); got != "high" {
				t.Fatalf("upstream reasoning effort = %q", got)
			}
			if !gjson.GetBytes(captured.Body, "custom.keep").Bool() {
				t.Fatalf("unrelated request content changed: %s", captured.Body)
			}
		})
	}
}

func TestExecuteRejectsUnadvertisedEffortSuffix(t *testing.T) {
	storage, err := newStorage("test-key-not-a-secret", "test")
	if err != nil {
		t.Fatal(err)
	}
	rawStorage, _ := json.Marshal(storage)
	host := &mockHost{do: func(_ context.Context, _ string, req Request) (Response, error) {
		if req.URL != apiBaseURL+"/models/user" {
			return Response{}, errors.New("unexpected inference request")
		}
		body, _ := json.Marshal(modelsResponse{Data: []upstreamModel{{
			ID: "vendor/reasoning-model", Architecture: modelArchitecture{OutputModalities: []string{"text"}},
			Reasoning: reasoningInfo{SupportedEfforts: []string{"low"}},
		}}})
		return Response{StatusCode: http.StatusOK, Body: body}, nil
	}}
	_, err = New(host).Execute(context.Background(), ExecuteRequest{ExecutorRequest: pluginapi.ExecutorRequest{
		AuthID: "auth", Model: "reasoning-model(high)", SourceFormat: "claude",
		Payload: []byte(`{"model":"reasoning-model(high)","messages":[]}`), StorageJSON: rawStorage,
	}})
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.Code != "effort_not_supported" || statusErr.HTTPStatus != http.StatusUnprocessableEntity {
		t.Fatalf("execute error = %#v", err)
	}
}

func TestExecuteStreamPreservesChunkBoundariesAndText(t *testing.T) {
	storage, err := newStorage("stream-key", "stream")
	if err != nil {
		t.Fatal(err)
	}
	rawStorage, _ := json.Marshal(storage)
	closed := make(chan struct{})
	host := &mockHost{closed: closed}
	host.do = func(_ context.Context, _ string, req Request) (Response, error) {
		if req.URL == apiBaseURL+"/models/user" {
			return modelCatalogResponse("anthropic/claude-opus-5"), nil
		}
		return Response{}, errors.New("unexpected request")
	}
	host.open = func(_ context.Context, _ string, req Request) (Stream, error) {
		assertOnlyModelChanged(t, []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hello"}],"stream":true}`), req.Body, "anthropic/claude-opus-5")
		return Stream{StatusCode: http.StatusOK, ID: "upstream", Headers: http.Header{"Content-Type": []string{"text/event-stream"}}}, nil
	}
	host.chunks = []StreamChunk{
		{Payload: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"anthropic/claude-opus-5\"}}\n")},
		{Payload: []byte("\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello world\"}}\n\n")},
		{Payload: []byte("data: [DONE]\n\n"), Done: true},
	}

	service := New(host)
	_, err = service.ExecuteStream(context.Background(), ExecuteRequest{ExecutorRequest: pluginapi.ExecutorRequest{
		AuthID: "auth", Model: "claude-opus-5", SourceFormat: "claude", StorageJSON: rawStorage,
		Payload: []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hello"}],"stream":true}`),
	}, StreamID: "output"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("stream pump did not finish")
	}

	host.mu.Lock()
	emitted := host.emitted.String()
	outputError := host.outputError
	host.mu.Unlock()
	if outputError != "" {
		t.Fatalf("stream closed with error: %s", outputError)
	}
	if !bytes.Contains([]byte(emitted), []byte(`"model":"claude-opus-5"`)) || !bytes.Contains([]byte(emitted), []byte(`"text":"hello world"`)) || !bytes.HasSuffix([]byte(emitted), []byte("data: [DONE]\n\n")) {
		t.Fatalf("unexpected emitted SSE: %s", emitted)
	}
}

func TestRequestModelRewritePreservesLongHistory(t *testing.T) {
	messages := make([]map[string]any, 200)
	for i := range messages {
		messages[i] = map[string]any{"role": "user", "content": i}
	}
	original, _ := json.Marshal(map[string]any{"model": "alias", "messages": messages, "tools": []any{}, "reasoning": map[string]any{"effort": "low"}})
	rewritten, err := rewriteRequestModel(original, "vendor/native")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(rewritten, &body); err != nil {
		t.Fatal(err)
	}
	if got := len(body["messages"].([]any)); got != 200 {
		t.Fatalf("history length = %d", got)
	}
}

func TestRequestModelRewriteIsBytePreservingOutsideModel(t *testing.T) {
	original := []byte("{\n  \"model\" : \"alias\", \"large_integer\": 900719925474099312345, \"custom\" : { \"value\" : 1.2300 }\n}")
	rewritten, err := rewriteRequestModel(original, "vendor/native")
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(original, []byte(`"alias"`), []byte(`"vendor/native"`), 1)
	if !bytes.Equal(rewritten, want) {
		t.Fatalf("targeted rewrite changed unrelated JSON\nwant: %s\ngot:  %s", want, rewritten)
	}
}

func TestExecutePropagatesCancellationToHostTransport(t *testing.T) {
	storage, err := newStorage("cancel-key", "cancel")
	if err != nil {
		t.Fatal(err)
	}
	rawStorage, _ := json.Marshal(storage)
	host := &mockHost{do: func(ctx context.Context, _ string, req Request) (Response, error) {
		if req.URL == apiBaseURL+"/models/user" {
			return modelCatalogResponse("vendor/model"), nil
		}
		<-ctx.Done()
		return Response{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = New(host).Execute(ctx, ExecuteRequest{ExecutorRequest: pluginapi.ExecutorRequest{
		AuthID: "auth", Model: "model", SourceFormat: "openai", StorageJSON: rawStorage,
		Payload: []byte(`{"model":"model","messages":[{"role":"user","content":"hello"}]}`),
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("execute error = %v, want context cancellation", err)
	}
}

func assertOnlyModelChanged(t *testing.T, original, rewritten []byte, native string) {
	t.Helper()
	var before, after map[string]any
	if err := json.Unmarshal(original, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rewritten, &after); err != nil {
		t.Fatal(err)
	}
	before["model"] = native
	if !jsonEqual(before, after) {
		t.Fatalf("request changed beyond model\nbefore: %s\nafter:  %s", original, rewritten)
	}
}

func jsonEqual(left, right any) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return bytes.Equal(a, b)
}

func modelCatalogResponse(native string) Response {
	body, _ := json.Marshal(modelsResponse{Data: []upstreamModel{{ID: native, Architecture: modelArchitecture{OutputModalities: []string{"text"}}}}})
	return Response{StatusCode: http.StatusOK, Body: body}
}

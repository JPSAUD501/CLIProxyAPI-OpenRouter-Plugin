package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestNormalizeModelsUsesShortVendorAliasesAndPreservesOpenRouterModels(t *testing.T) {
	models, aliases := normalizeModels([]upstreamModel{
		{
			ID: "anthropic/claude-opus-5", Name: "Claude Opus 5", ContextLength: 258000,
			Architecture: modelArchitecture{InputModalities: []string{"text", "image"}, OutputModalities: []string{"text"}},
			TopProvider:  topProvider{MaxCompletionTokens: 64000}, Parameters: []string{"tools", "reasoning", "structured_outputs"},
			Reasoning: reasoningInfo{SupportedEfforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},
		},
		{ID: "openrouter/auto", Name: "Auto Router", Architecture: modelArchitecture{OutputModalities: []string{"text"}}},
		{ID: "vendor/image-only", Architecture: modelArchitecture{OutputModalities: []string{"image"}}},
	})

	if len(models) != 4 {
		t.Fatalf("got %d models, want 4", len(models))
	}
	if got := aliases["claude-opus-5"]; got != "anthropic/claude-opus-5" {
		t.Fatalf("short alias maps to %q", got)
	}
	if got := aliases["openrouter/auto"]; got != "openrouter/auto" {
		t.Fatalf("virtual model maps to %q", got)
	}
	if got := aliases["claude-opus-5:nitro"]; got != "anthropic/claude-opus-5:nitro" {
		t.Fatalf("Nitro alias maps to %q", got)
	}
	if got := aliases["openrouter/auto:nitro"]; got != "openrouter/auto:nitro" {
		t.Fatalf("virtual Nitro model maps to %q", got)
	}

	var opusFound bool
	for _, model := range models {
		if model.ID != "claude-opus-5" {
			continue
		}
		opusFound = true
		if model.Name != "anthropic/claude-opus-5" || model.ContextLength != 258000 || model.MaxCompletionTokens != 64000 {
			t.Fatalf("unexpected Opus metadata: %#v", model)
		}
		if model.Thinking == nil || len(model.Thinking.Levels) != 3 || !model.Thinking.DynamicAllowed {
			t.Fatalf("reasoning metadata was not preserved: %#v", model.Thinking)
		}
	}
	if !opusFound {
		t.Fatal("short Opus alias was not registered")
	}
}

func TestNormalizeModelsOmitsOnlyAmbiguousShortAlias(t *testing.T) {
	models, aliases := normalizeModels([]upstreamModel{
		{ID: "vendor-a/shared", Architecture: modelArchitecture{OutputModalities: []string{"text"}}},
		{ID: "vendor-b/shared", Architecture: modelArchitecture{OutputModalities: []string{"text"}}},
		{ID: "vendor-c/unique", Architecture: modelArchitecture{OutputModalities: []string{"text"}}},
		{ID: "vendor-c/unique", Architecture: modelArchitecture{OutputModalities: []string{"text"}}},
	})

	if len(models) != 2 || models[0].ID != "unique" || models[1].ID != "unique:nitro" {
		t.Fatalf("unexpected normalized models: %#v", models)
	}
	if _, exists := aliases["shared"]; exists {
		t.Fatal("ambiguous alias must not be registered")
	}
}

func TestNormalizeModelsAppendsNitroToStaticVariants(t *testing.T) {
	models, aliases := normalizeModels([]upstreamModel{{
		ID: "z-ai/glm-5.2:free", Name: "GLM 5.2 Free",
		Architecture: modelArchitecture{OutputModalities: []string{"text"}},
		Reasoning:    reasoningInfo{SupportedEfforts: []string{"low", "high"}},
	}})

	if len(models) != 2 || models[0].ID != "glm-5.2:free" || models[1].ID != "glm-5.2:free:nitro" {
		t.Fatalf("unexpected static variant models: %#v", models)
	}
	if got := aliases["glm-5.2:free:nitro"]; got != "z-ai/glm-5.2:free:nitro" {
		t.Fatalf("stacked Nitro alias maps to %q", got)
	}
	if models[1].Thinking == nil || len(models[1].Thinking.Levels) != 2 {
		t.Fatalf("Nitro reasoning metadata was not copied: %#v", models[1].Thinking)
	}
}

func TestFetchModelsAcceptsSafeRelativePagination(t *testing.T) {
	page := 0
	host := &mockHost{do: func(_ context.Context, _ string, req Request) (Response, error) {
		page++
		response := modelsResponse{Data: []upstreamModel{{ID: "vendor/model", Architecture: modelArchitecture{OutputModalities: []string{"text"}}}}}
		if page == 1 {
			next := "/api/v1/models/user?cursor=next"
			response.Links.Next = &next
		} else if req.URL != apiBaseURL+"/models/user?cursor=next" {
			t.Fatalf("second page URL = %q", req.URL)
		}
		body, _ := json.Marshal(response)
		return Response{StatusCode: http.StatusOK, Body: body}, nil
	}}
	models, status, err := New(host).fetchModels(context.Background(), "callback", "key")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || page != 2 || len(models) != 2 {
		t.Fatalf("status=%d pages=%d models=%d", status, page, len(models))
	}
}

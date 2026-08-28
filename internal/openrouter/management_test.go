package openrouter

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestLoginHTMLStartsEmptyAndDoesNotExposeAKey(t *testing.T) {
	html := loginHTML("state-value", "public-key", "")
	inputStart := strings.Index(html, `<input id="api-key"`)
	if inputStart < 0 {
		t.Fatal("API key input was not rendered")
	}
	inputEnd := strings.Index(html[inputStart:], ">")
	if inputEnd < 0 {
		t.Fatal("API key input was not closed")
	}
	if strings.Contains(html[inputStart:inputStart+inputEnd], ` value=`) {
		t.Fatal("login field must not have a prefilled value")
	}
	for _, required := range []string{`type="password"`, `autocomplete="off"`, `crypto.subtle.encrypt`, `RSA-OAEP`} {
		if !strings.Contains(html, required) {
			t.Fatalf("login HTML is missing %q", required)
		}
	}
	response := htmlResponse(200, html)
	if response.Headers.Get("Cache-Control") != "no-store" || response.Headers.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(response.Headers.Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatalf("security headers are incomplete: %#v", response.Headers)
	}
}

func TestModelCapabilitiesReturnsAuthenticatedCatalogEfforts(t *testing.T) {
	service := New(nil)
	service.cache["first"] = catalogCache{Models: []pluginapi.ModelInfo{
		{ID: "reasoning-model", Thinking: &pluginapi.ThinkingSupport{Levels: []string{"high", "low", "future"}}},
		{ID: "reasoning-model:nitro", Thinking: &pluginapi.ThinkingSupport{Levels: []string{"high", "low", "future"}}},
		{ID: "plain-model"},
	}}
	service.cache["second"] = catalogCache{Models: []pluginapi.ModelInfo{
		{ID: "reasoning-model", Thinking: &pluginapi.ThinkingSupport{Levels: []string{"max", "LOW"}}},
	}}

	response, err := service.Management("", pluginapi.ManagementRequest{
		Method: http.MethodGet, Path: "/v0/management/plugins/openrouter/model-capabilities",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response metadata: status=%d headers=%v", response.StatusCode, response.Headers)
	}
	var payload modelCapabilitiesResponse
	if err = json.Unmarshal(response.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Provider != providerID || payload.CatalogModelCount != 3 || len(payload.Models) != 2 {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	model := payload.Models[0]
	if model.ModelID != "reasoning-model" || strings.Join(model.EffortLevels, ",") != "low,high,max" {
		t.Fatalf("unexpected model capabilities: %#v", model)
	}
	nitro := payload.Models[1]
	if nitro.ModelID != "reasoning-model:nitro" || strings.Join(nitro.EffortLevels, ",") != "low,high" {
		t.Fatalf("unexpected Nitro capabilities: %#v", nitro)
	}
}

func TestModelCapabilitiesRejectsUnavailableCatalog(t *testing.T) {
	service := New(nil)
	response, err := service.Management("", pluginapi.ManagementRequest{
		Method: http.MethodGet, Path: "/v0/management/plugins/openrouter/model-capabilities",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(response.Body), "openrouter_catalog_unavailable") {
		t.Fatalf("unexpected response: status=%d body=%s", response.StatusCode, response.Body)
	}
}

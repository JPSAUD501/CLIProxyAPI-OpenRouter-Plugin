package openrouter

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func (s *Service) Management(callbackID string, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	switch req.Path {
	case "/v0/management/plugins/openrouter/model-capabilities":
		return s.modelCapabilities()
	case "/v0/resource/plugins/openrouter/login":
		return s.loginPage(req)
	case "/v0/resource/plugins/openrouter/connect":
		return s.connect(callbackID, req)
	default:
		return pluginapi.ManagementResponse{StatusCode: http.StatusNotFound, Headers: textHeaders(), Body: []byte("Not found")}, nil
	}
}

type modelCapability struct {
	ModelID      string   `json:"model_id"`
	EffortLevels []string `json:"effort_levels"`
}

type modelCapabilitiesResponse struct {
	Provider          string            `json:"provider"`
	CatalogModelCount int               `json:"catalog_model_count"`
	Models            []modelCapability `json:"models"`
}

var suffixEffortOrder = map[string]int{
	"none": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5, "max": 6,
}

func (s *Service) modelCapabilities() (pluginapi.ManagementResponse, error) {
	s.mu.Lock()
	caches := make([]catalogCache, 0, len(s.cache))
	for _, cached := range s.cache {
		caches = append(caches, cached)
	}
	s.mu.Unlock()

	catalogModels := make(map[string]struct{})
	type capabilitySet struct {
		modelID string
		levels  map[string]struct{}
	}
	capabilities := make(map[string]*capabilitySet)
	for _, cached := range caches {
		for _, model := range cached.Models {
			modelID := strings.TrimSpace(model.ID)
			if modelID == "" {
				continue
			}
			key := strings.ToLower(modelID)
			catalogModels[key] = struct{}{}
			if model.Thinking == nil {
				continue
			}
			for _, rawLevel := range model.Thinking.Levels {
				level := strings.ToLower(strings.TrimSpace(rawLevel))
				if _, supported := suffixEffortOrder[level]; !supported {
					continue
				}
				capability := capabilities[key]
				if capability == nil {
					capability = &capabilitySet{modelID: modelID, levels: make(map[string]struct{})}
					capabilities[key] = capability
				}
				capability.levels[level] = struct{}{}
			}
		}
	}

	if len(catalogModels) == 0 {
		return jsonManagementResponse(http.StatusServiceUnavailable, map[string]string{"error": "openrouter_catalog_unavailable"})
	}

	models := make([]modelCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		levels := make([]string, 0, len(capability.levels))
		for level := range capability.levels {
			levels = append(levels, level)
		}
		sort.Slice(levels, func(i, j int) bool { return suffixEffortOrder[levels[i]] < suffixEffortOrder[levels[j]] })
		models = append(models, modelCapability{ModelID: capability.modelID, EffortLevels: levels})
	}
	sort.Slice(models, func(i, j int) bool { return strings.ToLower(models[i].ModelID) < strings.ToLower(models[j].ModelID) })

	return jsonManagementResponse(http.StatusOK, modelCapabilitiesResponse{
		Provider: providerID, CatalogModelCount: len(catalogModels), Models: models,
	})
}

func jsonManagementResponse(status int, value any) (pluginapi.ManagementResponse, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	return pluginapi.ManagementResponse{StatusCode: status, Headers: http.Header{
		"Cache-Control": []string{"no-store"},
		"Content-Type":  []string{"application/json; charset=utf-8"},
	}, Body: body}, nil
}

func (s *Service) loginPage(req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	state := strings.TrimSpace(req.Query.Get("state"))
	s.mu.Lock()
	flow, found := s.logins[state]
	s.mu.Unlock()
	if !found || time.Now().UTC().After(flow.ExpiresAt) {
		return htmlResponse(http.StatusBadRequest, messagePage("Connection expired", "Start a new OpenRouter connection from the Management Center.")), nil
	}
	return htmlResponse(http.StatusOK, loginHTML(state, flow.PublicKey, "")), nil
}

func (s *Service) connect(callbackID string, req pluginapi.ManagementRequest) (pluginapi.ManagementResponse, error) {
	state := strings.TrimSpace(req.Query.Get("state"))
	payload := strings.TrimSpace(req.Query.Get("payload"))
	s.mu.Lock()
	flow, found := s.logins[state]
	s.mu.Unlock()
	if !found || time.Now().UTC().After(flow.ExpiresAt) {
		return htmlResponse(http.StatusBadRequest, messagePage("Connection expired", "Start a new OpenRouter connection from the Management Center.")), nil
	}
	apiKey, err := decryptLoginPayload(flow, payload)
	if err != nil {
		return htmlResponse(http.StatusBadRequest, loginHTML(state, flow.PublicKey, err.Error())), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	label, err := s.validateKey(ctx, callbackID, apiKey)
	if err == nil {
		_, _, err = s.fetchModels(ctx, callbackID, apiKey)
	}
	if err != nil {
		apiKey = ""
		return htmlResponse(http.StatusBadRequest, loginHTML(state, flow.PublicKey, "The API key could not be validated by OpenRouter.")), nil
	}
	storage, err := newStorage(apiKey, label)
	apiKey = ""
	if err != nil {
		return pluginapi.ManagementResponse{}, err
	}
	s.mu.Lock()
	flow.Storage = &storage
	flow.PrivateKey = nil
	s.mu.Unlock()
	return htmlResponse(http.StatusOK, messagePage("OpenRouter connected", "You can close this window.")), nil
}

func loginHTML(state, publicKey, errorMessage string) string {
	errorBlock := ""
	if errorMessage != "" {
		errorBlock = `<div class="error" role="alert">` + html.EscapeString(errorMessage) + `</div>`
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Connect OpenRouter</title><style>` + loginCSS + `</style></head><body><main><section class="card"><h1>Connect OpenRouter</h1><p class="intro">Enter an API key from your OpenRouter account.</p>` + errorBlock + `<form id="connect"><label for="api-key">API key</label><input id="api-key" type="password" autocomplete="off" spellcheck="false" required placeholder="sk-or-v1-..."><button type="submit">Connect</button><p id="status" role="status"></p></form></section></main><script>
const state=` + quoteJS(state) + `;const publicKey=` + quoteJS(publicKey) + `;const form=document.getElementById("connect");const status=document.getElementById("status");
function bytes(value){const raw=atob(value);return Uint8Array.from(raw,c=>c.charCodeAt(0))}function b64url(value){let s="";for(const b of value)s+=String.fromCharCode(b);return btoa(s).replace(/\+/g,"-").replace(/\//g,"_").replace(/=+$/g,"")}
form.addEventListener("submit",async event=>{event.preventDefault();const input=document.getElementById("api-key");const secret=input.value.trim();if(!secret)return;input.disabled=true;status.textContent="Connecting...";try{const key=await crypto.subtle.importKey("spki",bytes(publicKey),{name:"RSA-OAEP",hash:"SHA-256"},false,["encrypt"]);const encrypted=await crypto.subtle.encrypt({name:"RSA-OAEP"},key,new TextEncoder().encode(secret));input.value="";location.replace("/v0/resource/plugins/openrouter/connect?state="+encodeURIComponent(state)+"&payload="+encodeURIComponent(b64url(new Uint8Array(encrypted))))}catch(error){input.disabled=false;status.textContent="Could not encrypt the API key."}});
</script></body></html>`
}

const loginCSS = `:root{color-scheme:dark;--page:#19161d;--surface:#211e25;--text:#f3f0f5;--muted:#aaa4af;--border:#4a444f;--focus:#9a7dff;--button:#7f5af0}*{box-sizing:border-box}body{margin:0;min-height:100vh;background:var(--page);color:var(--text);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;font-size:14px;line-height:1.45}main{min-height:100vh;display:grid;place-items:center;padding:24px}.card{width:100%;max-width:420px;padding:30px;background:var(--surface);border:1px solid #38333d;border-radius:12px}h1{margin:0 0 8px;font-size:22px}.intro{margin:0 0 24px;color:var(--muted)}label{display:block;margin-bottom:8px;font-weight:600}input{width:100%;height:42px;padding:0 12px;border:1px solid var(--border);border-radius:6px;background:#171419;color:var(--text);font:inherit;outline:none}input:focus-visible{border-color:var(--focus);box-shadow:0 0 0 3px rgba(154,125,255,.22)}button{width:100%;height:42px;margin-top:22px;border:1px solid #9c85ef;border-radius:6px;background:var(--button);color:#fff;font:600 14px/1 inherit;cursor:pointer}.error{margin:0 0 18px;padding:10px;border:1px solid #8c465d;border-radius:6px;background:#34232b;color:#ffd7e2}#status{min-height:20px;margin:12px 0 0;color:var(--muted)}@media(max-width:520px){main{padding:14px}.card{padding:24px 20px}}`

func messagePage(title, message string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>` + html.EscapeString(title) + `</title><style>` + loginCSS + `</style></head><body><main><section class="card"><h1>` + html.EscapeString(title) + `</h1><p class="intro">` + html.EscapeString(message) + `</p></section></main></body></html>`
}

func quoteJS(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "`", "\\`")
	value = strings.ReplaceAll(value, "${", "\\${")
	return "`" + value + "`"
}

func htmlResponse(status int, body string) pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{StatusCode: status, Headers: http.Header{
		"Cache-Control":           []string{"no-store"},
		"Content-Security-Policy": []string{"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"},
		"Content-Type":            []string{"text/html; charset=utf-8"},
		"Referrer-Policy":         []string{"no-referrer"},
		"X-Content-Type-Options":  []string{"nosniff"},
	}, Body: []byte(body)}
}

func textHeaders() http.Header {
	return http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}, "Cache-Control": []string{"no-store"}}
}

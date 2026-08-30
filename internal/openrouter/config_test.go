package openrouter

import (
	"context"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestStartLoginUsesConfiguredPublicBaseURL(t *testing.T) {
	service := New(&mockHost{})
	if err := service.Configure([]byte("enabled: true\npublic-base-url: https://ai-proxy.linkai.me/\n")); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	response, err := service.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{
		BaseURL: "http://127.0.0.1:8317/v0/management/oauth-callback",
	})
	if err != nil {
		t.Fatalf("StartLogin() error = %v", err)
	}
	if !strings.HasPrefix(response.URL, "https://ai-proxy.linkai.me/v0/resource/plugins/openrouter/login?state=") {
		t.Fatalf("StartLogin() URL = %q, want public HTTPS origin", response.URL)
	}
	if strings.Contains(response.URL, "127.0.0.1") {
		t.Fatalf("StartLogin() URL = %q, must not expose the host loopback origin", response.URL)
	}
}

func TestStartLoginFallsBackToHostBaseURL(t *testing.T) {
	service := New(&mockHost{})
	if err := service.Configure([]byte("enabled: true\n")); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	response, err := service.StartLogin(context.Background(), pluginapi.AuthLoginStartRequest{
		BaseURL: "http://127.0.0.1:8317/v0/management/oauth-callback",
	})
	if err != nil {
		t.Fatalf("StartLogin() error = %v", err)
	}
	if !strings.HasPrefix(response.URL, "http://127.0.0.1:8317/v0/resource/plugins/openrouter/login?state=") {
		t.Fatalf("StartLogin() URL = %q, want host origin fallback", response.URL)
	}
}

func TestConfigureRejectsUnsafePublicBaseURLsWithoutReplacingActiveConfig(t *testing.T) {
	service := New(&mockHost{})
	if err := service.Configure([]byte("public-base-url: https://ai-proxy.linkai.me\n")); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}

	invalid := []string{
		"http://ai-proxy.linkai.me",
		"https://user:pass@ai-proxy.linkai.me",
		"https://ai-proxy.linkai.me/prefix",
		"https://ai-proxy.linkai.me?token=value",
		"https://ai-proxy.linkai.me#fragment",
		"ai-proxy.linkai.me",
	}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			if err := service.Configure([]byte("public-base-url: \"" + raw + "\"\n")); err == nil {
				t.Fatalf("Configure(%q) error = nil, want validation error", raw)
			}
			base, err := service.loginBaseURL("http://127.0.0.1:8317")
			if err != nil {
				t.Fatalf("loginBaseURL() error = %v", err)
			}
			if base != "https://ai-proxy.linkai.me" {
				t.Fatalf("loginBaseURL() = %q after rejected config, want previous value", base)
			}
		})
	}
}

func TestConfigureRejectsInvalidYAMLWithoutReplacingActiveConfig(t *testing.T) {
	service := New(&mockHost{})
	if err := service.Configure([]byte("public-base-url: https://ai-proxy.linkai.me\n")); err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	if err := service.Configure([]byte("public-base-url: [\n")); err == nil {
		t.Fatal("Configure() error = nil, want YAML decoding error")
	}
	base, err := service.loginBaseURL("http://127.0.0.1:8317")
	if err != nil {
		t.Fatalf("loginBaseURL() error = %v", err)
	}
	if base != "https://ai-proxy.linkai.me" {
		t.Fatalf("loginBaseURL() = %q after rejected config, want previous value", base)
	}
}

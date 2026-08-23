package openrouter

import (
	"strings"
	"testing"
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

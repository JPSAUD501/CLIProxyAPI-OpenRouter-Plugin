package main

import (
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

func TestManagementRegistrationIncludesModelCapabilities(t *testing.T) {
	result, err := dispatch(pluginabi.MethodManagementRegister, nil)
	if err != nil {
		t.Fatal(err)
	}
	registration, ok := result.(managementRegistrationResponse)
	if !ok {
		t.Fatalf("unexpected registration type: %T", result)
	}
	if len(registration.Routes) != 1 {
		t.Fatalf("management route count = %d, want 1", len(registration.Routes))
	}
	route := registration.Routes[0]
	if route.Method != http.MethodGet || route.Path != "/plugins/openrouter/model-capabilities" {
		t.Fatalf("unexpected management route: %#v", route)
	}
}

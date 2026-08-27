package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/JPSAUD501/CLIProxyAPI-OpenRouter-Plugin/internal/openrouter"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const pluginVersion = "0.2.0"

var pluginService = openrouter.New(hostTransport{})

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats"`
	ManagementAPI         bool                         `json:"management_api"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

type rpcAuthLoginStartRequest struct {
	pluginapi.AuthLoginStartRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcAuthLoginPollRequest struct {
	pluginapi.AuthLoginPollRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcAuthRefreshRequest struct {
	pluginapi.AuthRefreshRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcAuthModelRequest struct {
	pluginapi.AuthModelRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcManagementRequest struct {
	pluginapi.ManagementRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type managementRegistrationResponse struct {
	Routes    []pluginapi.ManagementRoute `json:"routes,omitempty"`
	Resources []pluginapi.ResourceRoute   `json:"resources,omitempty"`
}

func handleMethod(method string, request []byte) ([]byte, bool) {
	result, err := dispatch(method, request)
	if err != nil {
		var statusErr *openrouter.StatusError
		if errors.As(err, &statusErr) {
			return errorEnvelope(statusErr.Code, statusErr.Message, statusErr.HTTPStatus, statusErr.Retryable), true
		}
		return errorEnvelope("plugin_error", err.Error(), http.StatusInternalServerError, false), true
	}
	raw, err := okEnvelope(result)
	if err != nil {
		return errorEnvelope("encoding_error", err.Error(), http.StatusInternalServerError, false), true
	}
	return raw, false
}

func dispatch(method string, request []byte) (any, error) {
	ctx := context.Background()
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		var req lifecycleRequest
		if len(request) > 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, err
			}
		}
		if err := pluginService.Configure(req.ConfigYAML); err != nil {
			return nil, err
		}
		return pluginRegistration(), nil
	case pluginabi.MethodModelStatic:
		return pluginService.StaticModels(), nil
	case pluginabi.MethodModelForAuth:
		var req rpcAuthModelRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.ModelsForAuth(ctx, req.HostCallbackID, req.AuthModelRequest)
	case pluginabi.MethodAuthIdentifier, pluginabi.MethodExecutorIdentifier:
		return identifierResponse{Identifier: "openrouter"}, nil
	case pluginabi.MethodAuthParse:
		var req pluginapi.AuthParseRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.ParseAuth(ctx, req)
	case pluginabi.MethodAuthLoginStart:
		var req rpcAuthLoginStartRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.StartLogin(ctx, req.AuthLoginStartRequest)
	case pluginabi.MethodAuthLoginPoll:
		var req rpcAuthLoginPollRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.PollLogin(ctx, req.AuthLoginPollRequest)
	case pluginabi.MethodAuthRefresh:
		var req rpcAuthRefreshRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.RefreshAuth(ctx, req.HostCallbackID, req.AuthRefreshRequest)
	case pluginabi.MethodExecutorExecute:
		var req openrouter.ExecuteRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.Execute(ctx, req)
	case pluginabi.MethodExecutorExecuteStream:
		var req openrouter.ExecuteRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		headers, err := pluginService.ExecuteStream(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{"headers": headers}, nil
	case pluginabi.MethodExecutorCountTokens:
		return pluginService.CountTokens()
	case pluginabi.MethodExecutorHTTPRequest:
		var req openrouter.HTTPRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.HTTP(ctx, req)
	case pluginabi.MethodManagementRegister:
		return managementRegistrationResponse{Resources: []pluginapi.ResourceRoute{{Path: "/login"}, {Path: "/connect"}}}, nil
	case pluginabi.MethodManagementHandle:
		var req rpcManagementRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return nil, err
		}
		return pluginService.Management(req.HostCallbackID, req.ManagementRequest)
	default:
		return nil, &openrouter.StatusError{Code: "unknown_method", Message: "unknown plugin method: " + method, HTTPStatus: http.StatusNotImplemented}
	}
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "OpenRouter",
			Version:          pluginVersion,
			Author:           "JPSAU501",
			GitHubRepository: "https://github.com/JPSAUD501/CLIProxyAPI-OpenRouter-Plugin",
		},
		Capabilities: registrationCapabilities{
			ModelProvider:         true,
			AuthProvider:          true,
			Executor:              true,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeOAuth,
			ExecutorInputFormats:  []string{"openai", "openai-response", "claude"},
			ExecutorOutputFormats: []string{"openai", "openai-response", "claude"},
			ManagementAPI:         true,
		},
	}
}

func okEnvelope(value any) ([]byte, error) {
	result, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginabi.Envelope{OK: true, Result: result})
}

func errorEnvelope(code, message string, status int, retryable bool) []byte {
	raw, _ := json.Marshal(pluginabi.Envelope{OK: false, Error: &pluginabi.Error{Code: code, Message: message, HTTPStatus: status, Retryable: retryable}})
	return raw
}

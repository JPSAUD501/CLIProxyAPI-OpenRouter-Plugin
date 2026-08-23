# OpenRouter plugin for CLIProxyAPI

Native OpenRouter provider for the official CLIProxyAPI plugin ABI. It discovers the models available to each API key and forwards OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages without translating conversation content.

## Requirements

- CLIProxyAPI 7.2.139
- Windows amd64
- An OpenRouter API key

The plugin stores each key as an independent CLIProxyAPI credential. The API key is encrypted with Windows DPAPI for the current user; persisted files contain only the ciphertext, a SHA-256 deduplication hash, the key label, and non-sensitive metadata.

## Installation

1. Download `openrouter.dll`, `openrouter.h`, and `openrouter.dll.sha256` from the release.
2. Verify the DLL checksum.
3. Copy `openrouter.dll` to the CLIProxyAPI plugin directory.
4. Enable the plugin:

```yaml
plugins:
  enabled: true
  dir: plugins
  configs:
    openrouter:
      enabled: true
```

5. Restart CLIProxyAPI.
6. Open the OpenRouter authentication flow in the Management Center and enter the API key. The browser encrypts it before sending the login result to the plugin.

Each credential is assigned priority `-1`, allowing credentials with the default priority `0` to remain preferred for shared model IDs. Credentials at the same priority follow the global CLIProxyAPI routing strategy.

## Model catalog

The catalog comes from OpenRouter's authenticated `GET /api/v1/models/user` response and refreshes every 30 minutes.

- Manufacturer namespaces are removed: `anthropic/claude-opus-5` is exposed as `claude-opus-5`.
- OpenRouter virtual models retain their native names, such as `openrouter/auto` and `openrouter/free`.
- Models without text output are not exposed in this version.
- If two native slugs produce the same short alias, that ambiguous alias is omitted.
- Context, output limit, modalities, parameters, and reasoning efforts are copied from the authenticated catalog when OpenRouter supplies them.

The plugin keeps the last valid in-memory catalog for transient network errors, HTTP 429, and HTTP 5xx. An authorization failure affects only the corresponding credential.

## Protocol behavior

The executor maps the selected short alias back to its native OpenRouter slug and forwards the request to the matching native endpoint:

- `openai` → `POST /api/v1/chat/completions`
- `openai-response` → `POST /api/v1/responses`
- `claude` → `POST /api/v1/messages`

Request content is not truncated, synthesized, or normalized. System and developer instructions, history, tools, tool results, reasoning, multimodal content, provider preferences, and OpenRouter-specific parameters remain in the original protocol. JSON and SSE responses remain in that same protocol; only native model slugs are changed back to their public aliases.

The plugin does not add retries, simulate unsupported endpoints, count tokens locally, or implement `/responses/compact`. Failover and cooldown remain CLIProxyAPI responsibilities, while provider routing remains an OpenRouter request option.

## Build

```powershell
go test ./...
go vet ./...
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "1"
go build -trimpath -buildmode=c-shared -o dist/openrouter.dll ./cmd/openrouter-plugin
```

## License

MIT


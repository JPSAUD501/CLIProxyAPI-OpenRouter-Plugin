# OpenRouter plugin for CLIProxyAPI

Native OpenRouter provider for the official CLIProxyAPI plugin ABI. It discovers the models available to each API key and forwards OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages without translating conversation content.

## Requirements

- CLIProxyAPI 7.2.143
- Windows amd64, Linux amd64, or Linux arm64
- An OpenRouter API key
- A 256-bit master key in `CLIPROXY_OPENROUTER_MASTER_KEY`

The plugin stores each key as an independent CLIProxyAPI credential. Storage v2 encrypts API keys with AES-256-GCM using the master key supplied as a Base64-encoded 32-byte value. Persisted files contain only ciphertext, a random nonce, a SHA-256 deduplication hash, the key label, note, and non-sensitive metadata. The master key is never stored in the credential file.

Windows can still read legacy DPAPI credentials created by storage v1. Linux deliberately rejects those files with an actionable error because DPAPI decryption is bound to the originating Windows user.

## Installation

1. Download the archive for the target operating system and architecture.
2. Verify the archive checksum and the checksum included beside the plugin binary.
3. Copy `openrouter.dll` on Windows or `openrouter.so` on Linux to the CLIProxyAPI plugin directory.
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
6. Open the OpenRouter authentication flow in the Management Center and enter the API key. The plugin encrypts it before persistence.

Each credential is assigned priority `-1`, allowing credentials with the default priority `0` to remain preferred for shared model IDs. Credentials at the same priority follow the global CLIProxyAPI routing strategy.

## Model catalog

The catalog comes from OpenRouter's authenticated `GET /api/v1/models/user` response and refreshes every 30 minutes.

- Manufacturer namespaces are removed: `anthropic/claude-opus-5` is exposed as `claude-opus-5`.
- OpenRouter virtual models retain their native names, such as `openrouter/auto` and `openrouter/free`.
- Models without text output are not exposed in this version.
- If two native slugs produce the same short alias, that ambiguous alias is omitted.
- Context, output limit, modalities, parameters, and reasoning efforts are copied from the authenticated catalog when OpenRouter supplies them.

The plugin keeps the last valid in-memory catalog for transient network errors, HTTP 429, and HTTP 5xx. An authorization failure affects only the corresponding credential.

The authenticated Management API exposes the suffix-compatible reasoning levels from that same normalized catalog at `GET /v0/management/plugins/openrouter/model-capabilities`. The response contains only model aliases and effort levels. It does not expose API keys, credential storage, account labels, or request data.

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

Linux builds use the same command with `GOOS=linux`, the target `GOARCH`, and the output name `openrouter.so`. CGO and a compiler for the target architecture are required.

## Migrate legacy Windows credentials

Run the migration on the same Windows user account that created the DPAPI file. Set `CLIPROXY_OPENROUTER_MASTER_KEY` to the same master key that the destination instance will use, then stop CLIProxyAPI and run:

```powershell
openrouter-migrate.exe C:\Users\you\.cli-proxy-api\openrouter-account.json
```

The migration decrypts the legacy value in memory, writes a storage-v2 file beside the original, verifies that it can be decrypted with the configured master key, and atomically replaces the original. A failed migration leaves the original file untouched. Do not copy a legacy DPAPI file to Linux.

## Key rotation

To rotate the master key, decrypt and migrate each credential on a trusted host while the old key is present, then re-encrypt with the new key before starting the destination instance. Never run two proxy instances against the same OAuth or provider credential set during a cutover.

## License

MIT


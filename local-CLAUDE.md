# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

A Go-based LLM proxy server that forwards requests to OpenAI, Anthropic, Gemini providers and local LLM backends (via vLLM). Features cost tracking, rate limiting, and Claude Code compatibility proxy.

**Key capabilities:**
- Multi-provider support: OpenAI, Anthropic, Gemini, and local LLMs (gpt-oss, qwen)
- Claude Code proxy (`/cc-qwen/`): Converts Anthropic API format to OpenAI format for local models
- Debug middleware (`--llm-debug`): Shows curl-equivalent request/response info with colored output
- `<think>` tag processing: Fixes Qwen models that emit `</think>` without opening tag

## Common Development Commands

```bash
# Build and run
make install build     # Install dependencies and build
make run               # Run server on port 9002
make dev               # Run in development mode with hot reload

# Run a single test
go test -v ./internal/providers -run "TestOpenAIIntegration" -timeout 90s

# Run unit tests only (no API keys needed)
make test              # or: go test -v ./internal/... -short -skip "Integration"

# Run integration tests (requires API keys)
make test-all
make test-openai       # OpenAI only
make test-anthropic    # Anthropic only
make test-gemini       # Gemini only

# Code quality
make check             # fmt + vet + lint
make fmt               # Format Go code
```

### Debug Mode

Run with `--llm-debug` flag to see colored curl-equivalent request/response output:
```bash
./bin/llm-proxy --llm-debug
```
- Request: cyan, Response: green, Info: yellow
- Suppresses other logs when active
- Shows headers (sensitive redacted), pretty-printed JSON bodies, timing

## Architecture Overview

### Directory Structure

```
cmd/llm-proxy/main.go      # Entry point, middleware setup, provider registration
internal/
├── config/config.go       # YAML config loading with env var expansion
├── providers/
│   ├── provider.go        # Provider interface and manager
│   ├── openai.go          # OpenAI provider
│   ├── anthropic.go       # Anthropic provider
│   ├── gemini.go          # Gemini provider
│   ├── local_llm.go       # Local LLM provider (gpt-oss, qwen)
│   └── claude_code_proxy.go  # /cc-qwen/ Claude→OpenAI conversion
├── middleware/
│   ├── debug.go           # --llm-debug colored output
│   ├── streaming.go       # SSE streaming support
│   └── ...                # logging, CORS, rate limiting, token parsing
├── cost/                  # Cost tracking (file, DynamoDB, Datadog)
└── ratelimit/             # Rate limiting (memory, Redis)
configs/
├── base.yml               # Base configuration
├── dev.yml                # Development overrides
└── onprem.yml             # On-premises deployment config
```

### Provider Interface

Every provider implements the `Provider` interface in `internal/providers/provider.go`:
- `GetName()` - Provider identifier
- `IsStreamingRequest(req)` - Detect streaming mode
- `Proxy()` - HTTP handler for proxying requests
- `GetHealthStatus()` - Health check data
- `ValidateAPIKey(req, keyStore)` - API key validation
- `ExtractRequestModelAndMessages(req)` - For token estimation
- `ParseResponseMetadata(body, isStreaming)` - Extract usage/tokens

### Middleware Order (Critical)

Defined in `cmd/llm-proxy/main.go` - order matters:
1. MetaURLRewritingMiddleware (first)
2. DebugMiddleware (if `--llm-debug`)
3. APIKeyValidationMiddleware (if enabled)
4. LoggingMiddleware
5. RateLimitingMiddleware (if enabled)
6. CORSMiddleware
7. TokenParsingMiddleware
8. StreamingMiddleware (last)

### Configuration

- Base config: `configs/base.yml`
- Environment overlay: `configs/{ENVIRONMENT}.yml` (default: dev)
- Environment variables expand in YAML: `${VAR_NAME:-default}`

Local LLM configuration example in `configs/base.yml`:
```yaml
local_llms:
  qwen:
    enabled: true
    default_model: "qwen/qwen3-next-80b-a3b-thinking"
    thinking_tag_fix: true  # Enable <think> tag processing
    models:
      "qwen/qwen3-next-80b-a3b-thinking":
        endpoints:
          - url: "http://192.168.1.200:8001/v1"
            api_key: ""  # Optional

claude_code_proxy:
  enabled: true
  target_provider: "qwen"
  target_model: "qwen/qwen3-next-80b-a3b-thinking"
  parameter_mapping:
    max_tokens: "max_completion_tokens"
```

## Routing Map

| Prefix | Format | Backend | Notes |
|--------|--------|---------|-------|
| `/openai/*` | OpenAI | Real OpenAI API | No local routing |
| `/anthropic/*` | Anthropic | Real Anthropic API | |
| `/gemini/*` | Gemini | Real Gemini API | |
| `/bedrock/*` | Mixed | AWS Bedrock | 28+ models |
| `/gpt-oss/*` | OpenAI | Local gpt-oss LLM | |
| `/qwen/*` | OpenAI | Local Qwen LLM | `<think>` tag fix applied |
| `/cc/*` | **Anthropic** | Fireworks/Local | Claude Code production endpoint |
| `/cc-qwen/*` | **Anthropic** | Local Qwen LLM | Claude Code compatible |
| `/multi/*` | OpenAI | On-prem + Cloud | Federated routing |

## Key Implementation Details

### Think Tag Processing (`internal/providers/local_llm.go`)

For Qwen models ending in `-thinking`:
- **Non-streaming**: If response contains `</think>` but no `<think>`, prepend `<think>` at start
- **Streaming**: Always inject `<think>` at stream start for `-thinking` models

### Claude Code Proxy (`internal/providers/claude_code_proxy.go`)

Converts Anthropic API format to OpenAI format (for local vLLM):
- Endpoint: `/cc-qwen/v1/messages` → proxies to `/qwen/v1/chat/completions`
- Content blocks: Array of `{type: "text", text: "..."}` → single string
- Parameters: `max_tokens` → `max_completion_tokens`
- Response: OpenAI choices → Claude content blocks
- Streaming: OpenAI SSE → Claude SSE events

### Claude Code Cloud (`internal/providers/claude_code_cloud.go`)

Production endpoint for Claude Code with configurable backends (Fireworks, local vLLM):
- Endpoint: `/cc/v1/messages` - Anthropic Messages API compatible
- Model mapping: `hc/glm-4.7` → `accounts/fireworks/models/glm-4p7` (Fireworks)
- Supports multiple backends: `fireworks`, `local`, `openai`
- Configuration in `configs/onprem.yml` under `claude_code_cloud`

Client configuration (`~/.claude/settings.json`):
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://llm.example.edu/cc/v1",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "hc/glm-4.7",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "hc/glm-4.7"
  }
}
```

### Local LLM Failover (`internal/providers/local_llm.go`)

- Random endpoint selection (stateless round-robin)
- 100ms timeout per endpoint
- Immediate failover to next endpoint on failure
- Failed endpoints immediately rejoin rotation
- Error response: `"Sorry the backend server XXX is currently not available..."`

## Environment Setup

```bash
# Copy and configure
cp .env.example .env

# Required for integration tests
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
export GEMINI_API_KEY=...

# Local LLM endpoints (optional)
export GPT_OSS_ENDPOINT_1=http://192.168.1.100:8000/v1
export QWEN_ENDPOINT_1=http://192.168.1.200:8001/v1
```

## Development Notes

- **Go Version**: 1.24.5
- **Router**: Gorilla Mux
- **Testing**: Integration tests require real API keys; unit tests use `-short -skip "Integration"`
- **Logging**: slog with pretty (dev) or JSON (prod) format via `LOG_FORMAT` env var

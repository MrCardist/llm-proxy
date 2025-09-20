# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is a Go-based LLM proxy server that forwards requests to OpenAI, Anthropic, and Gemini providers with comprehensive cost tracking, rate limiting, and API key management. The proxy serves as a lightweight alternative to `litellm` with a modular architecture built on Go and the Gorilla web toolkit.

**Core Purpose**: Multi-provider LLM proxy with advanced features like streaming support, cost tracking, rate limiting, and local LLM integration.

## Active Development Goals

The project is currently being extended with these major features:

1. **Debug Middleware**: Real-time request/response debugging with `--llm-debug` flag showing colored, formatted JSON output
2. **Local LLM Providers**: Support for locally hosted models (gpt-oss, qwen) with endpoint management and failover
3. **Claude Code Proxy**: `/cc-qwen/` endpoint that accepts Anthropic API format and routes to local models
4. **Think Tag Processing**: Automatic `<think>` tag wrapping for reasoning content from local models

### Implementation Status & Priorities

1. **✅ Base Architecture**: Established provider system, middleware pipeline, and configuration management
2. **🚧 Local LLM Integration**: Add `gpt-oss` and `qwen` providers with round-robin endpoint selection
3. **🚧 Debug Middleware**: Implement `--llm-debug` with curl-equivalent output formatting
4. **🚧 Claude Code Proxy**: Anthropic→OpenAI format conversion with parameter mapping
5. **🚧 Think Tag Processing**: Pattern detection and automatic `<think>` tag insertion for qwen models

## Architecture Overview

### Core Components

**Main Entry Point**: `cmd/llm-proxy/main.go`
- Server initialization and graceful shutdown
- Provider registration and middleware orchestration
- Configuration loading (base.yml + environment overlay)
- Global component initialization (cost tracker, rate limiter, API key store)

**Provider System** (`internal/providers/`):
- `provider.go`: Core Provider interface defining streaming detection, metadata parsing, health checks
- `openai.go`, `anthropic.go`, `gemini.go`: Standard LLM provider implementations
- `local_llm.go`: Local LLM provider with endpoint management and failover (100ms timeout)
- `claude_code_proxy.go`: Anthropic↔OpenAI format conversion proxy
- Each provider uses `CreateGenericDirector()` for common reverse proxy logic

**Configuration System** (`internal/config/config.go`):
- YAML-based with environment variable expansion (`${VAR:-default}`)
- Hierarchical loading: `configs/base.yml` + `configs/{ENVIRONMENT}.yml` 
- Deep merging of environment-specific overrides
- Validation of transport, rate limiting, and pricing configurations

**Middleware Pipeline** (`internal/middleware/`):
- **CRITICAL ORDER**: MetaURL → Debug → APIKey → Logging → RateLimit → CORS → TokenParsing → Streaming
- `meta_url_rewriting.go`: Handles `/meta/{userID}/provider/` → `/provider/` transformation
- `debug.go`: Request/response capture with colored terminal output (suppresses other logs)
- `streaming.go`: Optimized streaming response handling (must be last middleware)
- `token_parsing.go`: Metadata extraction and cost tracking callback execution

**Feature Modules**:
- `internal/cost/`: Multi-transport cost tracking (file, DynamoDB, Datadog) with async workers
- `internal/ratelimit/`: Memory and Redis backends with token estimation
- `internal/apikeys/`: DynamoDB-based key store supporting `iw:` prefix lookups

### Provider Interface Contract

All providers must implement:

```go
type Provider interface {
    GetName() string
    IsStreamingRequest(req *http.Request) bool
    ParseResponseMetadata(responseBody io.Reader, isStreaming bool) (*LLMResponseMetadata, error)
    Proxy() http.Handler
    GetHealthStatus() map[string]interface{}
    UserIDFromRequest(req *http.Request) string
    RegisterExtraRoutes(router *mux.Router)
    ValidateAPIKey(req *http.Request, keyStore APIKeyStore) error
    ExtractRequestModelAndMessages(req *http.Request) (string, []string)
}
```

### Configuration Loading Strategy

1. Load `configs/base.yml` (full configuration with defaults)
2. Load `configs/${ENVIRONMENT}.yml` (environment-specific overrides, defaults to "dev")  
3. Deep merge environment config into base config
4. Validate merged configuration and parse pricing structures
5. Create transport, rate limiter, and API key store instances

### Middleware Processing Order

**CRITICAL**: The middleware order is essential for proper operation, especially streaming:

1. **MetaURLRewritingMiddleware** (must be first): Rewrites `/meta/{userID}/provider/` to `/provider/`
2. **DebugMiddleware** (early): Captures requests/responses, suppresses other logs when enabled
3. **APIKeyValidationMiddleware**: Validates and replaces `iw:` prefixed keys from key store
4. **LoggingMiddleware**: Request/response logging (skipped in debug mode)
5. **RateLimitingMiddleware**: Token estimation and rate limit enforcement
6. **CORSMiddleware**: CORS header management
7. **TokenParsingMiddleware**: Response metadata extraction and callback execution
8. **StreamingMiddleware** (must be last): Streaming response optimization

## Local LLM Implementation Plan

### Provider Configuration Structure

```yaml
local_llms:
  gpt-oss:
    enabled: true
    default_model: "openai/gpt-oss-120b"
    request_timeout: 100  # milliseconds
    max_retries: 3
    models:
      openai/gpt-oss-120b:
        enabled: true
        endpoints:
          - url: "http://192.168.1.100:8000/v1"
            api_key: ""  # optional
          - url: "http://192.168.1.101:8000/v1"
  
  qwen:
    enabled: true
    default_model: "qwen/qwen3-next-80b-a3b-thinking"
    thinking_tag_fix: true  # Enable <think> processing
    models:
      qwen/qwen3-next-80b-a3b-thinking:
        enabled: true
        endpoints:
          - url: "http://192.168.1.200:8001/v1"
          - url: "http://192.168.1.201:8001/v1"

claude_code_proxy:
  enabled: true
  target_provider: "qwen"
  target_model: "qwen/qwen3-next-80b-a3b-thinking"
  parameter_mapping:
    max_tokens: "max_completion_tokens"
```

### Request Flow Examples

**1. Local LLM Request**: `POST /qwen/v1/chat/completions`
- Route to qwen provider
- Random endpoint selection from available URLs
- Apply think tag processing if model name ends with `-thinking`
- 100ms timeout with immediate failover

**2. Claude Code Request**: `POST /cc-qwen/v1/messages` (Anthropic format)
- Convert Anthropic messages format to OpenAI chat/completions  
- Apply parameter mapping (max_tokens → max_completion_tokens)
- Route to configured target_provider (qwen) using target_model
- Convert response back to Anthropic format
- Apply think tag processing

**3. Debug Mode**: `./llm-proxy --llm-debug`
- Suppress all standard logs
- Show curl-equivalent output: method, URL, headers, JSON body
- Color coding: cyan for requests, green for responses
- Pretty-print JSON with endpoint selection and retry information

### Think Tag Processing Logic

**Non-streaming responses**:
- If response contains `</think>` tag anywhere, prepend `<think>` at the beginning
- Only process if response doesn't already start with `<think>`
- Applied only to qwen provider responses

**Streaming responses**:  
- If model name ends with `-thinking`, inject `<think>` at stream start
- Handle `</think>` tag detection during streaming
- Buffer management to avoid corrupting partial JSON chunks

### Error Handling Strategy

**Connection Failures**:
- 100ms timeout per endpoint attempt
- Immediate failover to next endpoint in list
- Failed endpoints immediately rejoin rotation (no blacklisting)
- Return OpenAI-format error: "Sorry the backend server XXX is currently not available..."

**Configuration Errors**:
- Validate endpoint URLs include full paths (e.g., `/v1`)
- Ensure default_model exists in provider's models configuration
- Warn about missing API keys but allow optional authentication

## Development Commands

### Build and Run
```bash
# Development workflow
make install build    # Install deps and build binary
make dev             # Run with live reload
make run             # Run built binary

# Debug mode
./bin/llm-proxy --llm-debug  # Enable request/response debugging

# Configuration validation
make validate-config configs/base.yml,configs/dev.yml
```

### Testing Strategy
```bash
# Unit tests (no API keys required)
make test

# Integration tests (requires API keys)
make test-all
make test-openai
make test-anthropic  
make test-gemini

# Health check verification
make test-health

# Environment setup check
make env-check
```

### Code Quality
```bash
make check    # Run fmt + vet + lint
make fmt      # Format Go code  
make vet      # Run go vet
make lint     # Run golint
```

## Configuration Management

### Environment Variables

**Required for Integration Testing**:
```bash
export OPENAI_API_KEY=your_openai_key
export ANTHROPIC_API_KEY=your_anthropic_key  
export GEMINI_API_KEY=your_gemini_key
```

**Optional Runtime Configuration**:
```bash
export PORT=9002                    # Server port (default: 9002)
export ENVIRONMENT=dev              # Config environment (dev/staging/production)
export LOG_LEVEL=debug              # Logging level
export LOG_FORMAT=json              # Log format (json/pretty)
```

**Cost Tracking**:
```bash
export COST_TRACKING_FILE=./cost.jsonl  # File transport fallback
export DD_API_KEY=datadog_key           # Datadog transport
```

### Configuration File Structure

- `configs/base.yml`: Complete configuration with all providers and models
- `configs/dev.yml`: Development overrides (typically disables cost tracking/rate limiting)
- `configs/staging.yml`: Staging environment settings
- `configs/production.yml`: Production configuration with full feature enablement

### Model Pricing Configuration

```yaml
providers:
  openai:
    models:
      gpt-4:
        pricing:
          tiers:
            - threshold: 0      # Default pricing
              input: 30.0       # $30 per 1M input tokens
              output: 60.0      # $60 per 1M output tokens
            - threshold: 1000000 # Volume pricing above 1M tokens
              input: 25.0
              output: 50.0
          overrides:
            gpt-4-turbo: 
              input: 10.0
              output: 30.0
```

## Key Implementation Details

### Streaming Response Handling

- `StreamingMiddleware` must be the last middleware in the chain
- Streaming detection is provider-specific via `IsStreamingRequest()`
- Response metadata parsing works for both streaming and non-streaming responses
- Gzip decompression handled by `DecompressResponseIfNeeded()` utility

### Cost Tracking Flow

1. `TokenParsingMiddleware` extracts `LLMResponseMetadata` from responses
2. Metadata includes tokens, model, provider, request ID
3. Callbacks execute asynchronously if async mode enabled
4. Multiple transports write cost data simultaneously (file, DynamoDB, Datadog)

### Rate Limiting Strategy

- Token estimation via Content-Length (`BytesPerToken`) or message parsing (`CharsPerToken`)
- Provider-specific character-per-token ratios for better accuracy
- "Optimistic first request" allows initial traffic through even if over limit
- Supports per-key, per-user, per-model overrides

### API Key Management

- Standard keys pass through unchanged
- Keys prefixed with `iw:` trigger DynamoDB lookup for actual provider key
- Failed key validation returns 401 with provider-specific error format
- Key store validates key status (enabled/disabled) and provider association

## Testing & Validation

### Integration Test Requirements

- Real API keys needed for provider integration tests
- Tests validate streaming and non-streaming requests
- Health check tests verify all provider status endpoints
- Rate limiting tests require memory backend (Redis not supported in tests)

### Configuration Validation

Use `--validate-config` flag to test configuration files:
```bash
./llm-proxy --validate-config configs/base.yml,configs/dev.yml
```

Validates transport configuration, rate limiting setup, and pricing structures.

### Version Information

Use `--version` flag to see loaded configuration and build information:
```bash  
./llm-proxy --version
```

Shows complete configuration in both human-readable and JSON formats.

## Implementation Guidelines

### Adding New Providers

1. Create new provider file implementing `Provider` interface
2. Register in `main.go` provider registration section  
3. Add corresponding test file following existing patterns
4. Update configuration schema if needed
5. Add health check implementation
6. Implement streaming detection logic

### Middleware Development

1. Follow established middleware patterns in `internal/middleware/`
2. Understand middleware order requirements (especially streaming)
3. Handle both streaming and non-streaming requests appropriately
4. Add comprehensive tests including edge cases
5. Consider provider-specific behavior differences

### Configuration Changes

1. Update YAML schema in `internal/config/config.go`
2. Add validation logic in `Validate()` methods
3. Update environment variable expansion if needed
4. Test configuration loading and merging
5. Update example configurations in `configs/` directory

The codebase emphasizes modularity, comprehensive testing, and robust error handling. When implementing new features, follow the established patterns for provider registration, middleware ordering, and configuration management.
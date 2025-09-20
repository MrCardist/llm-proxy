# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is a Go-based LLM proxy server that forwards requests to OpenAI, Anthropic, and Gemini providers and does cost tracking. Please read the text in in LLM-proxy-article.md to understand the purpose of the proxy

## New Purpose

Now we want to add additional features to this proxy because its code base is small and simple. We want to add these features:

-   debug middleware: User llm request real-time debug: it should be possible to start the proxy foreground with an –-llm-debug option that only shows the client request (headers and json) in one color and the response of the backend llm (also json) in a different color. Show json well formatted in a Terminal ANSI color
-   Implement prefixes such as /qwen/ that fix certain issues with locally hosted LLM and make them truly openai compatible, there the issues are frequently because of mislabled llm reasoning content. All reasoning should be wrapped in “think” tags, for example :  
    \<think\>this is reasoning content\</think\>this is normal content. But
-   Claude Code proxy, under a prefix for example /cc-qwen/ we want to export the anthropic api as it is used for claude code to the end user and we want to redirect the call to an open-source model such as qwen that us installed locally on our GPU and launched via vllm. Please see markdown doc custom-claude-code-proxy.md for a similar project implemented in python, the challenge there was that the llm had the parameter max_completion_tokens instead of the legacy max_tokens

## New Purpose implementation

First setup a .env.example file with defaults and commented environment vars, then make sure you can compile the binary and, launch it and parse the output, then start the implementation with debug middleware and new providers and finally claude code , test frequently   
  
Then follow these instructions that we developed in a Q+A

-   What's our local vllm server URL?   
    There are multiple local vLLM endpoints and there should be a list of urls per provider, these are used in round robin and failover mode
-   What model names would you like to be mapped?   
    The specific model names I am hosting locally are “openai/gpt-oss-120b” and “qwen/qwen3-next-80b-a3b-thinking”, these are each hosted on different machines and ports.
-   Reasoning detection: Should \<think\> wrapping be only when certain patterns are detected and Configurable per model?  
    Yes, for example Qwen only shows a \</think\> tag at the end of the thinking process but not at the beginning, so the modification must be a pattern detection of thinking, in this case insert \<think\> at the beginning of the response
-   What is the config approach?   
    continue to use YAML config for endpoints/models and .env file, only use command line for –-llm-debug option
-   Error handling: How should the proxy handle when local LLM is unavailable?  
    It should respond with a normal error message: Sorry the backend server XXX is currently not available, please try again later or contact your Sysadmin

Note that claude code should be able to start the proxy binary and be able to parse all debug and log output to learn and code

Finally Claude Opus had a few clarifying Architecture Questions:

1\. Model-to-Endpoint Mapping: You have 2 models on different machines/ports. Should the configuration be:

\- Model openai/gpt-oss-120b → specific list of endpoints?

\- Model qwen/qwen3-next-80b-a3b-thinking → different list of endpoints?

\- Or should all endpoints serve both models (true round-robin)?  
Answer: this is by provider, so I have a new provider called gpt-oss and this provider has a model called openai/gpt-oss-120b and each model within that provider may have a distinct list of endpoints

2\. Failover Logic: For the round-robin + failover:

\- Should it try next endpoint immediately on connection failure?

\- What about timeout duration before marking endpoint as "down"?

\- Should failed endpoints be retried periodically (health checks)?  
  
Answer: The next endpoint should be tried immediately, and the timeout should be 100 milliseconds. The failed endpoint should be tried again immediately if the round robin think it is its turn,

3\. Request Routing: For the different prefixes:

\- /qwen/ → routes to which model specifically? (the qwen one?)

\- /cc-qwen/ → accepts Claude format, but routes to which backend model?

\- Should /openai/ requests also be routable to local models?  
/openai/ requests should not be routed to local models

4\. Thinking Pattern Detection:

\- For responses that end with \</think\> but no opening tag, should we:

\- Scan backwards from \</think\> to find where reasoning started?

\- Use a heuristic (like "Let me think", "First, I should", etc.)?

\- Or wrap the entire response up to \</think\>?

Answer: you should scan backwards from \</think\> to the beginning of the response as the reasoning content starts at the beginning of the response

5\. API Key Handling:

\- Should local LLM endpoints require API keys?

\- How should the proxy handle authentication for local models?  
Answer: keys are optional, they local llm moels may or may not require keys

6\. Streaming Behavior:

\- For the \<think\> tag insertion in streaming responses, should we:

\- Buffer initial chunks to detect thinking patterns?

\- Or inject \<think\> immediately and handle closing tag when detected?  
Answer: inject \<think\> immediately and handle closing tag when detected but only do this for Qwen

7\. Debug Output Format:

\- Should --llm-debug show:

\- Just the request/response bodies?

\- Or also metadata like endpoint selected, retry attempts, latency?

\- Should it follow the proxy's stdout or separate file/stream?

Answer: It should show all the information that one would pass to curl, the proxy's stdout should not show any other errors if --llm-debug is active

```
Final Implementation Plan Summary

  1. Provider Structure

  - New providers: gpt-oss and qwen
  - Each provider has models with their own endpoint lists
  - Each provider has a default_model for when client doesn't specify

  2. Routing Map

  - /gpt-oss/* → gpt-oss provider (OpenAI format)
  - /qwen/* → qwen provider (OpenAI format + think tag fixes)
  - /cc-qwen/* → Claude Code proxy (accepts Anthropic format → converts to OpenAI → routes to qwen provider)
  - /openai/* → real OpenAI only (no local routing)

  3. Think Tag Processing

  - Non-streaming: If response contains </think> anywhere, prepend <think> at start (unless already present)
  - Streaming: Always inject <think> at stream start if model name ends with -thinking (unless already present in first chunk)
  - Only applied to qwen provider responses
  - Edge case: If response already starts with <think>, do NOT prepend another one

  4. Model Handling

  - Never modify/override model names from client requests
  - If request has no model specified, use provider's default_model
  - Models are passed through as-is to backend endpoints

  5. Endpoint Management

  - Random selection from available endpoints (no stateful round-robin to simplify)
  - 100ms timeout, immediate failover to next endpoint
  - Failed endpoints immediately rejoin rotation (no blacklisting)
  - URLs in config must include full path (e.g., "/v1"), proxy does NOT append paths

  6. Debug Mode (--llm-debug)

  - Shows curl-equivalent: method, URL, headers, JSON body
  - Shows actual endpoint URL selected
  - Shows retry attempts if first endpoint fails
  - Shows timing information (request duration)
  - Request in cyan, response in green
  - Pretty-printed JSON
  - Suppresses all other logs when active

  7. Error Handling

  - All errors returned in OpenAI JSON format (even for /cc-qwen/*)
  - Connection failures: "Sorry the backend server XXX is currently not available..."

  8. Configuration Example

  providers:
    gpt-oss:
      enabled: true
      default_model: "openai/gpt-oss-120b"  # used if client doesn't specify
      models:
        openai/gpt-oss-120b:
          endpoints:
            - url: "http://192.168.1.100:8000/v1"  # full path including /v1
              api_key: ""  # optional
            - url: "http://192.168.1.101:8000/v1"

    qwen:
      enabled: true
      default_model: "qwen/qwen3-next-80b-a3b-thinking"
      models:
        qwen/qwen3-next-80b-a3b-thinking:  # ends with '-thinking' → streaming <think> injection
          endpoints:
            - url: "http://192.168.1.200:8001/v1"  # full path including /v1
            - url: "http://192.168.1.201:8001/v1"

    claude_code_proxy:  # special provider for /cc-qwen/
      enabled: true
      target_provider: "qwen"
      target_model: "qwen/qwen3-next-80b-a3b-thinking"  # always uses this model
      parameter_mapping:
        max_tokens: "max_completion_tokens"  # Claude → OpenAI parameter conversion

  9. Request Flow Examples

  Example 1: /qwen/v1/chat/completions with "model": "qwen/qwen3-next-80b-a3b-thinking"
  - Routes to qwen provider
  - Uses specified model's endpoints
  - Response gets <think> tag processing

  Example 2: /cc-qwen/v1/messages (Claude format) with "model": "claude-3-sonnet"
  - Converts Anthropic → OpenAI format
  - Always uses configured target_model: qwen/qwen3-next-80b-a3b-thinking
  - Converts max_tokens → max_completion_tokens
  - Response gets <think> processing + OpenAI → Anthropic format conversion

  Example 3: /gpt-oss/v1/chat/completions with no model specified
  - Uses default_model: openai/gpt-oss-120b
  - No think tag processing

  10. Testing Order

  - First: Get basic compilation + .env.example working (with default ports 8000, 8001, etc.)
  - Second: Debug middleware (--llm-debug flag)
  - Third: Local providers (gpt-oss, qwen)
  - Fourth: Claude Code proxy (/cc-qwen/)
  - Each step with test checkpoint before proceeding

  11. Implementation Details

  - .env.example will include default local LLM ports (8000, 8001)
  - Round-robin is stateless random selection for simplicity
  - Endpoint URLs must be complete (proxy never modifies them)
  - Debug mode shows all retry details and selected endpoints
```

**Everything below is content from the original project:**

## Common Development Commands

### Build and Run

```bash
# Install dependencies and build
make install build

# Run the proxy server (development mode)
make dev

# Run the built binary
make run

# Quick start (install + build + ready to run)
make quick-start
```

### Testing

```bash
# Run unit tests only
make test

# Run all tests including integration tests (requires API keys)
make test-all

# Run provider-specific tests
make test-openai
make test-anthropic  
make test-gemini

# Run health check tests
make test-health
```

### Code Quality

```bash
# Run all code quality checks (format + vet + lint)
make check

# Individual quality checks
make fmt    # Format Go code
make vet    # Run go vet
make lint   # Run golint
```

### Environment Setup

Set these environment variables for integration testing:

```bash
export OPENAI_API_KEY=your_openai_key
export ANTHROPIC_API_KEY=your_anthropic_key
export GEMINI_API_KEY=your_gemini_key
```

Check environment variables: `make env-check`

## Architecture Overview

### Core Components

**Main Entry Point**: `cmd/llm-proxy/main.go` - Server setup, middleware registration, and provider coordination

**Provider System** (`internal/providers/`):

-   `provider.go` - Core interfaces and provider management
-   `openai.go`, `anthropic.go`, `gemini.go` - Provider-specific implementations
-   Each provider implements streaming detection, request proxying, response metadata parsing, and health checking

**Configuration** (`internal/config/config.go`):

-   YAML-based configuration with environment-specific overlays
-   Supports cost tracking, rate limiting, and API key management features
-   Base config in `configs/base.yml`, environment configs in `configs/{env}.yml`

**Middleware** (`internal/middleware/`):

-   Modular middleware system with specific order requirements
-   URL rewriting for meta routes (`/meta/{userID}/provider/`)
-   API key validation, rate limiting, CORS, logging, token parsing, streaming

**Features**:

-   **Cost Tracking** (`internal/cost/`) - Multi-transport system (file, DynamoDB, Datadog)
-   **Rate Limiting** (`internal/ratelimit/`) - Memory and Redis backends with token estimation
-   **API Key Management** (`internal/apikeys/`) - DynamoDB-based key store with `iw:` prefix support

### Provider Registration Pattern

Each provider implements the `Provider` interface and gets registered in main.go:

-   Route registration for direct (`/provider/`) and meta (`/meta/{userID}/provider/`) patterns
-   Streaming detection and response metadata extraction
-   Health status reporting and API key validation

### Configuration Loading

1.  Load `configs/base.yml`
2.  Overlay `configs/{ENVIRONMENT}.yml` (defaults to `dev`)
3.  Validate configuration including transport and rate limiting settings
4.  Parse model pricing structures for cost tracking

### Middleware Order (Critical)

1.  MetaURLRewritingMiddleware (must be first)
2.  APIKeyValidationMiddleware (if enabled)
3.  LoggingMiddleware
4.  RateLimitingMiddleware (if enabled)
5.  CORSMiddleware
6.  TokenParsingMiddleware (with cost tracking callbacks)
7.  StreamingMiddleware (must be last)

## Development Notes

-   **Go Version**: 1.24.5 (see go.mod)
-   **Router**: Gorilla Mux for HTTP routing
-   **Streaming**: Native streaming support across all providers with proper middleware handling
-   **Testing**: Integration tests require real API keys, unit tests use `-short -skip "Integration"`
-   **Logging**: Structured logging with slog, supports both pretty (dev) and JSON (prod) formats
-   **Docker**: Multi-environment support (dev/prod) with docker-compose configurations
-   **Rate Limiting**: Supports token estimation via Content-Length and message parsing with provider-specific character-per-token ratios

## Configuration Validation

Validate configuration files: `make validate-config configs/base.yml,configs/dev.yml`

Use `--version` flag to see loaded configuration and verify setup.

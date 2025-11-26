# Implementation Plan: Local LLM Support & Claude Code Proxy

This document captures the planning and requirements for adding local LLM support and Claude Code proxy functionality to the llm-proxy.

## New Features Overview

1. **Debug middleware**: Real-time LLM request debugging with `--llm-debug` flag showing colored curl-equivalent output
2. **Local LLM prefixes** (`/qwen/`, `/gpt-oss/`): Fix issues with locally hosted LLMs, particularly reasoning content tagging
3. **Claude Code proxy** (`/cc-qwen/`): Export Anthropic API for Claude Code, routing to local models like Qwen via vLLM

## Requirements Q&A

### Local vLLM Server Configuration
- Multiple local vLLM endpoints per provider
- Used in round-robin and failover mode
- Model names hosted locally:
  - `openai/gpt-oss-120b`
  - `qwen/qwen3-next-80b-a3b-thinking`
- Each hosted on different machines and ports

### Reasoning Detection
- Qwen only shows `</think>` tag at end of thinking, not at beginning
- Solution: Pattern detection - if `</think>` found, insert `<think>` at beginning of response
- Only apply to Qwen provider responses

### Configuration Approach
- YAML config for endpoints/models
- `.env` file for environment variables
- Command line only for `--llm-debug` option

### Error Handling
- Return friendly error: "Sorry the backend server XXX is currently not available, please try again later or contact your Sysadmin"

## Architecture Decisions

### 1. Model-to-Endpoint Mapping
- By provider: `gpt-oss` provider has model `openai/gpt-oss-120b`
- Each model within a provider may have a distinct list of endpoints

### 2. Failover Logic
- Try next endpoint immediately on connection failure
- 100ms timeout before trying next endpoint
- Failed endpoints immediately rejoin rotation (no blacklisting)

### 3. Request Routing
- `/qwen/` → routes to qwen provider
- `/cc-qwen/` → accepts Claude format, routes to qwen backend
- `/openai/` → real OpenAI only (no local routing)

### 4. Thinking Pattern Detection
- Scan backwards from `</think>` to beginning of response
- Reasoning content starts at beginning of response
- Wrap entire content up to `</think>` in `<think>` tags

### 5. API Key Handling
- Keys are optional for local LLM endpoints
- Local models may or may not require keys

### 6. Streaming Behavior
- Inject `<think>` immediately at stream start
- Handle closing tag when detected
- Only apply to Qwen provider

### 7. Debug Output Format
- Show all information that one would pass to curl
- Suppress all other logs when `--llm-debug` is active
- Request in cyan, response in green
- Pretty-printed JSON

## Final Implementation Plan Summary

### 1. Provider Structure
- New providers: `gpt-oss` and `qwen`
- Each provider has models with their own endpoint lists
- Each provider has a `default_model` for when client doesn't specify

### 2. Routing Map
- `/gpt-oss/*` → gpt-oss provider (OpenAI format)
- `/qwen/*` → qwen provider (OpenAI format + think tag fixes)
- `/cc-qwen/*` → Claude Code proxy (accepts Anthropic format → converts to OpenAI → routes to qwen provider)
- `/openai/*` → real OpenAI only (no local routing)

### 3. Think Tag Processing
- **Non-streaming**: If response contains `</think>` anywhere, prepend `<think>` at start (unless already present)
- **Streaming**: Always inject `<think>` at stream start if model name ends with `-thinking` (unless already present in first chunk)
- Only applied to qwen provider responses
- **Edge case**: If response already starts with `<think>`, do NOT prepend another one

### 4. Model Handling
- Never modify/override model names from client requests
- If request has no model specified, use provider's `default_model`
- Models are passed through as-is to backend endpoints

### 5. Endpoint Management
- Random selection from available endpoints (no stateful round-robin to simplify)
- 100ms timeout, immediate failover to next endpoint
- Failed endpoints immediately rejoin rotation (no blacklisting)
- URLs in config must include full path (e.g., `/v1`), proxy does NOT append paths

### 6. Debug Mode (`--llm-debug`)
- Shows curl-equivalent: method, URL, headers, JSON body
- Shows actual endpoint URL selected
- Shows retry attempts if first endpoint fails
- Shows timing information (request duration)
- Request in cyan, response in green
- Pretty-printed JSON
- Suppresses all other logs when active

### 7. Error Handling
- All errors returned in OpenAI JSON format (even for `/cc-qwen/*`)
- Connection failures: "Sorry the backend server XXX is currently not available..."

### 8. Configuration Example

```yaml
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
```

### 9. Request Flow Examples

**Example 1**: `/qwen/v1/chat/completions` with `"model": "qwen/qwen3-next-80b-a3b-thinking"`
- Routes to qwen provider
- Uses specified model's endpoints
- Response gets `<think>` tag processing

**Example 2**: `/cc-qwen/v1/messages` (Claude format) with `"model": "claude-3-sonnet"`
- Converts Anthropic → OpenAI format
- Always uses configured `target_model`: `qwen/qwen3-next-80b-a3b-thinking`
- Converts `max_tokens` → `max_completion_tokens`
- Response gets `<think>` processing + OpenAI → Anthropic format conversion

**Example 3**: `/gpt-oss/v1/chat/completions` with no model specified
- Uses `default_model`: `openai/gpt-oss-120b`
- No think tag processing

### 10. Testing Order
1. Get basic compilation + `.env.example` working (with default ports 8000, 8001, etc.)
2. Debug middleware (`--llm-debug` flag)
3. Local providers (gpt-oss, qwen)
4. Claude Code proxy (`/cc-qwen/`)
5. Each step with test checkpoint before proceeding

### 11. Implementation Details
- `.env.example` includes default local LLM ports (8000, 8001)
- Round-robin is stateless random selection for simplicity
- Endpoint URLs must be complete (proxy never modifies them)
- Debug mode shows all retry details and selected endpoints

## Reference Documents

- `custom-claude-code-proxy.md` - Python implementation reference for Claude Code proxy
- `LLM-proxy-article.md` - Original project purpose and architecture

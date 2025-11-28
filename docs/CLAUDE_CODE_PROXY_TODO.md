# Claude Code Proxy Implementation TODO

## Configuration Answers

| Question | Answer |
|----------|--------|
| Tool calling support on Qwen | Yes, OpenAI-style function calling supported |
| Model mapping | Always route to single Qwen backend |
| Thinking content blocks | Convert `<think>` tags to proper Claude thinking blocks |
| True streaming | Address later (buffered OK for now) |
| Model ID format | AWS Bedrock IDs for now (e.g., `us.anthropic.claude-haiku-4-5-20251001-v1:0`) |
| Test endpoint | `http://dgx01.arcs.oregonstate.edu:8005/v1/` |
| Model name on endpoint | `qwen/qwen3-next-80b-a3b-thinking` |
| Authentication | None required (open endpoint) |
| Extended thinking parameter | Ignore - always enabled on backend |
| Prompt caching (`cache_control`) | Ignore/strip |
| Vision/screenshots | Not supported - ignore image blocks |

## Supported Claude Models

Accept these Bedrock model IDs (all route to same Qwen backend):
- `us.anthropic.claude-haiku-4-5-20251001-v1:0`
- `us.anthropic.claude-sonnet-4-5-20250929-v1:0`
- `global.anthropic.claude-opus-4-5-20251101-v1:0`

## API Reference

- Claude API docs: https://docs.anthropic.com/en/api/messages

## Implementation Tasks

### Phase 1: Core Fixes (Required for Claude Code) ✅ COMPLETE

- [x] **System prompt handling**
  - Convert Anthropic `system` parameter to OpenAI system message
  - Insert as first message with `role: "system"`

- [x] **Tool definitions (request)**
  - Convert Claude tool format to OpenAI function format
  - Claude: `{name, description, input_schema}` → OpenAI: `{type: "function", function: {name, description, parameters}}`

- [x] **Tool use in responses**
  - Convert OpenAI `tool_calls` to Claude `tool_use` content blocks
  - Map `function.arguments` (JSON string) → `input` (object)
  - Generate appropriate tool_use IDs

- [x] **Tool results in requests**
  - Convert Claude `tool_result` content blocks to OpenAI tool messages
  - Claude: `{role: "user", content: [{type: "tool_result", tool_use_id, content}]}`
  - OpenAI: `{role: "tool", tool_call_id, content}`

- [x] **Thinking content blocks**
  - Parse `<think>...</think>` from Qwen response
  - Convert to Claude thinking content block: `{type: "thinking", thinking: "..."}`
  - Place before text content in response

### Phase 2: Model & Header Handling ✅ COMPLETE

- [x] **Accept Bedrock model IDs**
  - Parse incoming model names (Bedrock format)
  - Always use configured target model for backend
  - Return appropriate model name in response

- [x] **Anthropic headers**
  - Handle `anthropic-version` header (accepted, passed through)
  - `x-api-key` not required for local LLM endpoint

### Phase 3: Content Block Handling ✅ COMPLETE

- [x] **Mixed content blocks in requests**
  - Handle messages with multiple content block types
  - Strip `image` blocks (silently ignored, not supported)
  - Strip `cache_control` fields (not included in OpenAI request)

- [x] **Multiple content blocks in responses**
  - Support returning both `thinking` and `text` blocks
  - Support returning both `text` and `tool_use` blocks

### Phase 4: Streaming ✅ COMPLETE

- [x] **Buffered streaming**
  - Converts OpenAI SSE to Claude SSE format

- [x] **True streaming with think block separation**
  - Detects `<think>` tag at stream start and emits `content_block_start` with type "thinking"
  - Streams thinking content via `thinking_delta` events in real-time
  - Detects `</think>` tag and transitions to text block with `text_delta` events
  - Properly closes content blocks with `content_block_stop` events

### Phase 5: Standard Model IDs ✅ COMPLETE

- [x] **Accept standard Anthropic model IDs**
  - `claude-*`, `anthropic.*` patterns accepted
  - Also accepts `qwen/*` and `*-thinking` models
  - All route to configured target model

## Format Reference

### Claude Tool Definition
```json
{
  "name": "get_weather",
  "description": "Get weather for a location",
  "input_schema": {
    "type": "object",
    "properties": {
      "location": {"type": "string"}
    },
    "required": ["location"]
  }
}
```

### OpenAI Tool Definition
```json
{
  "type": "function",
  "function": {
    "name": "get_weather",
    "description": "Get weather for a location",
    "parameters": {
      "type": "object",
      "properties": {
        "location": {"type": "string"}
      },
      "required": ["location"]
    }
  }
}
```

### Claude Tool Use Response
```json
{
  "content": [
    {
      "type": "tool_use",
      "id": "toolu_01ABC123",
      "name": "get_weather",
      "input": {"location": "San Francisco"}
    }
  ]
}
```

### OpenAI Tool Call Response
```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "tool_calls": [{
        "id": "call_abc123",
        "type": "function",
        "function": {
          "name": "get_weather",
          "arguments": "{\"location\": \"San Francisco\"}"
        }
      }]
    }
  }]
}
```

### Claude Tool Result (in request)
```json
{
  "role": "user",
  "content": [
    {
      "type": "tool_result",
      "tool_use_id": "toolu_01ABC123",
      "content": "72°F, sunny"
    }
  ]
}
```

### OpenAI Tool Result (in request)
```json
{
  "role": "tool",
  "tool_call_id": "call_abc123",
  "content": "72°F, sunny"
}
```

### Claude Thinking Block
```json
{
  "type": "thinking",
  "thinking": "Let me analyze this step by step..."
}
```

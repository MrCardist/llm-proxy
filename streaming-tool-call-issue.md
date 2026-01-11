# Streaming Tool_Calls Implementation Issues

## Overview

This document describes issues found in the streaming tool_calls implementation in the ClaudeCodeCloud provider (`internal/providers/claude_code_cloud.go`). While basic streaming tool_calls support exists, several edge cases and the forced-streaming path have incomplete implementations.

**IMPORTANT DISTINCTION**: There are TWO separate categories of issues:

1. **Backend Issues** (sglang/Fireworks/model-level) - Invalid JSON generation from inference engines
2. **Proxy Issues** (llm-proxy code) - Missing tool_calls handling in forced-streaming path

Both issues can occur independently or compound each other.

## Backend Issue: GLM-4.7 Invalid JSON Generation

**Reference**: https://github.com/sgl-project/sglang/issues/16371

**Nature**: Upstream bug in sglang inference engine or GLM-4.7 model

**Problem**: When GLM-4.7 generates multiple tool calls in streaming mode, it concatenates JSON objects without proper delimiters:

```json
// Invalid output from backend:
{"name": "search", "arguments": {"query": "test"}}{"name": "read_file", "arguments": {"path": "foo.txt"}}

// Instead of valid JSON array:
[
  {"name": "search", "arguments": {"query": "test"}},
  {"name": "read_file", "arguments": {"path": "foo.txt"}}
]
```

**Symptoms**:
- `JSONDecodeError` when parsing tool_use `input_json_delta` events
- Multi-step agentic workflows fail in Claude Code
- Happens at the SSE stream generation level in sglang/vLLM

**Impact**: This is a **backend/model issue**, not something the proxy can fix. The proxy receives already-malformed JSON from Fireworks/sglang and forwards it. Workarounds would require:
- Backend fix in sglang/vLLM inference engine
- Or proxy-side JSON parsing and reconstruction (complex, error-prone)
- Or using different models (DeepSeek V3, Kimi K2) that don't have this bug

**Status**: Reported upstream to sglang project. May affect Fireworks-hosted GLM-4.7 if they use sglang.

### How to Verify: Backend vs Proxy Issue

To determine whether tool_calls issues are from the backend or the proxy, test directly against Fireworks API:

```bash
# Test Fireworks API directly (bypass proxy)
curl -N https://api.fireworks.ai/inference/v1/chat/completions \
  -H "Authorization: Bearer $FIREWORKS_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "accounts/fireworks/models/glm-4p7",
    "max_tokens": 8000,
    "stream": true,
    "messages": [
      {"role": "user", "content": "Search for Python and then read the file results.txt"}
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "search",
          "parameters": {"type": "object", "properties": {"query": {"type": "string"}}}
        }
      },
      {
        "type": "function",
        "function": {
          "name": "read_file",
          "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}
        }
      }
    ]
  }'
```

**If Fireworks returns valid JSON**: The issue is in the proxy code (Issues 1-5 below)
**If Fireworks returns malformed JSON** (concatenated objects): The issue is upstream in sglang/Fireworks

**Expected valid response format** (from Fireworks, OpenAI-compatible):
```json
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"{\"query\":"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Python\"}"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"results.txt\"}"}}]}}]}
data: [DONE]
```

Each tool_call should have a unique `index` and arguments should stream as valid JSON fragments.

## Proxy Issues (llm-proxy Code)

The following issues are in our proxy implementation and can be fixed independently of the backend issue above.

## Issue 1: Tool_Calls Lost in Forced-Streaming Path (CRITICAL)

**Severity**: High - Silent data loss

**Location**: `handleForcedStreamingRequest()` (lines 776-852 in `claude_code_cloud.go`)

**Problem**: When the proxy forces streaming for Fireworks backend (when `max_tokens > 4096`) but the client requested a non-streaming response, tool_calls are completely dropped.

**Current Implementation**:
```go
// Line 798-800: Only accumulates text content
if content, ok := delta["content"].(string); ok {
    fullContent += content
}
```

The code only captures `delta["content"]` (text) and ignores `delta["tool_calls"]` entirely.

**Impact**:
- Clients requesting non-streaming responses with `max_tokens > 4096` that receive tool_calls from the backend will get incomplete responses
- Tool_use blocks are silently lost
- `stop_reason` will incorrectly show `"end_turn"` instead of `"tool_use"`
- Affects Fireworks GLM, Kimi K2, and other models that emit tool_calls

**Example Scenario**:
```json
// Client request
POST /cc/v1/messages
{
  "model": "hc/glm-4.7",
  "max_tokens": 8000,
  "stream": false,
  "messages": [...],
  "tools": [...]
}

// Backend streams (Fireworks forced streaming):
data: {"choices":[{"delta":{"content":"Let me help"}}]}
data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","function":{"name":"search","arguments":"{\"query\":\"test\"}"}}]}}]}
data: [DONE]

// Client receives (tool_call lost):
{
  "content": [{"type": "text", "text": "Let me help"}],
  "stop_reason": "end_turn"  // Wrong!
}
```

**Fix Required**:
1. Track tool_calls during streaming accumulation (similar to `handleStreamingRequest`)
2. Rebuild complete tool_use content blocks before returning
3. Set proper `stop_reason = "tool_use"` when tool_calls were present
4. Convert OpenAI tool_calls format to Anthropic tool_use blocks

## Issue 2: Unused Tool_Call Argument Accumulator

**Severity**: Low - Code cleanliness

**Location**: `handleStreamingRequest()` lines 1243-1244

**Problem**: The code accumulates tool_call arguments in a `strings.Builder` buffer but never uses it.

**Current Implementation**:
```go
// Line 911-917: State structure
type toolCallState struct {
    id        string
    name      string
    arguments strings.Builder  // Accumulated but never used
    started   bool
    index     int
}

// Line 1243-1244: Accumulation
if arguments, ok := function["arguments"].(string); ok {
    tcState.arguments.WriteString(arguments)
}
```

**Why It's Orphaned**:
- Tool_use blocks are created with empty `input: {}` (line 1284)
- Arguments are sent as streaming `input_json_delta` events directly (line 1300)
- The accumulated buffer is never read or used to construct the final tool_use block
- For streaming responses, this is actually correct behavior (arguments stream incrementally)

**Impact**: None functionally, but creates confusion and wastes memory

**Fix Options**:
1. Remove the `arguments` field from `toolCallState` struct (not needed for streaming)
2. Or use it to validate complete JSON at end of stream (defensive programming)

## Issue 3: Potential Index Tracking Inconsistency

**Severity**: Medium - May cause index mismatches

**Location**: `handleStreamingRequest()` line 1287

**Problem**: Block index tracking may become inconsistent with multiple sequential tool_calls.

**Current Implementation**:
```go
// Line 1278: Assign current index to tool call state
tcState.index = currentBlockIndex

// Line 1287: Increment after sending content_block_start
currentBlockIndex++

// Line 1296: Use tcState.index for delta events
p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
    "type":  "content_block_delta",
    "index": tcState.index,  // Uses stored index
    "delta": map[string]interface{}{
        "type":         "input_json_delta",
        "partial_json": arguments,
    },
})
```

**Issue**:
- `tcState.index` is set once when the tool call is first seen
- `currentBlockIndex` increments after sending `content_block_start`
- If multiple tool_calls arrive in sequence, each gets the correct index
- However, if text content arrives between tool_calls, it may cause confusion

**Example Problematic Sequence**:
```
1. Text block starts → index 0, currentBlockIndex → 1
2. Tool call 0 starts → tcState[0].index = 1, currentBlockIndex → 2
3. Tool call 1 starts → tcState[1].index = 2, currentBlockIndex → 3
4. More arguments for tool 0 → uses tcState[0].index = 1 ✓
5. More arguments for tool 1 → uses tcState[1].index = 2 ✓
```

This appears to work correctly, but the logic is subtle and could break if:
- Text blocks interleave with tool_call chunks
- Reasoning blocks appear between tool calls
- The backend sends tool_calls in a non-standard order

**Fix Recommendation**: Add comments explaining the index tracking logic clearly, or refactor to make it more explicit.

## Issue 4: Incomplete Tool_Call Closure on [DONE]

**Severity**: Low - Edge case

**Location**: `handleStreamingRequest()` lines 961-968

**Problem**: Tool calls that never received a function name won't be properly closed.

**Current Implementation**:
```go
// Line 961-968: Close open tool_use blocks
for _, tc := range toolCalls {
    if tc.started {
        p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
            "type":  "content_block_stop",
            "index": tc.index,
        })
    }
}
```

**Scenario**:
1. Backend sends `tool_calls[0]` with only `{"index": 0, "id": "call_abc"}`
2. Stream ends with `[DONE]` before function name arrives
3. `tc.started` is still `false` (only set to true after name is received, line 1280)
4. No `content_block_stop` event is sent
5. Client may be waiting for a block that never completes

**Impact**: Rare edge case, likely only occurs with broken/interrupted streams

**Fix**: Either:
- Send `content_block_stop` for all tool_calls regardless of `started` state
- Or add error handling for incomplete tool_calls

## Issue 5: No Tool_Use ID Normalization in Forced-Streaming Path

**Severity**: Medium - Inconsistent behavior

**Location**: `handleForcedStreamingRequest()` lines 825-830

**Problem**: Tool_use IDs are normalized to `toolu_xxx` format in the normal streaming path (line 1275) but this normalization is missing in the forced-streaming path.

**Current Implementation (streaming path)**:
```go
// Line 1270-1275: Normalize ID to Claude format
id := toolCallID
if !strings.HasPrefix(id, "toolu_") {
    id = "toolu_" + toolCallID
}
```

**Missing in forced-streaming path**: No equivalent normalization exists when reconstructing tool_use blocks from accumulated content.

**Impact**:
- Backend tool IDs like `call_abc123` will be passed through unchanged
- Breaks consistency with Anthropic API where all tool_use IDs start with `toolu_`
- Claude Code may reject responses with non-standard tool IDs

## Architecture Context

### Data Flow for Streaming Tool_Calls

```
┌─────────────────┐
│ Client Request  │
│ stream=true     │
│ tools=[...]     │
└────────┬────────┘
         │
         v
┌─────────────────────────────┐
│ ClaudeCodeCloud Provider    │
│ handleStreamingRequest()    │
└────────┬────────────────────┘
         │
         v
┌─────────────────────────────┐
│ Backend (Fireworks/OpenAI)  │
│ Returns SSE stream with:    │
│ - delta.content (text)      │
│ - delta.tool_calls[]        │
└────────┬────────────────────┘
         │
         v
┌─────────────────────────────┐
│ OpenAI → Anthropic Convert  │
│ tool_calls → tool_use        │
│ - Normalize IDs (toolu_xxx) │
│ - Track per-tool state      │
│ - Stream input_json_delta   │
└────────┬────────────────────┘
         │
         v
┌─────────────────────────────┐
│ Anthropic SSE Events:       │
│ content_block_start         │
│ content_block_delta         │
│ content_block_stop          │
└─────────────────────────────┘
```

### Files Affected

1. **`internal/providers/claude_code_cloud.go`**
   - `handleStreamingRequest()` - Main streaming implementation (mostly works)
   - `handleForcedStreamingRequest()` - **Broken for tool_calls**
   - `convertOpenAIToAnthropic()` - Non-streaming conversion (works)

2. **`internal/providers/claude_code_proxy.go`**
   - Similar streaming implementation but for `/cc-qwen/` endpoint
   - Also lacks tool_calls support in forced-streaming path

3. **`internal/middleware/streaming.go`**
   - Ensures response writer supports flushing
   - Not related to tool_calls specifically

## Testing Scenarios

### Scenario 1: Normal Streaming with Tool_Calls (WORKS)
```bash
curl -N https://llm.example.edu/cc/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "hc/glm-4.7",
    "max_tokens": 1000,
    "stream": true,
    "messages": [...],
    "tools": [{"name": "search", ...}]
  }'
```
**Result**: Tool_use blocks stream correctly with `content_block_start`, `content_block_delta`, `content_block_stop` events.

### Scenario 2: Non-Streaming with Tool_Calls, max_tokens ≤ 4096 (WORKS)
```bash
curl https://llm.example.edu/cc/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "hc/glm-4.7",
    "max_tokens": 4000,
    "stream": false,
    "messages": [...],
    "tools": [{"name": "search", ...}]
  }'
```
**Result**: Backend returns non-streaming response, tool_use blocks converted correctly.

### Scenario 3: Non-Streaming with Tool_Calls, max_tokens > 4096 (BROKEN)
```bash
curl https://llm.example.edu/cc/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "hc/glm-4.7",
    "max_tokens": 8000,
    "stream": false,
    "messages": [...],
    "tools": [{"name": "search", ...}]
  }'
```
**Result**: Proxy forces streaming internally, accumulates response, but **tool_use blocks are lost**. Only text content is returned.

## Recommended Fixes

### Priority 1: Fix Forced-Streaming Tool_Calls Loss

Add tool_calls tracking to `handleForcedStreamingRequest()`:

```go
// Add to handleForcedStreamingRequest
var fullContent string
var allToolCalls []map[string]interface{}  // NEW: Track tool_calls

scanner := bufio.NewScanner(backendResp.Body)
for scanner.Scan() {
    line := scanner.Text()
    if !strings.HasPrefix(line, "data: ") {
        continue
    }

    data := strings.TrimPrefix(line, "data: ")
    if data == "[DONE]" {
        break
    }

    var streamData map[string]interface{}
    if err := json.Unmarshal([]byte(data), &streamData); err != nil {
        continue
    }

    choices := streamData["choices"].([]interface{})
    choice := choices[0].(map[string]interface{})
    delta := choice["delta"].(map[string]interface{})

    // Existing: Accumulate text content
    if content, ok := delta["content"].(string); ok {
        fullContent += content
    }

    // NEW: Accumulate tool_calls
    if toolCalls, ok := delta["tool_calls"].([]interface{}); ok {
        for _, tc := range toolCalls {
            toolCall := tc.(map[string]interface{})
            allToolCalls = append(allToolCalls, toolCall)
        }
    }
}

// NEW: Reconstruct tool_use blocks from accumulated tool_calls
toolUseBlocks := p.reconstructToolUseBlocks(allToolCalls)

// Build final response
response := map[string]interface{}{
    "id":      responseID,
    "type":    "message",
    "role":    "assistant",
    "model":   model,
    "content": append(p.parseThinkTagsToBlocks(fullContent), toolUseBlocks...),
    "stop_reason": determineStopReason(allToolCalls),  // "tool_use" if tool_calls present
    "usage": map[string]interface{}{...},
}
```

### Priority 2: Remove Unused Arguments Buffer

Remove the `arguments` field from `toolCallState` struct or document why it exists.

### Priority 3: Document Index Tracking Logic

Add detailed comments explaining how `currentBlockIndex` and `tcState.index` are managed.

### Priority 4: Normalize Tool IDs in All Paths

Ensure tool_use ID normalization happens in forced-streaming path as well.

## Related Code References

- `internal/providers/claude_code_cloud.go:776-852` - `handleForcedStreamingRequest()`
- `internal/providers/claude_code_cloud.go:858-1310` - `handleStreamingRequest()`
- `internal/providers/claude_code_cloud.go:407-471` - `convertOpenAIToAnthropic()`
- `internal/providers/claude_code_cloud.go:1270-1307` - Tool_call streaming logic

## Commit History

Recent commits related to streaming tool_calls:
- `e451f39` - feat: add streaming tool_calls and reasoning_content support
- `552f262` - fix: normalize tool_use IDs to Claude format (toolu_xxx)
- `9dfd4c2` - fix: collect streaming response for non-streaming clients when max_tokens > 4096
- `6ba4ad0` - feat: implement Claude Code Cloud (/cc) endpoint for open-source models

## Summary and Next Steps

### Issue Separation

**Backend Issue (sglang/GLM-4.7)**:
- Generates concatenated JSON objects for multiple tool calls: `{...}{...}`
- Cannot be fixed in the proxy without complex JSON parsing/reconstruction
- Workaround: Use different models (DeepSeek V3, Kimi K2) or wait for upstream fix
- Verify by testing Fireworks API directly (see "How to Verify" section above)

**Proxy Issue (llm-proxy)**:
- Tool_calls are completely dropped in forced-streaming path (`max_tokens > 4096`)
- CAN be fixed by adding tool_calls accumulation to `handleForcedStreamingRequest()`
- Independent of backend JSON formatting issues
- Priority fix: Prevents silent data loss

### Impact Matrix

| Scenario | Stream | max_tokens | Backend Issue | Proxy Issue | Result |
|----------|--------|------------|---------------|-------------|---------|
| Normal streaming | Yes | Any | May occur | No | Backend JSON may be malformed |
| Non-streaming | No | ≤ 4096 | No | No | ✅ Works correctly |
| Non-streaming | No | > 4096 | May occur | **Yes** | ❌ Tool_calls lost entirely |
| Forced streaming | Internal | > 4096 | May occur | **Yes** | ❌ Tool_calls lost + JSON may be malformed |

## Testing Results (2026-01-11)

### Phase 1: Fireworks Backend Test ✅

**Status**: BACKEND IS CLEAN

Tested Fireworks API directly with multiple tool_calls in streaming mode:
- Model: `accounts/fireworks/models/glm-4p7`
- Scenario: Request with 2 tools (search + read_file)

**Result**: Backend returns properly formatted tool_calls:
- ✅ Each tool_call has unique index (0, 1)
- ✅ Each tool_call has unique ID (`chatcmpl-tool-xxx`)
- ✅ Arguments are valid JSON strings
- ✅ Tool_calls arrive in separate SSE events
- ✅ Proper `finish_reason: "tool_calls"`

**No malformed JSON detected** - The backend issue described in sglang #16371 is NOT present in this configuration.

See `test-results/fireworks-backend-test.md` for detailed findings.

### Phase 2: Proxy Code Fix ✅

**Status**: FIX IMPLEMENTED AND VERIFIED

**Changes Made**:
- Modified `handleForcedStreamingRequest()` in `internal/providers/claude_code_cloud.go`
- Added tool_calls accumulation (lines 781-848)
- Added tool_calls conversion to tool_use blocks (lines 881-910)
- Fixed stop_reason logic to detect tool_use when tool_calls present (lines 912-926)
- Added `sort` import for ordering tool_calls by index

**What the fix does**:
1. Tracks accumulated tool_calls while collecting streaming response
2. Reconstructs complete tool_use content blocks from accumulated data
3. Sets correct `stop_reason: "tool_use"` when tool_calls are present
4. Maintains ID normalization to `toolu_xxx` format

### Phase 3: Automated Tests ✅

**Status**: ALL TESTS PASSING

Created focused unit tests in `internal/providers/claude_code_cloud_forced_streaming_test.go`:

1. **TestHandleForcedStreamingRequest_ToolCallsCollection** ✅
   - Verifies tool_calls are collected from streaming response
   - Confirms both tool call index 0 and 1 are properly accumulated
   - Validates tool call ID, name, and arguments are captured

2. **TestToolCallToToolUseConversion** ✅
   - Verifies tool_calls are converted to Claude tool_use format
   - Confirms ID normalization (functions.Name:0 → toolu_xxx)
   - Validates arguments are parsed into input map

3. **TestStopReasonDetermination** ✅
   - Tests all stop_reason scenarios:
     - With tool_calls → "tool_use" (even if finish_reason is "stop")
     - Without tool_calls → "end_turn", "max_tokens", etc.

**Test Results**:
```
=== RUN   TestHandleForcedStreamingRequest_ToolCallsCollection
--- PASS: TestHandleForcedStreamingRequest_ToolCallsCollection (0.00s)
=== RUN   TestToolCallToToolUseConversion
--- PASS: TestToolCallToToolUseConversion (0.00s)
=== RUN   TestStopReasonDetermination
--- PASS: TestStopReasonDetermination (0.00s)
PASS
ok	github.com/Instawork/llm-proxy/internal/providers	0.006s
```

### Build Verification ✅

Successfully built the complete proxy:
```
make build
✓ Build completed: ./bin/llm-proxy
```

## Verification of Fix: Before vs After

### Before Fix (Old Code)

```go
// Only accumulated text content
if content, ok := delta["content"].(string); ok {
    contentBuilder.WriteString(content)
}
// Tool_calls were completely ignored
```

**Result**: When `max_tokens > 4096`, tool_calls were silently dropped

### After Fix (New Code)

```go
// Accumulate text content
if content, ok := delta["content"].(string); ok {
    contentBuilder.WriteString(content)
}

// NEW: Accumulate tool_calls
if toolCallsArray, ok := delta["tool_calls"].([]interface{}); ok {
    for _, tc := range toolCallsArray {
        // Properly track and accumulate each tool call
        ...
    }
}

// Convert accumulated tool_calls to tool_use blocks
for _, idx := range sortedIndices {
    toolUseBlock := p.convertToolCallToToolUse(toolCallMap)
    if toolUseBlock != nil {
        claudeResp.Content = append(claudeResp.Content, *toolUseBlock)
    }
}
```

**Result**: Tool_calls are now properly preserved and returned to the client

## Scenarios Now Working

| Scenario | Before | After |
|----------|--------|-------|
| Streaming with tool_calls | ✅ Works | ✅ Still works |
| Non-streaming, max_tokens ≤ 4096 with tool_calls | ✅ Works | ✅ Still works |
| **Non-streaming, max_tokens > 4096 with tool_calls** | ❌ **BROKEN** | ✅ **FIXED** |
| Tool use ID normalization | ✅ Works | ✅ Still works |

## Recommended Actions

1. ✅ **COMPLETED**: Test Fireworks API directly - confirmed backend is clean
2. ✅ **COMPLETED**: Fix proxy Issue 1 (tool_calls loss in forced-streaming)
3. ✅ **COMPLETED**: Implement comprehensive unit tests
4. ⏳ **Future**: Monitor sglang issue #16371 for any backend improvements
5. ⏳ **Optional**: Switch to DeepSeek V3 as default if further improvements desired

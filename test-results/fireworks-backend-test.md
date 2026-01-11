# Fireworks Backend Test Results

**Date**: 2026-01-11
**Model**: accounts/fireworks/models/glm-4p7
**Test**: Multiple tool_calls in streaming mode

## Summary

✅ **BACKEND IS CLEAN** - Fireworks API returns properly formatted tool_calls

## Test Scenario

Requested the model to use two tools sequentially:
1. `search` - Search for "Python programming"
2. `read_file` - Read "results.txt"

## Backend Response Analysis

### Tool_Calls Format

The backend returned TWO separate tool_calls in proper OpenAI format:

```json
// First tool_call (index 0)
{
  "index": 0,
  "id": "chatcmpl-tool-82accfbe73639e5b",
  "type": "function",
  "function": {
    "name": "search",
    "arguments": "{\"query\": \"Python programming\"}"
  }
}

// Second tool_call (index 1)
{
  "index": 1,
  "id": "chatcmpl-tool-8ab41f03314a9d98",
  "type": "function",
  "function": {
    "name": "read_file",
    "arguments": "{\"path\": \"results.txt\"}"
  }
}
```

### Key Observations

✅ **Unique indices**: Each tool_call has a unique index (0, 1)
✅ **Unique IDs**: Each tool_call has a unique ID (chatcmpl-tool-xxx format)
✅ **Valid JSON arguments**: Arguments are properly formatted JSON strings
✅ **Separate chunks**: Tool_calls arrive in separate SSE events, not concatenated
✅ **Proper finish_reason**: Response ends with `"finish_reason": "tool_calls"`

### Response Structure

1. **Reasoning phase**: Model outputs `reasoning_content` (thinking process)
2. **Content phase**: Model outputs regular `content` (assistant message)
3. **Tool_calls phase**: Model outputs tool_calls in separate chunks
4. **Finish**: `finish_reason: "tool_calls"` with usage stats

### No Malformed JSON Detected

❌ **No concatenated JSON patterns** like `{...}{...}`
❌ **No missing delimiters** between tool_calls
❌ **No merged arguments** across multiple tools

## Conclusion

The backend issue described in https://github.com/sgl-project/sglang/issues/16371 is **NOT present** in this version/configuration of GLM-4.7 on Fireworks.

This means:
- Any tool_calls issues in our proxy are **proxy bugs**, not backend issues
- The proxy should be able to properly handle multiple tool_calls
- Fixing the proxy's forced-streaming path is now the priority

## Implications

Since the backend is clean, **Issue 1** in `streaming-tool-call-issue.md` (tool_calls lost in forced-streaming path) is purely a proxy implementation bug and can be fixed without backend workarounds.

## Raw Output

See `test-results/fireworks-direct-output.txt` for complete raw response.

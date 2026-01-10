package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Instawork/llm-proxy/internal/config"
	"github.com/gorilla/mux"
)

// ClaudeCodeCloud implements the Provider interface for the /cc endpoint
// It provides a unified Anthropic-compatible endpoint that routes to various backends
// (Fireworks, local vLLM, etc.) based on model configuration
type ClaudeCodeCloud struct {
	name          string
	config        *config.ClaudeCodeCloudConfig
	client        *http.Client
	thinkTagRegex *regexp.Regexp
}

// NewClaudeCodeCloud creates a new Claude Code cloud provider
func NewClaudeCodeCloud(cfg *config.ClaudeCodeCloudConfig) *ClaudeCodeCloud {
	client := &http.Client{
		Timeout: 300 * time.Second, // Longer timeout for cloud APIs
	}

	return &ClaudeCodeCloud{
		name:          "cc",
		config:        cfg,
		client:        client,
		thinkTagRegex: regexp.MustCompile(`<think>(.*?)</think>`),
	}
}

// GetName returns the provider name
func (p *ClaudeCodeCloud) GetName() string {
	return p.name
}

// IsStreamingRequest checks if the request is for streaming
func (p *ClaudeCodeCloud) IsStreamingRequest(req *http.Request) bool {
	var requestBody map[string]interface{}
	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			json.Unmarshal(bodyBytes, &requestBody)
			if stream, ok := requestBody["stream"].(bool); ok {
				return stream
			}
		}
	}
	return false
}

// getModelConfig looks up the model configuration by name or alias
func (p *ClaudeCodeCloud) getModelConfig(modelName string) (*config.CCCloudModelConfig, string) {
	if p.config == nil || p.config.Models == nil {
		return nil, ""
	}

	// Direct lookup
	if cfg, ok := p.config.Models[modelName]; ok {
		return &cfg, modelName
	}

	// Search by alias
	for name, cfg := range p.config.Models {
		for _, alias := range cfg.Aliases {
			if alias == modelName {
				return &cfg, name
			}
		}
	}

	return nil, ""
}

// getBackendURL returns the URL and API key for the backend
func (p *ClaudeCodeCloud) getBackendURL(modelCfg *config.CCCloudModelConfig) (string, string, error) {
	switch modelCfg.Backend {
	case "fireworks":
		baseURL := os.Getenv("FIREWORKS_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.fireworks.ai/inference/v1"
		}
		apiKey := os.Getenv("FIREWORKS_API_KEY")
		if apiKey == "" {
			return "", "", fmt.Errorf("FIREWORKS_API_KEY not set")
		}
		return baseURL + "/chat/completions", apiKey, nil

	case "local":
		// For local vLLM, use the configured endpoints with failover
		if len(modelCfg.Endpoints) == 0 {
			return "", "", fmt.Errorf("no endpoints configured for local backend")
		}
		// Simple selection: use first endpoint for now
		// TODO: Add proper failover like local_llm.go
		endpoint := modelCfg.Endpoints[0]
		url := os.ExpandEnv(endpoint.URL)
		apiKey := os.ExpandEnv(endpoint.APIKey)
		return url + "/chat/completions", apiKey, nil

	case "openai":
		baseURL := os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return "", "", fmt.Errorf("OPENAI_API_KEY not set")
		}
		return baseURL + "/chat/completions", apiKey, nil

	default:
		return "", "", fmt.Errorf("unknown backend: %s", modelCfg.Backend)
	}
}

// convertAnthropicToOpenAI converts Anthropic request format to OpenAI format
// forceStream is used when the backend requires streaming (e.g., Fireworks with max_tokens > 4096)
func (p *ClaudeCodeCloud) convertAnthropicToOpenAI(claudeReq *ClaudeCodeRequest, targetModel string, forceStream bool) map[string]interface{} {
	stream := claudeReq.Stream || forceStream
	openaiReq := map[string]interface{}{
		"model":  targetModel,
		"stream": stream,
	}

	// Only add non-zero values to avoid parameter validation errors
	if claudeReq.MaxTokens > 0 {
		openaiReq["max_tokens"] = claudeReq.MaxTokens
	}
	if claudeReq.Temperature > 0 {
		openaiReq["temperature"] = claudeReq.Temperature
	}
	if claudeReq.TopP > 0 {
		openaiReq["top_p"] = claudeReq.TopP
	}

	// Convert stop sequences
	if len(claudeReq.StopSequences) > 0 {
		openaiReq["stop"] = claudeReq.StopSequences
	}

	// Convert Claude tools to OpenAI format
	if claudeReq.Tools != nil {
		openaiTools := p.convertToolsToOpenAI(claudeReq.Tools)
		if len(openaiTools) > 0 {
			openaiReq["tools"] = openaiTools
		}
	}

	var messages []map[string]interface{}

	// Convert system message
	systemText := p.extractSystemText(claudeReq.System)
	if systemText != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": systemText,
		})
	}

	// Convert user/assistant messages
	messages = append(messages, p.convertMessagesToOpenAI(claudeReq.Messages)...)

	openaiReq["messages"] = messages
	return openaiReq
}

// convertToolsToOpenAI converts Claude tool definitions to OpenAI format
func (p *ClaudeCodeCloud) convertToolsToOpenAI(tools interface{}) []map[string]interface{} {
	var openaiTools []map[string]interface{}

	toolsArray, ok := tools.([]interface{})
	if !ok {
		return openaiTools
	}

	for _, tool := range toolsArray {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := toolMap["name"].(string)
		description, _ := toolMap["description"].(string)
		inputSchema := toolMap["input_schema"]

		if name != "" {
			openaiTool := map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        name,
					"description": description,
					"parameters":  inputSchema,
				},
			}
			openaiTools = append(openaiTools, openaiTool)
		}
	}

	return openaiTools
}

// convertMessagesToOpenAI converts Claude messages to OpenAI format
func (p *ClaudeCodeCloud) convertMessagesToOpenAI(messages []ClaudeCodeMessage) []map[string]interface{} {
	var openaiMessages []map[string]interface{}

	for _, msg := range messages {
		converted := p.convertSingleMessageToOpenAI(msg)
		openaiMessages = append(openaiMessages, converted...)
	}

	return openaiMessages
}

// convertSingleMessageToOpenAI converts a single Claude message to OpenAI format
func (p *ClaudeCodeCloud) convertSingleMessageToOpenAI(msg ClaudeCodeMessage) []map[string]interface{} {
	var result []map[string]interface{}

	// Handle string content directly
	if contentStr, ok := msg.Content.(string); ok {
		result = append(result, map[string]interface{}{
			"role":    msg.Role,
			"content": contentStr,
		})
		return result
	}

	// Handle array of content blocks
	contentArray, ok := msg.Content.([]interface{})
	if !ok {
		result = append(result, map[string]interface{}{
			"role":    msg.Role,
			"content": p.extractContentText(msg.Content),
		})
		return result
	}

	// Process content blocks
	var textParts []string
	var toolCalls []map[string]interface{}
	var toolResults []map[string]interface{}

	for _, block := range contentArray {
		blockMap, ok := block.(map[string]interface{})
		if !ok {
			continue
		}

		blockType, _ := blockMap["type"].(string)

		switch blockType {
		case "text":
			if text, ok := blockMap["text"].(string); ok {
				textParts = append(textParts, text)
			}

		case "tool_use":
			toolID, _ := blockMap["id"].(string)
			toolName, _ := blockMap["name"].(string)
			toolInput := blockMap["input"]

			inputJSON, _ := json.Marshal(toolInput)

			toolCalls = append(toolCalls, map[string]interface{}{
				"id":   toolID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      toolName,
					"arguments": string(inputJSON),
				},
			})

		case "tool_result":
			toolUseID, _ := blockMap["tool_use_id"].(string)
			content := p.extractToolResultContent(blockMap["content"])

			toolResults = append(toolResults, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": toolUseID,
				"content":      content,
			})
		}
	}

	// Build the message(s)
	if msg.Role == "assistant" {
		assistantMsg := map[string]interface{}{
			"role": "assistant",
		}

		if len(textParts) > 0 {
			assistantMsg["content"] = strings.Join(textParts, "")
		}

		if len(toolCalls) > 0 {
			assistantMsg["tool_calls"] = toolCalls
			if len(textParts) == 0 {
				assistantMsg["content"] = nil
			}
		}

		result = append(result, assistantMsg)

	} else if msg.Role == "user" {
		if len(textParts) > 0 {
			result = append(result, map[string]interface{}{
				"role":    "user",
				"content": strings.Join(textParts, ""),
			})
		}

		result = append(result, toolResults...)
	}

	return result
}

// extractToolResultContent extracts content from a tool_result block
func (p *ClaudeCodeCloud) extractToolResultContent(content interface{}) string {
	if content == nil {
		return ""
	}

	if str, ok := content.(string); ok {
		return str
	}

	if arr, ok := content.([]interface{}); ok {
		var parts []string
		for _, item := range arr {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemMap["type"] == "text" {
					if text, ok := itemMap["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "")
	}

	return fmt.Sprintf("%v", content)
}

// extractContentText extracts text from Anthropic content
func (p *ClaudeCodeCloud) extractContentText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var textBuilder strings.Builder
		for _, block := range v {
			if blockMap, ok := block.(map[string]interface{}); ok {
				if blockType, ok := blockMap["type"].(string); ok && blockType == "text" {
					if text, ok := blockMap["text"].(string); ok {
						textBuilder.WriteString(text)
					}
				}
			}
		}
		return textBuilder.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// extractSystemText extracts text from system field
func (p *ClaudeCodeCloud) extractSystemText(system interface{}) string {
	switch v := system.(type) {
	case string:
		return v
	case []interface{}:
		var textBuilder strings.Builder
		for _, block := range v {
			if blockMap, ok := block.(map[string]interface{}); ok {
				if blockType, ok := blockMap["type"].(string); ok && blockType == "text" {
					if text, ok := blockMap["text"].(string); ok {
						textBuilder.WriteString(text)
						textBuilder.WriteString("\n")
					}
				}
			}
		}
		return strings.TrimSpace(textBuilder.String())
	default:
		if system != nil {
			return fmt.Sprintf("%v", system)
		}
		return ""
	}
}

// convertOpenAIToAnthropic converts OpenAI response format to Anthropic/Claude format
func (p *ClaudeCodeCloud) convertOpenAIToAnthropic(openaiResp map[string]interface{}, requestedModel string) *ClaudeResponse {
	claudeResp := &ClaudeResponse{
		Type:    "message",
		Role:    "assistant",
		Model:   requestedModel, // Return the model the user requested (hc/xxx)
		Content: []ClaudeContentBlock{},
	}

	if id, ok := openaiResp["id"].(string); ok {
		claudeResp.ID = id
	}

	// Extract content from choices
	if choices, ok := openaiResp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				// Process text content with think tags
				if content, ok := message["content"].(string); ok && content != "" {
					contentBlocks := p.parseThinkTagsToBlocks(content)
					claudeResp.Content = append(claudeResp.Content, contentBlocks...)
				}

				// Process tool_calls -> tool_use blocks
				if toolCalls, ok := message["tool_calls"].([]interface{}); ok {
					for _, tc := range toolCalls {
						if toolCall, ok := tc.(map[string]interface{}); ok {
							toolUseBlock := p.convertToolCallToToolUse(toolCall)
							if toolUseBlock != nil {
								claudeResp.Content = append(claudeResp.Content, *toolUseBlock)
							}
						}
					}
				}
			}

			// Extract stop reason
			if finishReason, ok := choice["finish_reason"].(string); ok {
				switch finishReason {
				case "stop":
					claudeResp.StopReason = "end_turn"
				case "length":
					claudeResp.StopReason = "max_tokens"
				case "tool_calls":
					claudeResp.StopReason = "tool_use"
				case "content_filter":
					claudeResp.StopReason = "stop_sequence"
				default:
					claudeResp.StopReason = "end_turn"
				}
			}
		}
	}

	// Extract usage
	if usage, ok := openaiResp["usage"].(map[string]interface{}); ok {
		if inputTokens, ok := usage["prompt_tokens"].(float64); ok {
			claudeResp.Usage.InputTokens = int(inputTokens)
		}
		if outputTokens, ok := usage["completion_tokens"].(float64); ok {
			claudeResp.Usage.OutputTokens = int(outputTokens)
		}
	}

	return claudeResp
}

// parseThinkTagsToBlocks parses content with <think> tags into separate content blocks
func (p *ClaudeCodeCloud) parseThinkTagsToBlocks(content string) []ClaudeContentBlock {
	var blocks []ClaudeContentBlock

	thinkStartIdx := strings.Index(content, "<think>")
	thinkEndIdx := strings.Index(content, "</think>")

	if thinkStartIdx != -1 && thinkEndIdx != -1 && thinkEndIdx > thinkStartIdx {
		thinkingContent := content[thinkStartIdx+7 : thinkEndIdx]
		thinkingContent = strings.TrimSpace(thinkingContent)

		if thinkingContent != "" {
			blocks = append(blocks, ClaudeContentBlock{
				Type:     "thinking",
				Thinking: thinkingContent,
			})
		}

		remainingText := strings.TrimSpace(content[thinkEndIdx+8:])
		if remainingText != "" {
			blocks = append(blocks, ClaudeContentBlock{
				Type: "text",
				Text: remainingText,
			})
		}
	} else if thinkEndIdx != -1 && thinkStartIdx == -1 {
		// Has </think> but no <think> - some models emit this
		thinkingContent := strings.TrimSpace(content[:thinkEndIdx])
		remainingText := strings.TrimSpace(content[thinkEndIdx+8:])

		if thinkingContent != "" {
			blocks = append(blocks, ClaudeContentBlock{
				Type:     "thinking",
				Thinking: thinkingContent,
			})
		}
		if remainingText != "" {
			blocks = append(blocks, ClaudeContentBlock{
				Type: "text",
				Text: remainingText,
			})
		}
	} else {
		// No think tags, just text
		if content != "" {
			blocks = append(blocks, ClaudeContentBlock{
				Type: "text",
				Text: content,
			})
		}
	}

	return blocks
}

// convertToolCallToToolUse converts an OpenAI tool_call to a Claude tool_use block
func (p *ClaudeCodeCloud) convertToolCallToToolUse(toolCall map[string]interface{}) *ClaudeContentBlock {
	id, _ := toolCall["id"].(string)
	function, ok := toolCall["function"].(map[string]interface{})
	if !ok {
		return nil
	}

	name, _ := function["name"].(string)
	argumentsStr, _ := function["arguments"].(string)

	var input map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsStr), &input); err != nil {
		input = map[string]interface{}{}
	}

	return &ClaudeContentBlock{
		Type:  "tool_use",
		ID:    id,
		Name:  name,
		Input: input,
	}
}

// createAnthropicError creates an error in Anthropic format
func (p *ClaudeCodeCloud) createAnthropicError(errorType, message string, statusCode int) ([]byte, int) {
	anthropicErr := AnthropicError{
		Type: "error",
		Error: AnthropicErrorDetail{
			Type:    errorType,
			Message: message,
		},
	}

	errorBytes, _ := json.Marshal(anthropicErr)
	return errorBytes, statusCode
}

// Proxy returns the HTTP handler for this provider
func (p *ClaudeCodeCloud) Proxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Check for count_tokens endpoint
		if strings.HasSuffix(req.URL.Path, "/count_tokens") {
			p.handleCountTokens(w, req)
			return
		}

		// Parse the Anthropic request
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			errorBytes, statusCode := p.createAnthropicError("invalid_request_error", "Failed to read request body", http.StatusBadRequest)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}

		var claudeReq ClaudeCodeRequest
		if err := json.Unmarshal(bodyBytes, &claudeReq); err != nil {
			errorBytes, statusCode := p.createAnthropicError("invalid_request_error", "Invalid JSON in request body", http.StatusBadRequest)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}

		// Look up model configuration
		modelCfg, modelName := p.getModelConfig(claudeReq.Model)
		if modelCfg == nil {
			// List available models in error message
			var availableModels []string
			for name := range p.config.Models {
				availableModels = append(availableModels, name)
			}
			errorBytes, statusCode := p.createAnthropicError("invalid_request_error",
				fmt.Sprintf("Model '%s' not configured. Available models: %s", claudeReq.Model, strings.Join(availableModels, ", ")),
				http.StatusBadRequest)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}

		// Get backend URL and API key
		backendURL, apiKey, err := p.getBackendURL(modelCfg)
		if err != nil {
			errorBytes, statusCode := p.createAnthropicError("service_unavailable", err.Error(), http.StatusServiceUnavailable)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}

		// Fireworks API requires streaming for max_tokens > 4096
		forceStream := false
		if modelCfg.Backend == "fireworks" && claudeReq.MaxTokens > 4096 {
			forceStream = true
			log.Printf("Claude Code Cloud: forcing streaming for Fireworks (max_tokens=%d > 4096)", claudeReq.MaxTokens)
		}

		log.Printf("Claude Code Cloud: routing %s -> %s (backend: %s, model: %s)", claudeReq.Model, modelName, modelCfg.Backend, modelCfg.Model)

		// Convert Anthropic request to OpenAI format
		openaiReq := p.convertAnthropicToOpenAI(&claudeReq, modelCfg.Model, forceStream)

		// Create new request body
		openaiReqBytes, err := json.Marshal(openaiReq)
		if err != nil {
			errorBytes, statusCode := p.createAnthropicError("internal_server_error", "Failed to convert request format", http.StatusInternalServerError)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}

		// Handle streaming vs non-streaming
		if claudeReq.Stream {
			// Client wants streaming - give them streaming
			p.handleStreamingRequest(w, backendURL, apiKey, openaiReqBytes, claudeReq.Model)
		} else if forceStream {
			// Client wants non-streaming but backend requires streaming
			// Collect streaming response and return as non-streaming JSON
			p.handleForcedStreamingRequest(w, backendURL, apiKey, openaiReqBytes, claudeReq.Model)
		} else {
			// Normal non-streaming request
			p.handleNonStreamingRequest(w, backendURL, apiKey, openaiReqBytes, claudeReq.Model)
		}
	})
}

// handleNonStreamingRequest handles non-streaming requests
func (p *ClaudeCodeCloud) handleNonStreamingRequest(w http.ResponseWriter, backendURL, apiKey string, requestBody []byte, requestedModel string) {
	// Create HTTP request to backend
	req, err := http.NewRequest("POST", backendURL, bytes.NewBuffer(requestBody))
	if err != nil {
		errorBytes, statusCode := p.createAnthropicError("internal_server_error", "Failed to create backend request", http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// Make the request
	resp, err := p.client.Do(req)
	if err != nil {
		errorBytes, statusCode := p.createAnthropicError("service_unavailable", fmt.Sprintf("Backend request failed: %v", err), http.StatusServiceUnavailable)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		errorBytes, statusCode := p.createAnthropicError("internal_server_error", "Failed to read backend response", http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}

	// Parse the OpenAI response
	var openaiResp map[string]interface{}
	if err := json.Unmarshal(respBody, &openaiResp); err != nil {
		errorBytes, statusCode := p.createAnthropicError("internal_server_error", "Backend returned invalid response", http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}

	// Check for errors in the OpenAI response
	if errorObj, ok := openaiResp["error"].(map[string]interface{}); ok {
		errorMsg := "Backend error occurred"
		if msg, ok := errorObj["message"].(string); ok {
			errorMsg = msg
		}
		errorBytes, statusCode := p.createAnthropicError("service_unavailable", errorMsg, resp.StatusCode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}

	// Check for valid response
	if _, hasChoices := openaiResp["choices"]; !hasChoices {
		errorMsg := "Backend returned invalid or empty response"
		if len(respBody) < 500 && len(respBody) > 0 {
			errorMsg = fmt.Sprintf("%s (raw: %s)", errorMsg, string(respBody))
		}
		errorBytes, statusCode := p.createAnthropicError("service_unavailable", errorMsg, http.StatusBadGateway)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}

	// Convert OpenAI response to Anthropic format
	anthropicResp := p.convertOpenAIToAnthropic(openaiResp, requestedModel)

	// Return Anthropic response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(anthropicResp)
}

// handleForcedStreamingRequest handles requests where the backend requires streaming
// but the client wants a non-streaming response. It collects the streaming response
// and returns it as a single JSON response.
func (p *ClaudeCodeCloud) handleForcedStreamingRequest(w http.ResponseWriter, backendURL, apiKey string, requestBody []byte, requestedModel string) {
	// Create HTTP request to backend
	req, err := http.NewRequest("POST", backendURL, bytes.NewBuffer(requestBody))
	if err != nil {
		errorBytes, statusCode := p.createAnthropicError("internal_server_error", "Failed to create backend request", http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "text/event-stream")

	// Make the request
	resp, err := p.client.Do(req)
	if err != nil {
		errorBytes, statusCode := p.createAnthropicError("service_unavailable", fmt.Sprintf("Backend request failed: %v", err), http.StatusServiceUnavailable)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}
	defer resp.Body.Close()

	// Collect streaming response content
	var contentBuilder strings.Builder
	var inputTokens, outputTokens int
	var finishReason string

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			if data == "[DONE]" {
				break
			}

			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(data), &chunk); err == nil {
				// Extract content from delta
				if choices, ok := chunk["choices"].([]interface{}); ok && len(choices) > 0 {
					if choice, ok := choices[0].(map[string]interface{}); ok {
						if delta, ok := choice["delta"].(map[string]interface{}); ok {
							if content, ok := delta["content"].(string); ok {
								contentBuilder.WriteString(content)
							}
						}
						if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
							finishReason = fr
						}
					}
				}

				// Extract usage if present
				if usage, ok := chunk["usage"].(map[string]interface{}); ok {
					if pt, ok := usage["prompt_tokens"].(float64); ok {
						inputTokens = int(pt)
					}
					if ct, ok := usage["completion_tokens"].(float64); ok {
						outputTokens = int(ct)
					}
				}
			}
		}
	}

	// Build the complete content
	fullContent := contentBuilder.String()

	// Convert to Anthropic response format
	claudeResp := &ClaudeResponse{
		ID:      "msg_" + generateID(),
		Type:    "message",
		Role:    "assistant",
		Model:   requestedModel,
		Content: p.parseThinkTagsToBlocks(fullContent),
	}

	// Set stop reason
	switch finishReason {
	case "stop":
		claudeResp.StopReason = "end_turn"
	case "length":
		claudeResp.StopReason = "max_tokens"
	case "tool_calls":
		claudeResp.StopReason = "tool_use"
	default:
		claudeResp.StopReason = "end_turn"
	}

	claudeResp.Usage.InputTokens = inputTokens
	claudeResp.Usage.OutputTokens = outputTokens

	// Return as JSON response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(claudeResp)
}

// handleStreamingRequest handles streaming requests with support for:
// - Text content (content field)
// - Thinking/reasoning content (reasoning_content field from Fireworks GLM)
// - Tool calls (tool_calls field)
func (p *ClaudeCodeCloud) handleStreamingRequest(w http.ResponseWriter, backendURL, apiKey string, requestBody []byte, requestedModel string) {
	// Set up SSE headers for Anthropic format
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create HTTP request to backend
	req, err := http.NewRequest("POST", backendURL, bytes.NewBuffer(requestBody))
	if err != nil {
		p.writeAnthropicStreamError(w, "Failed to create backend request")
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "text/event-stream")

	// Make the request
	resp, err := p.client.Do(req)
	if err != nil {
		p.writeAnthropicStreamError(w, fmt.Sprintf("Backend request failed: %v", err))
		return
	}
	defer resp.Body.Close()

	// Send initial Anthropic streaming events
	msgID := "msg_" + generateID()
	p.writeAnthropicStreamEvent(w, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":      msgID,
			"type":    "message",
			"role":    "assistant",
			"content": []interface{}{},
			"model":   requestedModel,
		},
	})

	// State tracking
	var contentBuffer strings.Builder
	var thinkingBuffer strings.Builder
	inThinkingBlock := false
	thinkingBlockStarted := false
	textBlockStarted := false
	currentBlockIndex := 0
	finishReason := "end_turn"

	// Tool call state tracking
	type toolCallState struct {
		id        string
		name      string
		arguments strings.Builder
		started   bool
		index     int // block index in Anthropic response
	}
	toolCalls := make(map[int]*toolCallState) // keyed by OpenAI tool_calls index

	flushContent := func() {
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	// Helper to close current block and start text block if needed
	closeThinkingStartText := func() {
		if thinkingBlockStarted && inThinkingBlock {
			p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
				"type":  "content_block_stop",
				"index": currentBlockIndex,
			})
			inThinkingBlock = false
			currentBlockIndex++
		}
	}

	// Process streaming response from backend
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			if data == "[DONE]" {
				// Close any open text block
				if textBlockStarted {
					p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
						"type":  "content_block_stop",
						"index": currentBlockIndex,
					})
				} else if thinkingBlockStarted && inThinkingBlock {
					p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
						"type":  "content_block_stop",
						"index": currentBlockIndex,
					})
				}

				// Close any open tool call blocks
				for _, tc := range toolCalls {
					if tc.started {
						p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
							"type":  "content_block_stop",
							"index": tc.index,
						})
					}
				}

				// Send message_delta with stop_reason
				p.writeAnthropicStreamEvent(w, "message_delta", map[string]interface{}{
					"type": "message_delta",
					"delta": map[string]interface{}{
						"stop_reason":   finishReason,
						"stop_sequence": nil,
					},
					"usage": map[string]interface{}{
						"output_tokens": 0,
					},
				})

				p.writeAnthropicStreamEvent(w, "message_stop", map[string]interface{}{
					"type": "message_stop",
				})
				flushContent()
				break
			}

			// Parse OpenAI streaming chunk
			var openaiChunk map[string]interface{}
			if err := json.Unmarshal([]byte(data), &openaiChunk); err != nil {
				continue
			}

			choices, ok := openaiChunk["choices"].([]interface{})
			if !ok || len(choices) == 0 {
				continue
			}

			choice, ok := choices[0].(map[string]interface{})
			if !ok {
				continue
			}

			// Check finish_reason
			if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
				switch fr {
				case "stop":
					finishReason = "end_turn"
				case "length":
					finishReason = "max_tokens"
				case "tool_calls":
					finishReason = "tool_use"
				default:
					finishReason = "end_turn"
				}
			}

			delta, ok := choice["delta"].(map[string]interface{})
			if !ok {
				continue
			}

			// Handle reasoning_content (Fireworks GLM thinking)
			if reasoningContent, ok := delta["reasoning_content"].(string); ok && reasoningContent != "" {
				if !thinkingBlockStarted {
					thinkingBlockStarted = true
					inThinkingBlock = true
					p.writeAnthropicStreamEvent(w, "content_block_start", map[string]interface{}{
						"type":  "content_block_start",
						"index": currentBlockIndex,
						"content_block": map[string]interface{}{
							"type":     "thinking",
							"thinking": "",
						},
					})
				}
				if inThinkingBlock {
					p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": currentBlockIndex,
						"delta": map[string]interface{}{
							"type":     "thinking_delta",
							"thinking": reasoningContent,
						},
					})
				}
				flushContent()
				continue
			}

			// Handle text content
			if content, ok := delta["content"].(string); ok && content != "" {
				// Close thinking block if open
				closeThinkingStartText()

				contentBuffer.WriteString(content)
				fullContent := contentBuffer.String()

				// Check for <think> tag at start (for models that use tags instead of reasoning_content)
				if !thinkingBlockStarted && !textBlockStarted {
					if strings.HasPrefix(fullContent, "<think>") {
						inThinkingBlock = true
						thinkingBlockStarted = true
						p.writeAnthropicStreamEvent(w, "content_block_start", map[string]interface{}{
							"type":  "content_block_start",
							"index": currentBlockIndex,
							"content_block": map[string]interface{}{
								"type":     "thinking",
								"thinking": "",
							},
						})
						contentBuffer.Reset()
						afterTag := strings.TrimPrefix(fullContent, "<think>")
						if afterTag != "" {
							thinkingBuffer.WriteString(afterTag)
						}
						flushContent()
						continue
					} else if len(fullContent) < 7 {
						// Buffer until we know if it's <think> or not
						continue
					} else {
						// Start text block
						textBlockStarted = true
						p.writeAnthropicStreamEvent(w, "content_block_start", map[string]interface{}{
							"type":  "content_block_start",
							"index": currentBlockIndex,
							"content_block": map[string]interface{}{
								"type": "text",
								"text": "",
							},
						})
						p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
							"type":  "content_block_delta",
							"index": currentBlockIndex,
							"delta": map[string]interface{}{
								"type": "text_delta",
								"text": fullContent,
							},
						})
						contentBuffer.Reset()
						flushContent()
						continue
					}
				}

				// Handle in-thinking content with </think> check
				if inThinkingBlock {
					thinkingBuffer.WriteString(content)
					fullThinking := thinkingBuffer.String()

					if idx := strings.Index(fullThinking, "</think>"); idx != -1 {
						// Emit thinking content before tag
						if idx > 0 {
							p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
								"type":  "content_block_delta",
								"index": currentBlockIndex,
								"delta": map[string]interface{}{
									"type":     "thinking_delta",
									"thinking": fullThinking[:idx],
								},
							})
						}
						// Close thinking block
						p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
							"type":  "content_block_stop",
							"index": currentBlockIndex,
						})
						inThinkingBlock = false
						currentBlockIndex++
						thinkingBuffer.Reset()

						// Start text block with remaining content
						textContent := strings.TrimSpace(fullThinking[idx+8:])
						if textContent != "" {
							textBlockStarted = true
							p.writeAnthropicStreamEvent(w, "content_block_start", map[string]interface{}{
								"type":  "content_block_start",
								"index": currentBlockIndex,
								"content_block": map[string]interface{}{
									"type": "text",
									"text": "",
								},
							})
							p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
								"type":  "content_block_delta",
								"index": currentBlockIndex,
								"delta": map[string]interface{}{
									"type": "text_delta",
									"text": textContent,
								},
							})
						}
					} else {
						// Still in thinking, emit delta
						p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
							"type":  "content_block_delta",
							"index": currentBlockIndex,
							"delta": map[string]interface{}{
								"type":     "thinking_delta",
								"thinking": content,
							},
						})
						// Keep last 8 chars in buffer to detect </think>
						if thinkingBuffer.Len() > 16 {
							thinkingBuffer.Reset()
							thinkingBuffer.WriteString(fullThinking[len(fullThinking)-8:])
						}
					}
					contentBuffer.Reset()
					flushContent()
					continue
				}

				// Regular text content
				if textBlockStarted {
					p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": currentBlockIndex,
						"delta": map[string]interface{}{
							"type": "text_delta",
							"text": content,
						},
					})
					contentBuffer.Reset()
				} else {
					// Start text block
					textBlockStarted = true
					p.writeAnthropicStreamEvent(w, "content_block_start", map[string]interface{}{
						"type":  "content_block_start",
						"index": currentBlockIndex,
						"content_block": map[string]interface{}{
							"type": "text",
							"text": "",
						},
					})
					p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
						"type":  "content_block_delta",
						"index": currentBlockIndex,
						"delta": map[string]interface{}{
							"type": "text_delta",
							"text": content,
						},
					})
					contentBuffer.Reset()
				}
				flushContent()
				continue
			}

			// Handle tool_calls
			if toolCallsArray, ok := delta["tool_calls"].([]interface{}); ok {
				for _, tc := range toolCallsArray {
					toolCall, ok := tc.(map[string]interface{})
					if !ok {
						continue
					}

					// Get tool call index
					tcIndex := 0
					if idx, ok := toolCall["index"].(float64); ok {
						tcIndex = int(idx)
					}

					// Get or create tool call state
					tcState, exists := toolCalls[tcIndex]
					if !exists {
						tcState = &toolCallState{}
						toolCalls[tcIndex] = tcState
					}

					// Get tool call ID
					if id, ok := toolCall["id"].(string); ok && id != "" {
						tcState.id = id
					}

					// Get function details
					if function, ok := toolCall["function"].(map[string]interface{}); ok {
						if name, ok := function["name"].(string); ok && name != "" {
							tcState.name = name
						}
						if arguments, ok := function["arguments"].(string); ok {
							tcState.arguments.WriteString(arguments)
						}
					}

					// Start the tool_use block if not started
					if !tcState.started && tcState.name != "" {
						// Close any open text/thinking block first
						if textBlockStarted {
							p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
								"type":  "content_block_stop",
								"index": currentBlockIndex,
							})
							textBlockStarted = false
							currentBlockIndex++
						} else if thinkingBlockStarted && inThinkingBlock {
							p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
								"type":  "content_block_stop",
								"index": currentBlockIndex,
							})
							inThinkingBlock = false
							currentBlockIndex++
						}

						tcState.started = true
						tcState.index = currentBlockIndex

						// Generate a tool use ID if not provided
						toolUseID := tcState.id
						if toolUseID == "" {
							toolUseID = "toolu_" + generateID()
						}

						p.writeAnthropicStreamEvent(w, "content_block_start", map[string]interface{}{
							"type":  "content_block_start",
							"index": currentBlockIndex,
							"content_block": map[string]interface{}{
								"type":  "tool_use",
								"id":    toolUseID,
								"name":  tcState.name,
								"input": map[string]interface{}{},
							},
						})
						currentBlockIndex++
					}

					// Send input_json_delta for arguments
					if tcState.started {
						if function, ok := toolCall["function"].(map[string]interface{}); ok {
							if arguments, ok := function["arguments"].(string); ok && arguments != "" {
								p.writeAnthropicStreamEvent(w, "content_block_delta", map[string]interface{}{
									"type":  "content_block_delta",
									"index": tcState.index,
									"delta": map[string]interface{}{
										"type":         "input_json_delta",
										"partial_json": arguments,
									},
								})
							}
						}
					}
				}
				flushContent()
			}
		}
	}
}

// writeAnthropicStreamEvent writes an event in Anthropic SSE format
func (p *ClaudeCodeCloud) writeAnthropicStreamEvent(w http.ResponseWriter, event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData))
}

// writeAnthropicStreamError writes an error in Anthropic streaming format
func (p *ClaudeCodeCloud) writeAnthropicStreamError(w http.ResponseWriter, message string) {
	p.writeAnthropicStreamEvent(w, "error", map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "service_unavailable",
			"message": message,
		},
	})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// handleCountTokens handles the token counting endpoint
func (p *ClaudeCodeCloud) handleCountTokens(w http.ResponseWriter, req *http.Request) {
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		errorBytes, statusCode := p.createAnthropicError("invalid_request_error", "Failed to read request body", http.StatusBadRequest)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}

	var claudeReq ClaudeCodeRequest
	if err := json.Unmarshal(bodyBytes, &claudeReq); err != nil {
		errorBytes, statusCode := p.createAnthropicError("invalid_request_error", "Invalid JSON in request body", http.StatusBadRequest)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}

	// Estimate tokens based on character count (~4 chars per token)
	totalChars := 0
	for _, msg := range claudeReq.Messages {
		totalChars += len(p.extractContentText(msg.Content))
	}
	systemText := p.extractSystemText(claudeReq.System)
	totalChars += len(systemText)

	estimatedTokens := (totalChars + 3) / 4

	response := map[string]interface{}{
		"input_tokens": estimatedTokens,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// GetHealthStatus returns health status of the provider
func (p *ClaudeCodeCloud) GetHealthStatus() map[string]interface{} {
	var availableModels []string
	var backends []string

	if p.config != nil && p.config.Models != nil {
		for name, cfg := range p.config.Models {
			availableModels = append(availableModels, name)
			// Track unique backends
			found := false
			for _, b := range backends {
				if b == cfg.Backend {
					found = true
					break
				}
			}
			if !found {
				backends = append(backends, cfg.Backend)
			}
		}
	}

	return map[string]interface{}{
		"status":           "healthy",
		"endpoint":         "/cc/v1/messages",
		"available_models": availableModels,
		"backends":         backends,
	}
}

// UserIDFromRequest extracts user ID from request
func (p *ClaudeCodeCloud) UserIDFromRequest(req *http.Request) string {
	return ""
}

// RegisterExtraRoutes registers additional routes for the provider
func (p *ClaudeCodeCloud) RegisterExtraRoutes(router *mux.Router) {
	// Register count_tokens endpoint first (more specific route)
	router.HandleFunc("/cc/v1/messages/count_tokens", p.handleCountTokens).Methods("POST")
	// Register the main messages endpoint
	router.HandleFunc("/cc/v1/messages", p.Proxy().ServeHTTP).Methods("POST")

	// Also register routes for double-/v1 paths
	// Claude Code appends /v1/messages to ANTHROPIC_BASE_URL which already ends in /v1
	// So we need to handle /cc/v1/v1/messages as well
	router.HandleFunc("/cc/v1/v1/messages/count_tokens", p.handleCountTokens).Methods("POST")
	router.HandleFunc("/cc/v1/v1/messages", p.Proxy().ServeHTTP).Methods("POST")
}

// ValidateAPIKey validates API key (not required for this provider)
func (p *ClaudeCodeCloud) ValidateAPIKey(req *http.Request, keyStore APIKeyStore) error {
	return nil
}

// ExtractRequestModelAndMessages extracts model and messages for token estimation
func (p *ClaudeCodeCloud) ExtractRequestModelAndMessages(req *http.Request) (string, []string) {
	var requestBody ClaudeCodeRequest
	var messages []string

	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			if err := json.Unmarshal(bodyBytes, &requestBody); err == nil {
				for _, msg := range requestBody.Messages {
					content := p.extractContentText(msg.Content)
					messages = append(messages, content)
				}
				systemText := p.extractSystemText(requestBody.System)
				if systemText != "" {
					messages = append([]string{systemText}, messages...)
				}

				return requestBody.Model, messages
			}
		}
	}

	return "", messages
}

// ParseResponseMetadata extracts metadata from the response
func (p *ClaudeCodeCloud) ParseResponseMetadata(responseBody io.Reader, isStreaming bool) (*LLMResponseMetadata, error) {
	metadata := &LLMResponseMetadata{
		Provider:    p.name,
		IsStreaming: isStreaming,
	}

	if !isStreaming {
		bodyBytes, err := io.ReadAll(responseBody)
		if err != nil {
			return metadata, nil
		}

		var response AnthropicResponse
		if json.Unmarshal(bodyBytes, &response) == nil {
			metadata.InputTokens = response.Usage.InputTokens
			metadata.OutputTokens = response.Usage.OutputTokens
			metadata.TotalTokens = metadata.InputTokens + metadata.OutputTokens
			metadata.Model = response.Model
		}
	}

	return metadata, nil
}

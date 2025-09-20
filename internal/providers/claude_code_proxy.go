package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Instawork/llm-proxy/internal/config"
	"github.com/gorilla/mux"
)

// ClaudeCodeProxy implements Claude API to OpenAI API conversion and routing to local LLMs
type ClaudeCodeProxy struct {
	name           string
	config         *config.ClaudeCodeProxyConfig
	targetProvider Provider
}

// NewClaudeCodeProxy creates a new Claude Code proxy
func NewClaudeCodeProxy(name string, config *config.ClaudeCodeProxyConfig, targetProvider Provider) *ClaudeCodeProxy {
	return &ClaudeCodeProxy{
		name:           name,
		config:         config,
		targetProvider: targetProvider,
	}
}

// GetName returns the provider name
func (p *ClaudeCodeProxy) GetName() string {
	return p.name
}

// IsStreamingRequest checks if the request is for streaming (Claude format)
func (p *ClaudeCodeProxy) IsStreamingRequest(req *http.Request) bool {
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

// claudeToOpenAIFormat converts Claude API request to OpenAI format
func (p *ClaudeCodeProxy) claudeToOpenAIFormat(claudeRequest map[string]interface{}) map[string]interface{} {
	openAIRequest := make(map[string]interface{})

	// Always use the configured target model
	openAIRequest["model"] = p.config.TargetModel

	// Convert messages from Claude format to OpenAI format
	if claudeMessages, ok := claudeRequest["messages"].([]interface{}); ok {
		var openAIMessages []interface{}
		
		for _, msgInterface := range claudeMessages {
			if msg, ok := msgInterface.(map[string]interface{}); ok {
				openAIMsg := map[string]interface{}{
					"role": msg["role"],
				}

				// Handle Claude's content format (can be string or array)
				if content, exists := msg["content"]; exists {
					switch contentValue := content.(type) {
					case string:
						openAIMsg["content"] = contentValue
					case []interface{}:
						// Claude uses array of content blocks, extract text content
						var textContent strings.Builder
						for _, block := range contentValue {
							if blockMap, ok := block.(map[string]interface{}); ok {
								if blockType, ok := blockMap["type"].(string); ok && blockType == "text" {
									if text, ok := blockMap["text"].(string); ok {
										textContent.WriteString(text)
									}
								}
							}
						}
						openAIMsg["content"] = textContent.String()
					default:
						openAIMsg["content"] = fmt.Sprintf("%v", contentValue)
					}
				}

				openAIMessages = append(openAIMessages, openAIMsg)
			}
		}
		
		openAIRequest["messages"] = openAIMessages
	}

	// Handle parameter mapping (e.g., max_tokens -> max_completion_tokens)
	for claudeParam, openAIParam := range p.config.ParameterMapping {
		if value, exists := claudeRequest[claudeParam]; exists {
			openAIRequest[openAIParam] = value
		}
	}

	// Copy other compatible parameters
	compatibleParams := []string{"stream", "temperature", "top_p", "stop"}
	for _, param := range compatibleParams {
		if value, exists := claudeRequest[param]; exists {
			openAIRequest[param] = value
		}
	}

	return openAIRequest
}

// openAIToClaudeFormat converts OpenAI response to Claude format
func (p *ClaudeCodeProxy) openAIToClaudeFormat(openAIResponse map[string]interface{}) map[string]interface{} {
	claudeResponse := map[string]interface{}{
		"id":   openAIResponse["id"],
		"type": "message",
		"role": "assistant",
	}

	// Convert choices to Claude content format
	if choices, ok := openAIResponse["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := message["content"].(string); ok {
					claudeResponse["content"] = []map[string]interface{}{
						{
							"type": "text",
							"text": content,
						},
					}
				}
			}

			// Map finish reason
			finishReasonMap := map[string]string{
				"stop":           "end_turn",
				"length":         "max_tokens", 
				"content_filter": "content_filter",
				"function_call":  "tool_use",
			}
			
			if finishReason, ok := choice["finish_reason"].(string); ok {
				if claudeReason, exists := finishReasonMap[finishReason]; exists {
					claudeResponse["stop_reason"] = claudeReason
				} else {
					claudeResponse["stop_reason"] = "end_turn"
				}
			}
		}
	}

	// Convert usage information
	if usage, ok := openAIResponse["usage"].(map[string]interface{}); ok {
		claudeUsage := map[string]interface{}{}
		if promptTokens, ok := usage["prompt_tokens"]; ok {
			claudeUsage["input_tokens"] = promptTokens
		}
		if completionTokens, ok := usage["completion_tokens"]; ok {
			claudeUsage["output_tokens"] = completionTokens
		}
		claudeResponse["usage"] = claudeUsage
	}

	// Set model to mimic Claude for compatibility
	claudeResponse["model"] = "claude-3-sonnet-20240229"
	claudeResponse["stop_sequence"] = nil

	return claudeResponse
}

// convertStreamingChunk converts OpenAI streaming chunk to Claude format
func (p *ClaudeCodeProxy) convertStreamingChunk(chunk string) string {
	if !strings.HasPrefix(chunk, "data: ") {
		return chunk
	}

	data := strings.TrimPrefix(chunk, "data: ")
	if data == "[DONE]" {
		return "event: message_stop\ndata: {\"type\": \"message_stop\"}\n\n"
	}

	var openAIChunk map[string]interface{}
	if err := json.Unmarshal([]byte(data), &openAIChunk); err != nil {
		return chunk // Return original if parsing fails
	}

	// Convert OpenAI chunk to Claude format
	if choices, ok := openAIChunk["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if delta, ok := choice["delta"].(map[string]interface{}); ok {
				if content, ok := delta["content"].(string); ok && content != "" {
					claudeChunk := map[string]interface{}{
						"type":  "content_block_delta",
						"index": 0,
						"delta": map[string]interface{}{
							"type": "text_delta",
							"text": content,
						},
					}
					
					claudeData, _ := json.Marshal(claudeChunk)
					return fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", string(claudeData))
				}
			}

			if finishReason, ok := choice["finish_reason"]; ok && finishReason != nil {
				return "event: message_stop\ndata: {\"type\": \"message_stop\"}\n\n"
			}
		}
	}

	return "" // Skip chunks that don't contain content
}

// Proxy handles the Claude to OpenAI conversion and routing
func (p *ClaudeCodeProxy) Proxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Only handle /v1/messages endpoint (Claude's chat endpoint)
		if !strings.Contains(req.URL.Path, "/v1/messages") {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}

		// Read Claude request
		var claudeRequest map[string]interface{}
		if req.Body != nil {
			bodyBytes, err := io.ReadAll(req.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}

			if err := json.Unmarshal(bodyBytes, &claudeRequest); err != nil {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
				return
			}
		}

		// Convert to OpenAI format
		openAIRequest := p.claudeToOpenAIFormat(claudeRequest)
		
		// Create new request for target provider
		openAIBody, err := json.Marshal(openAIRequest)
		if err != nil {
			http.Error(w, "Failed to marshal request", http.StatusInternalServerError)
			return
		}

		// Create new HTTP request to target provider
		targetReq, err := http.NewRequest(req.Method, req.URL.String(), bytes.NewBuffer(openAIBody))
		if err != nil {
			http.Error(w, "Failed to create target request", http.StatusInternalServerError)
			return
		}

		// Copy headers but modify path for target provider
		for key, values := range req.Header {
			for _, value := range values {
				targetReq.Header.Add(key, value)
			}
		}

		// Change the URL path to route to target provider (qwen)
		targetReq.URL.Path = strings.Replace(req.URL.Path, "/cc-qwen/", "/qwen/", 1)
		// Convert Claude messages endpoint to OpenAI chat completions
		targetReq.URL.Path = strings.Replace(targetReq.URL.Path, "/v1/messages", "/v1/chat/completions", 1)
		
		targetReq.Header.Set("Content-Type", "application/json")
		targetReq.Header.Set("Content-Length", strconv.Itoa(len(openAIBody)))

		// Route to target provider
		if p.targetProvider != nil {
			// Create response writer to capture the response
			targetResp := &responseCapture{
				ResponseWriter: w,
				statusCode:     200,
				headers:        make(http.Header),
				body:           &bytes.Buffer{},
			}

			// Call target provider
			p.targetProvider.Proxy().ServeHTTP(targetResp, targetReq)

			// Check if it's streaming
			isStreaming := p.IsStreamingRequest(req)
			
			if isStreaming {
				// For streaming, we need to convert each chunk
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				
				// Send initial events for Claude format
				w.Write([]byte("event: message_start\ndata: {\"type\": \"message_start\", \"message\": {\"role\": \"assistant\", \"content\": []}}\n\n"))
				w.Write([]byte("event: content_block_start\ndata: {\"type\": \"content_block_start\", \"index\": 0, \"content_block\": {\"type\": \"text\", \"text\": \"\"}}\n\n"))
				
				// Process streaming response
				content := targetResp.body.String()
				lines := strings.Split(content, "\n")
				
				for _, line := range lines {
					if strings.HasPrefix(line, "data: ") {
						converted := p.convertStreamingChunk(line)
						if converted != "" {
							w.Write([]byte(converted))
							if f, ok := w.(http.Flusher); ok {
								f.Flush()
							}
						}
					}
				}
				
				// Send final events
				w.Write([]byte("event: content_block_stop\ndata: {\"type\": \"content_block_stop\", \"index\": 0}\n\n"))
				w.Write([]byte("event: message_stop\ndata: {\"type\": \"message_stop\"}\n\n"))
			} else {
				// For non-streaming, convert the response
				var openAIResponse map[string]interface{}
				if err := json.Unmarshal(targetResp.body.Bytes(), &openAIResponse); err != nil {
					// If parsing fails, return error in OpenAI format (as specified)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					errorResponse := map[string]interface{}{
						"error": map[string]interface{}{
							"message": "Failed to parse response from backend",
							"type":    "api_error",
							"code":    "api_error",
						},
					}
					json.NewEncoder(w).Encode(errorResponse)
					return
				}

				// Convert to Claude format
				claudeResponse := p.openAIToClaudeFormat(openAIResponse)
				
				// Copy headers and status
				for key, values := range targetResp.headers {
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(targetResp.statusCode)
				
				// Send converted response
				json.NewEncoder(w).Encode(claudeResponse)
			}
		} else {
			http.Error(w, "Target provider not available", http.StatusServiceUnavailable)
		}
	})
}

// responseCapture captures response data
type responseCapture struct {
	http.ResponseWriter
	statusCode int
	headers    http.Header
	body       *bytes.Buffer
}

func (rc *responseCapture) WriteHeader(code int) {
	rc.statusCode = code
}

func (rc *responseCapture) Write(data []byte) (int, error) {
	return rc.body.Write(data)
}

func (rc *responseCapture) Header() http.Header {
	return rc.headers
}

// GetHealthStatus returns health status
func (p *ClaudeCodeProxy) GetHealthStatus() map[string]interface{} {
	status := map[string]interface{}{
		"status":          "healthy",
		"target_provider": p.config.TargetProvider,
		"target_model":    p.config.TargetModel,
	}

	if p.targetProvider != nil {
		status["target_health"] = p.targetProvider.GetHealthStatus()
	} else {
		status["target_health"] = "unavailable"
		status["status"] = "unhealthy"
	}

	return status
}

// UserIDFromRequest extracts user ID (not applicable)
func (p *ClaudeCodeProxy) UserIDFromRequest(req *http.Request) string {
	return ""
}

// RegisterExtraRoutes registers additional routes
func (p *ClaudeCodeProxy) RegisterExtraRoutes(router *mux.Router) {
	// No extra routes needed
}

// ValidateAPIKey validates API key
func (p *ClaudeCodeProxy) ValidateAPIKey(req *http.Request, keyStore APIKeyStore) error {
	return nil
}

// ExtractRequestModelAndMessages extracts model and messages for token estimation
func (p *ClaudeCodeProxy) ExtractRequestModelAndMessages(req *http.Request) (string, []string) {
	var messages []string

	if req.Body != nil {
		var claudeRequest map[string]interface{}
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			if err := json.Unmarshal(bodyBytes, &claudeRequest); err == nil {
				// Extract messages from Claude format
				if claudeMessages, ok := claudeRequest["messages"].([]interface{}); ok {
					for _, msgInterface := range claudeMessages {
						if msg, ok := msgInterface.(map[string]interface{}); ok {
							// Handle Claude's content format
							if content, exists := msg["content"]; exists {
								switch contentValue := content.(type) {
								case string:
									messages = append(messages, contentValue)
								case []interface{}:
									var textContent strings.Builder
									for _, block := range contentValue {
										if blockMap, ok := block.(map[string]interface{}); ok {
											if blockType, ok := blockMap["type"].(string); ok && blockType == "text" {
												if text, ok := blockMap["text"].(string); ok {
													textContent.WriteString(text)
												}
											}
										}
									}
									if textContent.Len() > 0 {
										messages = append(messages, textContent.String())
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return p.config.TargetModel, messages
}

// ParseResponseMetadata parses response metadata
func (p *ClaudeCodeProxy) ParseResponseMetadata(responseBody io.Reader, isStreaming bool) (*LLMResponseMetadata, error) {
	metadata := &LLMResponseMetadata{
		Model:       p.config.TargetModel,
		Provider:    p.name,
		IsStreaming: isStreaming,
	}

	if !isStreaming {
		bodyBytes, err := io.ReadAll(responseBody)
		if err != nil {
			return metadata, nil
		}

		var response map[string]interface{}
		if json.Unmarshal(bodyBytes, &response) == nil {
			if usage, ok := response["usage"].(map[string]interface{}); ok {
				if inputTokens, ok := usage["input_tokens"].(float64); ok {
					metadata.InputTokens = int(inputTokens)
				}
				if outputTokens, ok := usage["output_tokens"].(float64); ok {
					metadata.OutputTokens = int(outputTokens)
				}
				metadata.TotalTokens = metadata.InputTokens + metadata.OutputTokens
			}
		}
	}

	return metadata, nil
}
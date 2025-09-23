package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Instawork/llm-proxy/internal/config"
	"github.com/gorilla/mux"
)

// ClaudeCodeProxy implements the Provider interface for Claude Code API compatibility
// It provides a unified /cc-local/v1/messages endpoint that routes based on model name
type ClaudeCodeProxy struct {
	name            string
	config          *config.ClaudeCodeProxyConfig
	providerManager *ProviderManager
	client          *http.Client
	thinkTagRegex   *regexp.Regexp
}

// NewClaudeCodeProxy creates a new Claude Code proxy provider with model-based routing
func NewClaudeCodeProxy(name string, config *config.ClaudeCodeProxyConfig, providerManager *ProviderManager) *ClaudeCodeProxy {
	client := &http.Client{
		Timeout: 120 * time.Second,
	}

	return &ClaudeCodeProxy{
		name:            name,
		config:          config,
		providerManager: providerManager,
		client:          client,
		thinkTagRegex:   regexp.MustCompile(`<think>(.*?)</think>`),
	}
}

// GetName returns the provider name
func (p *ClaudeCodeProxy) GetName() string {
	return p.name
}

// IsStreamingRequest checks if the request is for streaming
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

// ClaudeCodeMessage represents a message in Claude Code request format
type ClaudeCodeMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // Can be string or array of content blocks
}

// ClaudeCodeRequest represents a Claude Code API request
type ClaudeCodeRequest struct {
	Model         string               `json:"model"`
	MaxTokens     int                  `json:"max_tokens,omitempty"`
	Messages      []ClaudeCodeMessage  `json:"messages"`
	System        interface{}          `json:"system,omitempty"`        // Can be string or array
	StopSequences []string             `json:"stop_sequences,omitempty"`
	Stream        bool                 `json:"stream,omitempty"`
	Temperature   float64              `json:"temperature,omitempty"`
	TopP          float64              `json:"top_p,omitempty"`
	Tools         interface{}          `json:"tools,omitempty"`         // Pass through but ignore
	Metadata      interface{}          `json:"metadata,omitempty"`      // Pass through but ignore
}

// AnthropicError represents an error in Anthropic format
type AnthropicError struct {
	Type  string               `json:"type"`
	Error AnthropicErrorDetail `json:"error"`
}

// AnthropicErrorDetail represents error details in Anthropic format
type AnthropicErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// routeModelToProvider determines which provider to route to based on model name
func (p *ClaudeCodeProxy) routeModelToProvider(modelName string) string {
	if modelName == "" {
		return ""
	}
	
	modelLower := strings.ToLower(modelName)
	
	// Check for qwen models: qwen/*, *-thinking, exact matches
	if strings.Contains(modelLower, "qwen") || 
	   strings.HasSuffix(modelLower, "-thinking") ||
	   modelLower == "qwen/qwen3-next-80b-a3b-thinking" {
		return "qwen"
	}
	
	// Check for gpt-oss models: gpt-oss*, openai/gpt-oss*, exact matches
	if strings.Contains(modelLower, "gpt-oss") || 
	   strings.HasPrefix(modelLower, "openai/gpt-oss") ||
	   modelLower == "gpt-oss" ||
	   modelLower == "openai/gpt-oss-120b" {
		return "gpt-oss"
	}
	
	return ""
}

// convertClaudeCodeToOpenAI converts Claude Code request format to OpenAI format
func (p *ClaudeCodeProxy) convertClaudeCodeToOpenAI(claudeReq *ClaudeCodeRequest) map[string]interface{} {
	openaiReq := map[string]interface{}{
		"model":      claudeReq.Model,
		"stream":     claudeReq.Stream,
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
	
	var messages []map[string]interface{}
	
	// Convert messages and handle system message
	systemText := p.extractSystemText(claudeReq.System)
	if systemText != "" {
		// Add system message at the beginning
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": systemText,
		})
	}
	
	// Convert user/assistant messages
	for _, msg := range claudeReq.Messages {
		content := p.extractContentText(msg.Content)
		messages = append(messages, map[string]interface{}{
			"role":    msg.Role,
			"content": content,
		})
	}
	
	openaiReq["messages"] = messages
	return openaiReq
}

// extractContentText extracts text from Anthropic content (string or array format)
func (p *ClaudeCodeProxy) extractContentText(content interface{}) string {
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

// extractSystemText extracts text from system field (string or array format)
func (p *ClaudeCodeProxy) extractSystemText(system interface{}) string {
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
						textBuilder.WriteString("\n") // Separate multiple system blocks
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

// convertOpenAIToAnthropic converts OpenAI response format to Anthropic format
func (p *ClaudeCodeProxy) convertOpenAIToAnthropic(openaiResp map[string]interface{}) *AnthropicResponse {
	anthropicResp := &AnthropicResponse{
		Type: "message",
		Role: "assistant",
	}
	
	// Extract basic fields
	if id, ok := openaiResp["id"].(string); ok {
		anthropicResp.ID = id
	}
	if model, ok := openaiResp["model"].(string); ok {
		anthropicResp.Model = model
	}
	
	// Extract content from choices
	if choices, ok := openaiResp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := message["content"].(string); ok {
					// Process think tags to Anthropic thinking format
					processedContent := p.convertThinkTagsToAnthropic(content)
					anthropicResp.Content = []AnthropicContent{
						{
							Type: "text",
							Text: processedContent,
						},
					}
				}
			}
			
			// Extract stop reason
			if finishReason, ok := choice["finish_reason"].(string); ok {
				switch finishReason {
				case "stop":
					anthropicResp.StopReason = "end_turn"
				case "length":
					anthropicResp.StopReason = "max_tokens"
				case "content_filter":
					anthropicResp.StopReason = "stop_sequence"
				default:
					anthropicResp.StopReason = "end_turn"
				}
			}
		}
	}
	
	// Extract usage
	if usage, ok := openaiResp["usage"].(map[string]interface{}); ok {
		if inputTokens, ok := usage["prompt_tokens"].(float64); ok {
			anthropicResp.Usage.InputTokens = int(inputTokens)
		}
		if outputTokens, ok := usage["completion_tokens"].(float64); ok {
			anthropicResp.Usage.OutputTokens = int(outputTokens)
		}
	}
	
	return anthropicResp
}

// convertThinkTagsToAnthropic converts <think> tags to Anthropic thinking format
func (p *ClaudeCodeProxy) convertThinkTagsToAnthropic(content string) string {
	// For now, preserve ALL content including thinking tags
	// This ensures complete responses are returned to the user
	// Future enhancement: could format think tags specially if needed
	
	// Simply return the full content as-is
	// The thinking tags provide valuable context and reasoning
	return content
}

// createAnthropicError creates an error in Anthropic format
func (p *ClaudeCodeProxy) createAnthropicError(errorType, message string, statusCode int) ([]byte, int) {
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
func (p *ClaudeCodeProxy) Proxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
		
		// Determine target provider based on model
		targetProviderName := p.routeModelToProvider(claudeReq.Model)
		if targetProviderName == "" {
			errorBytes, statusCode := p.createAnthropicError("invalid_request_error", 
				fmt.Sprintf("Model '%s' is not supported. Supported models: qwen/*, *-thinking, gpt-oss*, openai/gpt-oss*", claudeReq.Model), 
				http.StatusBadRequest)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}
		
		// Get the target provider
		if p.providerManager == nil {
			errorBytes, statusCode := p.createAnthropicError("internal_server_error", "Provider manager not available", http.StatusInternalServerError)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}
		
		targetProvider := p.providerManager.GetProvider(targetProviderName)
		if targetProvider == nil {
			errorBytes, statusCode := p.createAnthropicError("service_unavailable", 
				fmt.Sprintf("Provider '%s' is not available", targetProviderName), 
				http.StatusServiceUnavailable)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}
		
		// Convert Claude Code request to OpenAI format
		openaiReq := p.convertClaudeCodeToOpenAI(&claudeReq)
		
		// Create new request body
		openaiReqBytes, err := json.Marshal(openaiReq)
		if err != nil {
			errorBytes, statusCode := p.createAnthropicError("internal_server_error", "Failed to convert request format", http.StatusInternalServerError)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}
		
		// Create new HTTP request for the target provider
		targetPath := fmt.Sprintf("/%s/v1/chat/completions", targetProviderName)
		newReq, err := http.NewRequestWithContext(req.Context(), "POST", targetPath, bytes.NewBuffer(openaiReqBytes))
		if err != nil {
			errorBytes, statusCode := p.createAnthropicError("internal_server_error", "Failed to create backend request", http.StatusInternalServerError)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write(errorBytes)
			return
		}
		
		// Copy headers
		for key, values := range req.Header {
			for _, value := range values {
				newReq.Header.Add(key, value)
			}
		}
		newReq.Header.Set("Content-Type", "application/json")
		newReq.URL.Path = targetPath
		
		// Handle streaming vs non-streaming
		if claudeReq.Stream {
			p.handleStreamingRequest(w, newReq, targetProvider)
		} else {
			p.handleNonStreamingRequest(w, newReq, targetProvider)
		}
	})
}

// handleNonStreamingRequest handles non-streaming requests
func (p *ClaudeCodeProxy) handleNonStreamingRequest(w http.ResponseWriter, req *http.Request, targetProvider Provider) {
	// Use the target provider's proxy to handle the request
	// We'll capture the response and convert it
	recorder := &responseRecorder{
		ResponseWriter: w,
		body:          &bytes.Buffer{},
		statusCode:    200,
		headers:       make(http.Header),
	}
	
	targetProvider.Proxy().ServeHTTP(recorder, req)
	
	// Parse the OpenAI response
	var openaiResp map[string]interface{}
	if err := json.Unmarshal(recorder.body.Bytes(), &openaiResp); err != nil {
		// If we can't parse as JSON, return error in Anthropic format
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
		errorBytes, statusCode := p.createAnthropicError("service_unavailable", errorMsg, recorder.statusCode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write(errorBytes)
		return
	}
	
	// Convert OpenAI response to Anthropic format
	anthropicResp := p.convertOpenAIToAnthropic(openaiResp)
	
	// Return Anthropic response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(recorder.statusCode)
	json.NewEncoder(w).Encode(anthropicResp)
}

// handleStreamingRequest handles streaming requests and converts SSE format
func (p *ClaudeCodeProxy) handleStreamingRequest(w http.ResponseWriter, req *http.Request, targetProvider Provider) {
	// Set up SSE headers for Anthropic format
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	
	// Create a pipe to capture streaming response
	pr, pw := io.Pipe()
	defer pr.Close()
	
	// Create a custom response writer that writes to our pipe
	streamRecorder := &streamResponseRecorder{
		ResponseWriter: w,
		pipe:          pw,
		headers:       make(http.Header),
	}
	
	// Send initial Anthropic streaming events
	p.writeAnthropicStreamEvent(w, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":      "msg_" + generateID(),
			"type":    "message",
			"role":    "assistant",
			"content": []interface{}{},
			"model":   req.Header.Get("model"),
		},
	})
	
	p.writeAnthropicStreamEvent(w, "content_block_start", map[string]interface{}{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	})
	
	// Start the target provider request in a goroutine
	go func() {
		defer pw.Close()
		targetProvider.Proxy().ServeHTTP(streamRecorder, req)
	}()
	
	// Process streaming response from target provider
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		line := scanner.Text()
		
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			
			if data == "[DONE]" {
				// Send final events
				p.writeAnthropicStreamEvent(w, "content_block_stop", map[string]interface{}{
					"type":  "content_block_stop",
					"index": 0,
				})
				p.writeAnthropicStreamEvent(w, "message_stop", map[string]interface{}{
					"type": "message_stop",
				})
				break
			}
			
			// Parse OpenAI streaming chunk
			var openaiChunk map[string]interface{}
			if err := json.Unmarshal([]byte(data), &openaiChunk); err == nil {
				if choices, ok := openaiChunk["choices"].([]interface{}); ok && len(choices) > 0 {
					if choice, ok := choices[0].(map[string]interface{}); ok {
						if delta, ok := choice["delta"].(map[string]interface{}); ok {
							if content, ok := delta["content"].(string); ok && content != "" {
								// Stream ALL content including think tags
								// This ensures complete responses are returned
								anthropicEvent := map[string]interface{}{
									"type":  "content_block_delta",
									"index": 0,
									"delta": map[string]interface{}{
										"type": "text_delta",
										"text": content,
									},
								}
								p.writeAnthropicStreamEvent(w, "content_block_delta", anthropicEvent)
							}
						}
					}
				}
			}
		}
		
		// Flush immediately for streaming, but handle potential connection errors
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
}


// writeAnthropicStreamEvent writes an event in Anthropic SSE format
func (p *ClaudeCodeProxy) writeAnthropicStreamEvent(w http.ResponseWriter, event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	
	// Handle potential write errors (client disconnect)
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData)); err != nil {
		// Client disconnected, don't log error as it's expected behavior
		return
	}
}

// generateID generates a simple ID for streaming messages
func generateID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// responseRecorder captures response for conversion
type responseRecorder struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
	headers    http.Header
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
}

func (r *responseRecorder) Header() http.Header {
	return r.headers
}

// streamResponseRecorder captures streaming response
type streamResponseRecorder struct {
	http.ResponseWriter
	pipe    *io.PipeWriter
	headers http.Header
}

func (r *streamResponseRecorder) Write(data []byte) (int, error) {
	return r.pipe.Write(data)
}

func (r *streamResponseRecorder) WriteHeader(code int) {
	// For streaming, we don't need to capture status code
}

func (r *streamResponseRecorder) Header() http.Header {
	return r.headers
}

// GetHealthStatus returns health status of the provider
func (p *ClaudeCodeProxy) GetHealthStatus() map[string]interface{} {
	status := map[string]interface{}{
		"status": "healthy",
		"supported_models": []string{
			"qwen/*", "*-thinking", "gpt-oss*", "openai/gpt-oss*",
		},
		"endpoint":           "/cc-local/v1/messages",
		"supported_providers": []string{"qwen", "gpt-oss"},
	}
	
	// Check provider manager availability
	if p.providerManager != nil {
		providersStatus := make(map[string]interface{})
		for _, providerName := range []string{"qwen", "gpt-oss"} {
			provider := p.providerManager.GetProvider(providerName)
			if provider != nil {
				providersStatus[providerName] = provider.GetHealthStatus()
			} else {
				providersStatus[providerName] = "unavailable"
				status["status"] = "degraded"
			}
		}
		status["providers_health"] = providersStatus
	} else {
		status["status"] = "unhealthy"
		status["error"] = "Provider manager not available"
	}
	
	return status
}

// UserIDFromRequest extracts user ID from request (not applicable for Claude Code proxy)
func (p *ClaudeCodeProxy) UserIDFromRequest(req *http.Request) string {
	return ""
}

// RegisterExtraRoutes allows the provider to register additional routes
func (p *ClaudeCodeProxy) RegisterExtraRoutes(router *mux.Router) {
	// Register the unified Claude Code endpoint
	router.HandleFunc("/cc-local/v1/messages", p.Proxy().ServeHTTP).Methods("POST")
}

// ValidateAPIKey validates API key (Claude Code proxy may not need keys)
func (p *ClaudeCodeProxy) ValidateAPIKey(req *http.Request, keyStore APIKeyStore) error {
	// Claude Code proxy doesn't require separate API key validation
	return nil
}

// ExtractRequestModelAndMessages extracts model and messages for token estimation
func (p *ClaudeCodeProxy) ExtractRequestModelAndMessages(req *http.Request) (string, []string) {
	var requestBody ClaudeCodeRequest
	var messages []string

	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			if err := json.Unmarshal(bodyBytes, &requestBody); err == nil {
				// Extract messages
				for _, msg := range requestBody.Messages {
					content := p.extractContentText(msg.Content)
					messages = append(messages, content)
				}
				// Add system message if present
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
func (p *ClaudeCodeProxy) ParseResponseMetadata(responseBody io.Reader, isStreaming bool) (*LLMResponseMetadata, error) {
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
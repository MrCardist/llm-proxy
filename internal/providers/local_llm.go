package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Instawork/llm-proxy/internal/config"
	"github.com/gorilla/mux"
)

// LocalLLMProvider implements the Provider interface for local LLM providers
type LocalLLMProvider struct {
	name              string
	config            *config.LocalLLMProviderConfig
	client            *http.Client
	thinkTagRegex     *regexp.Regexp
	endThinkTagRegex  *regexp.Regexp
}

// NewLocalLLMProvider creates a new local LLM provider
func NewLocalLLMProvider(name string, config *config.LocalLLMProviderConfig) *LocalLLMProvider {
	timeout := 120 * time.Second
	if config.RequestTimeout > 0 {
		timeout = time.Duration(config.RequestTimeout) * time.Second
	}

	return &LocalLLMProvider{
		name:   name,
		config: config,
		client: &http.Client{
			Timeout: timeout,
		},
		// Regex to detect if response already has <think> tags
		thinkTagRegex:    regexp.MustCompile(`(?i)<think>`),
		endThinkTagRegex: regexp.MustCompile(`(?i)</think>`),
	}
}

// GetName returns the provider name
func (p *LocalLLMProvider) GetName() string {
	return p.name
}

// IsStreamingRequest checks if the request is for streaming
func (p *LocalLLMProvider) IsStreamingRequest(req *http.Request) bool {
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

// selectEndpoint randomly selects an endpoint from the available ones for a model
func (p *LocalLLMProvider) selectEndpoint(modelName string) (*config.LocalLLMEndpointConfig, error) {
	model, exists := p.config.Models[modelName]
	if !exists || len(model.Endpoints) == 0 {
		return nil, fmt.Errorf("no endpoints configured for model %s", modelName)
	}

	// Random selection (stateless round-robin as specified)
	rand.Seed(time.Now().UnixNano())
	selectedEndpoint := model.Endpoints[rand.Intn(len(model.Endpoints))]
	
	return &selectedEndpoint, nil
}

// getModelNameFromRequest extracts the model name from the request body
func (p *LocalLLMProvider) getModelNameFromRequest(req *http.Request) string {
	var requestBody map[string]interface{}
	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			if err := json.Unmarshal(bodyBytes, &requestBody); err == nil {
				if model, ok := requestBody["model"].(string); ok {
					return model
				}
			}
		}
	}
	// If no model specified, use default
	return p.config.DefaultModel
}

// makeRequest makes a request to the selected endpoint with retry logic
func (p *LocalLLMProvider) makeRequest(req *http.Request, modelName string) (*http.Response, error) {
	maxRetries := 3
	if p.config.MaxRetries > 0 {
		maxRetries = p.config.MaxRetries
	}

	var lastErr error
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		endpoint, err := p.selectEndpoint(modelName)
		if err != nil {
			return nil, err
		}

		// Create new request for this endpoint
		targetURL, err := url.Parse(endpoint.URL)
		if err != nil {
			lastErr = fmt.Errorf("invalid endpoint URL %s: %w", endpoint.URL, err)
			continue
		}

		// Clone the original request
		clonedReq := req.Clone(context.Background())
		clonedReq.URL.Scheme = targetURL.Scheme
		clonedReq.URL.Host = targetURL.Host
		clonedReq.URL.Path = strings.TrimSuffix(targetURL.Path, "/") + strings.TrimPrefix(req.URL.Path, "/"+p.name)
		clonedReq.Host = targetURL.Host

		// Set API key if provided
		if endpoint.APIKey != "" {
			clonedReq.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
		}

		// Make the request with 100ms timeout as specified
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		clonedReq = clonedReq.WithContext(ctx)
		
		resp, err := p.client.Do(clonedReq)
		cancel()

		if err == nil {
			return resp, nil
		}

		lastErr = fmt.Errorf("endpoint %s failed: %w", endpoint.URL, err)
		log.Printf("Attempt %d failed for endpoint %s: %v", attempt+1, endpoint.URL, err)
	}

	return nil, fmt.Errorf("all endpoints failed after %d attempts: %w", maxRetries, lastErr)
}

// processThinkTags processes the response to add <think> tags if needed (for Qwen)
func (p *LocalLLMProvider) processThinkTags(responseBody []byte, isStreaming bool, modelName string) []byte {
	if !p.config.ThinkingTagFix {
		return responseBody
	}

	// Only process for models ending with "-thinking"
	if !strings.HasSuffix(modelName, "-thinking") {
		return responseBody
	}

	content := string(responseBody)
	
	// Check if response already has <think> at the start
	if p.thinkTagRegex.MatchString(content) {
		return responseBody // Already has think tag, don't modify
	}

	if isStreaming {
		// For streaming: always inject <think> at the beginning for -thinking models
		if !strings.HasPrefix(content, "<think>") {
			content = "<think>" + content
		}
	} else {
		// For non-streaming: if response contains </think>, prepend <think> at start
		if p.endThinkTagRegex.MatchString(content) && !strings.HasPrefix(content, "<think>") {
			content = "<think>" + content
		}
	}

	return []byte(content)
}

// Proxy returns the HTTP handler for this provider
func (p *LocalLLMProvider) Proxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Get model name from request
		modelName := p.getModelNameFromRequest(req)
		isStreaming := p.IsStreamingRequest(req)

		// Make request with retry logic
		resp, err := p.makeRequest(req, modelName)
		if err != nil {
			// Return error in OpenAI JSON format as specified
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			errorResponse := map[string]interface{}{
				"error": map[string]interface{}{
					"message": fmt.Sprintf("Sorry the backend server is currently not available, please try again later or contact your Sysadmin. Details: %v", err),
					"type":    "service_unavailable",
					"code":    "service_unavailable",
				},
			}
			json.NewEncoder(w).Encode(errorResponse)
			return
		}
		defer resp.Body.Close()

		// Copy headers
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		// Set status code
		w.WriteHeader(resp.StatusCode)

		// Read and process response body
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error reading response body: %v", err)
			return
		}

		// Process think tags if needed
		processedBody := p.processThinkTags(responseBody, isStreaming, modelName)

		// Write the response
		w.Write(processedBody)
	})
}

// GetHealthStatus returns health status of the provider
func (p *LocalLLMProvider) GetHealthStatus() map[string]interface{} {
	status := map[string]interface{}{
		"status":    "healthy",
		"endpoints": make([]map[string]interface{}, 0),
	}

	// Check each endpoint for each model
	for modelName, modelConfig := range p.config.Models {
		if !modelConfig.Enabled {
			continue
		}

		for _, endpoint := range modelConfig.Endpoints {
			endpointStatus := map[string]interface{}{
				"model":    modelName,
				"url":      endpoint.URL,
				"status":   "unknown",
				"response_time": "N/A",
			}

			// Quick health check
			start := time.Now()
			healthURL := strings.TrimSuffix(endpoint.URL, "/") + "/models"
			
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			req, _ := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
			if endpoint.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+endpoint.APIKey)
			}
			
			resp, err := p.client.Do(req)
			cancel()
			
			if err == nil {
				resp.Body.Close()
				endpointStatus["status"] = "healthy"
				endpointStatus["response_time"] = time.Since(start).String()
			} else {
				endpointStatus["status"] = "unhealthy"
				endpointStatus["error"] = err.Error()
			}

			status["endpoints"] = append(status["endpoints"].([]map[string]interface{}), endpointStatus)
		}
	}

	return status
}

// UserIDFromRequest extracts user ID from request (not applicable for local LLMs)
func (p *LocalLLMProvider) UserIDFromRequest(req *http.Request) string {
	return ""
}

// RegisterExtraRoutes allows the provider to register additional routes
func (p *LocalLLMProvider) RegisterExtraRoutes(router *mux.Router) {
	// No extra routes needed for local LLM providers
}

// ValidateAPIKey validates API key (local LLMs may not need keys)
func (p *LocalLLMProvider) ValidateAPIKey(req *http.Request, keyStore APIKeyStore) error {
	// Local LLMs may not require API key validation
	return nil
}

// ExtractRequestModelAndMessages extracts model and messages for token estimation
func (p *LocalLLMProvider) ExtractRequestModelAndMessages(req *http.Request) (string, []string) {
	var requestBody map[string]interface{}
	var messages []string

	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			if err := json.Unmarshal(bodyBytes, &requestBody); err == nil {
				// Extract model
				model := p.config.DefaultModel
				if modelName, ok := requestBody["model"].(string); ok && modelName != "" {
					model = modelName
				}

				// Extract messages
				if messagesArray, ok := requestBody["messages"].([]interface{}); ok {
					for _, msgInterface := range messagesArray {
						if msg, ok := msgInterface.(map[string]interface{}); ok {
							if content, ok := msg["content"].(string); ok {
								messages = append(messages, content)
							}
						}
					}
				}

				return model, messages
			}
		}
	}

	return p.config.DefaultModel, messages
}

// ParseResponseMetadata extracts metadata from the response
func (p *LocalLLMProvider) ParseResponseMetadata(responseBody io.Reader, isStreaming bool) (*LLMResponseMetadata, error) {
	// Basic metadata parsing for local LLMs
	// This would need to be enhanced based on the actual response format of your local LLMs
	
	metadata := &LLMResponseMetadata{
		Provider:    p.name,
		IsStreaming: isStreaming,
	}

	// Try to parse response for token usage
	if !isStreaming {
		bodyBytes, err := io.ReadAll(responseBody)
		if err != nil {
			return metadata, nil
		}

		var response map[string]interface{}
		if json.Unmarshal(bodyBytes, &response) == nil {
			if usage, ok := response["usage"].(map[string]interface{}); ok {
				if inputTokens, ok := usage["prompt_tokens"].(float64); ok {
					metadata.InputTokens = int(inputTokens)
				}
				if outputTokens, ok := usage["completion_tokens"].(float64); ok {
					metadata.OutputTokens = int(outputTokens)
				}
				if totalTokens, ok := usage["total_tokens"].(float64); ok {
					metadata.TotalTokens = int(totalTokens)
				}
			}

			if model, ok := response["model"].(string); ok {
				metadata.Model = model
			}
		}
	}

	return metadata, nil
}
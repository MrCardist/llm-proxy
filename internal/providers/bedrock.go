package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/gorilla/mux"
)

// BedrockProxy handles AWS Bedrock API requests and implements the Provider interface
type BedrockProxy struct {
	client *bedrockruntime.Client
	region string
}

// NewBedrockProxy creates a new Bedrock proxy
func NewBedrockProxy() *BedrockProxy {
	ctx := context.Background()

	// Determine AWS profile to use with precedence:
	// 1. AWS_PROFILE env var
	// 2. "bedrock" profile
	// 3. default profile
	profile := os.Getenv("AWS_PROFILE")
	if profile == "" {
		profile = "bedrock"
		log.Printf("AWS_PROFILE not set, attempting to use [bedrock] profile")
	}

	// Determine region
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = "us-east-1" // Default region
	}

	// Try to load config with the specified profile
	var sdkConfig aws.Config
	var err error

	if profile == "bedrock" {
		// Try bedrock profile first
		sdkConfig, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithSharedConfigProfile(profile),
		)
		if err != nil {
			log.Printf("Failed to load [bedrock] profile, falling back to default: %v", err)
			// Fall back to default
			sdkConfig, err = config.LoadDefaultConfig(ctx, config.WithRegion(region))
			if err != nil {
				log.Fatalf("Failed to load AWS config: %v", err)
			}
		} else {
			log.Printf("Successfully loaded AWS [bedrock] profile")
		}
	} else {
		// Use specified profile or default
		sdkConfig, err = config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithSharedConfigProfile(profile),
		)
		if err != nil {
			log.Fatalf("Failed to load AWS config with profile %s: %v", profile, err)
		}
		log.Printf("Successfully loaded AWS profile: %s", profile)
	}

	// Create Bedrock Runtime client
	client := bedrockruntime.NewFromConfig(sdkConfig)

	return &BedrockProxy{
		client: client,
		region: region,
	}
}

// GetName returns the name of the provider
func (b *BedrockProxy) GetName() string {
	return "bedrock"
}

// IsStreamingRequest checks if the request is a streaming request
func (b *BedrockProxy) IsStreamingRequest(req *http.Request) bool {
	// Check for streaming in the Accept header first
	if strings.Contains(req.Header.Get("Accept"), "text/event-stream") {
		return true
	}

	// Check the request body for "stream": true
	if req.Method == "POST" && strings.Contains(req.URL.Path, "/invoke") {
		return b.checkStreamingInBody(req)
	}

	return false
}

// checkStreamingInBody reads the request body to check for "stream": true
func (b *BedrockProxy) checkStreamingInBody(req *http.Request) bool {
	if req.Body == nil {
		return false
	}

	var bodyBytes []byte
	var err error

	if req.GetBody != nil {
		bodyReader, err := req.GetBody()
		if err != nil {
			return false
		}
		defer bodyReader.Close()
		bodyBytes, err = io.ReadAll(bodyReader)
		if err != nil {
			return false
		}
	} else {
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return false
		}
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewBuffer(bodyBytes)), nil
		}
	}

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return false
	}

	stream, ok := body["stream"].(bool)
	return ok && stream
}

// Proxy returns the HTTP handler for this provider
func (b *BedrockProxy) Proxy() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract model ID from path: /bedrock/model/{modelId}/invoke
		pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/bedrock/"), "/")
		if len(pathParts) < 3 || pathParts[0] != "model" {
			http.Error(w, "Invalid Bedrock path format. Expected: /bedrock/model/{modelId}/invoke", http.StatusBadRequest)
			return
		}

		modelID := pathParts[1]
		action := pathParts[2]

		// Read request body
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to read request body: %v", err), http.StatusBadRequest)
			return
		}

		// Check if streaming is requested
		isStreaming := b.IsStreamingRequest(r) || action == "invoke-with-response-stream"

		if isStreaming {
			b.handleStreamingRequest(w, r, modelID, bodyBytes)
		} else {
			b.handleNonStreamingRequest(w, r, modelID, bodyBytes)
		}
	})
}

// handleNonStreamingRequest handles non-streaming Bedrock requests
func (b *BedrockProxy) handleNonStreamingRequest(w http.ResponseWriter, r *http.Request, modelID string, bodyBytes []byte) {
	ctx := r.Context()

	// Invoke the model
	output, err := b.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(modelID),
		Body:        bodyBytes,
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
	})

	if err != nil {
		log.Printf("Bedrock InvokeModel error for model %s: %v", modelID, err)
		http.Error(w, fmt.Sprintf("Bedrock error: %v", err), http.StatusBadGateway)
		return
	}

	// Set response headers
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// Write response body
	if _, err := w.Write(output.Body); err != nil {
		log.Printf("Error writing Bedrock response: %v", err)
	}
}

// handleStreamingRequest handles streaming Bedrock requests
func (b *BedrockProxy) handleStreamingRequest(w http.ResponseWriter, r *http.Request, modelID string, bodyBytes []byte) {
	ctx := r.Context()

	// Invoke the model with streaming
	output, err := b.client.InvokeModelWithResponseStream(ctx, &bedrockruntime.InvokeModelWithResponseStreamInput{
		ModelId:     aws.String(modelID),
		Body:        bodyBytes,
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
	})

	if err != nil {
		log.Printf("Bedrock InvokeModelWithResponseStream error for model %s: %v", modelID, err)
		http.Error(w, fmt.Sprintf("Bedrock streaming error: %v", err), http.StatusBadGateway)
		return
	}

	// Set SSE headers for streaming
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Get the event stream
	stream := output.GetStream()
	defer stream.Close()

	// Process events
	for event := range stream.Events() {
		switch e := event.(type) {
		case *types.ResponseStreamMemberChunk:
			// Convert Bedrock chunk to SSE format
			chunkData := e.Value.Bytes

			// Write as SSE data event
			if _, err := fmt.Fprintf(w, "data: %s\n\n", string(chunkData)); err != nil {
				log.Printf("Error writing stream chunk: %v", err)
				return
			}

			// Flush the response writer
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}

		default:
			log.Printf("Unknown event type in Bedrock stream: %T", e)
		}
	}

	// Check for stream errors
	if err := stream.Err(); err != nil {
		log.Printf("Error in Bedrock event stream: %v", err)
		fmt.Fprintf(w, "data: {\"error\": \"Stream error: %v\"}\n\n", err)
	}

	// Send final [DONE] message
	fmt.Fprintf(w, "data: [DONE]\n\n")
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// GetHealthStatus returns the health status of the Bedrock provider
func (b *BedrockProxy) GetHealthStatus() map[string]interface{} {
	return map[string]interface{}{
		"provider": "bedrock",
		"status":   "healthy",
		"region":   b.region,
		"note":     "AWS Bedrock uses IAM authentication",
	}
}

// UserIDFromRequest extracts user ID from request (Bedrock doesn't have a standard user ID field)
func (b *BedrockProxy) UserIDFromRequest(req *http.Request) string {
	return ""
}

// RegisterExtraRoutes registers additional routes for Bedrock
func (b *BedrockProxy) RegisterExtraRoutes(router *mux.Router) {
	// No extra routes needed for Bedrock
}

// ValidateAPIKey validates API keys (Bedrock uses IAM, no API keys)
func (b *BedrockProxy) ValidateAPIKey(req *http.Request, keyStore APIKeyStore) error {
	// Bedrock uses AWS IAM authentication, not API keys
	// No validation needed here
	return nil
}

// ExtractRequestModelAndMessages extracts model and messages from request
func (b *BedrockProxy) ExtractRequestModelAndMessages(req *http.Request) (string, []string) {
	if req.Body == nil {
		return "", nil
	}

	var bodyBytes []byte
	var err error

	if req.GetBody != nil {
		bodyReader, err := req.GetBody()
		if err != nil {
			return "", nil
		}
		defer bodyReader.Close()
		bodyBytes, err = io.ReadAll(bodyReader)
		if err != nil {
			return "", nil
		}
	} else {
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return "", nil
		}
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewBuffer(bodyBytes)), nil
		}
	}

	// Extract model from path
	pathParts := strings.Split(strings.TrimPrefix(req.URL.Path, "/bedrock/"), "/")
	model := ""
	if len(pathParts) >= 2 && pathParts[0] == "model" {
		model = pathParts[1]
	}

	// Parse body to extract messages
	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return model, nil
	}

	var messages []string

	// Try Anthropic/Claude format
	if msgs, ok := body["messages"].([]interface{}); ok {
		for _, msg := range msgs {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				if content, ok := msgMap["content"].(string); ok {
					messages = append(messages, content)
				} else if contentArray, ok := msgMap["content"].([]interface{}); ok {
					// Handle content array (Anthropic format)
					for _, c := range contentArray {
						if cMap, ok := c.(map[string]interface{}); ok {
							if text, ok := cMap["text"].(string); ok {
								messages = append(messages, text)
							}
						}
					}
				}
			}
		}
	}

	// Try prompt field (for some models)
	if prompt, ok := body["prompt"].(string); ok {
		messages = append(messages, prompt)
	}

	// Try inputText field (for some models)
	if inputText, ok := body["inputText"].(string); ok {
		messages = append(messages, inputText)
	}

	return model, messages
}

// ParseResponseMetadata extracts tokens and model information from Bedrock response
func (b *BedrockProxy) ParseResponseMetadata(responseBody io.Reader, isStreaming bool) (*LLMResponseMetadata, error) {
	metadata := &LLMResponseMetadata{
		Provider:    "bedrock",
		IsStreaming: isStreaming,
	}

	if isStreaming {
		return b.parseStreamingResponse(responseBody, metadata)
	}

	return b.parseNonStreamingResponse(responseBody, metadata)
}

// parseNonStreamingResponse parses non-streaming Bedrock response
func (b *BedrockProxy) parseNonStreamingResponse(responseBody io.Reader, metadata *LLMResponseMetadata) (*LLMResponseMetadata, error) {
	bodyBytes, err := io.ReadAll(responseBody)
	if err != nil {
		return metadata, err
	}

	var response map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &response); err != nil {
		return metadata, err
	}

	// Try to extract usage information (format varies by model)
	if usage, ok := response["usage"].(map[string]interface{}); ok {
		if inputTokens, ok := usage["input_tokens"].(float64); ok {
			metadata.InputTokens = int(inputTokens)
		}
		if outputTokens, ok := usage["output_tokens"].(float64); ok {
			metadata.OutputTokens = int(outputTokens)
		}
		metadata.TotalTokens = metadata.InputTokens + metadata.OutputTokens
	}

	// Extract stop reason
	if stopReason, ok := response["stop_reason"].(string); ok {
		metadata.FinishReason = stopReason
	}

	return metadata, nil
}

// parseStreamingResponse parses streaming Bedrock response
func (b *BedrockProxy) parseStreamingResponse(responseBody io.Reader, metadata *LLMResponseMetadata) (*LLMResponseMetadata, error) {
	scanner := bufio.NewScanner(responseBody)
	var lastChunk map[string]interface{}

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines and SSE comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Parse SSE data line
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			// Skip [DONE] marker
			if data == "[DONE]" {
				break
			}

			// Parse JSON chunk
			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			lastChunk = chunk

			// Try to extract usage from chunk (usually in the last chunk)
			if usage, ok := chunk["usage"].(map[string]interface{}); ok {
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

	// Extract finish reason from last chunk
	if lastChunk != nil {
		if stopReason, ok := lastChunk["stop_reason"].(string); ok {
			metadata.FinishReason = stopReason
		}
	}

	return metadata, scanner.Err()
}

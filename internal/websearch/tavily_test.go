package websearch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTavilyClient_IsConfigured(t *testing.T) {
	// Test without API key
	client := &TavilyClient{}
	if client.IsConfigured() {
		t.Error("Expected IsConfigured to return false when no API key is set")
	}

	// Test with API key
	client = &TavilyClient{apiKey: "test-key"}
	if !client.IsConfigured() {
		t.Error("Expected IsConfigured to return true when API key is set")
	}
}

func TestSearchResult_FormatAsText(t *testing.T) {
	result := &SearchResult{
		Query:  "test query",
		Answer: "This is a summary answer",
		Results: []SearchResultItem{
			{
				Title:   "First Result",
				URL:     "https://example.com/1",
				Content: "First result content",
				Score:   0.95,
			},
			{
				Title:   "Second Result",
				URL:     "https://example.com/2",
				Content: "Second result content",
				Score:   0.85,
			},
		},
	}

	formatted := result.FormatAsText()

	// Check that the formatted text contains expected elements
	if len(formatted) == 0 {
		t.Error("FormatAsText returned empty string")
	}

	expectedStrings := []string{
		"test query",
		"This is a summary answer",
		"First Result",
		"https://example.com/1",
		"First result content",
		"Second Result",
		"https://example.com/2",
		"Second result content",
	}

	for _, expected := range expectedStrings {
		if !containsString(formatted, expected) {
			t.Errorf("FormatAsText output missing expected string: %s", expected)
		}
	}
}

func TestTavilyClient_Search_Success(t *testing.T) {
	// Create a mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/search" {
			t.Errorf("Expected /search, got %s", r.URL.Path)
		}

		// Return mock response
		response := TavilySearchResponse{
			Query:  "nvidia news",
			Answer: "NVIDIA has announced new products",
			Results: []TavilyResult{
				{
					Title:   "NVIDIA News Article",
					URL:     "https://example.com/nvidia",
					Content: "NVIDIA announced new GPUs today",
					Score:   0.95,
				},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	// Create client with mock server
	client := &TavilyClient{
		apiKey:     "test-key",
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	// Execute search
	result, err := client.Search("nvidia news", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Verify result
	if result.Query != "nvidia news" {
		t.Errorf("Expected query 'nvidia news', got '%s'", result.Query)
	}
	if result.Answer != "NVIDIA has announced new products" {
		t.Errorf("Unexpected answer: %s", result.Answer)
	}
	if len(result.Results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(result.Results))
	}
	if result.Results[0].Title != "NVIDIA News Article" {
		t.Errorf("Unexpected title: %s", result.Results[0].Title)
	}
}

func TestTavilyClient_Search_WithOptions(t *testing.T) {
	var receivedRequest TavilySearchRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture the request
		json.NewDecoder(r.Body).Decode(&receivedRequest)

		// Return minimal response
		response := TavilySearchResponse{Query: "test"}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client := &TavilyClient{
		apiKey:     "test-key",
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	opts := &SearchOptions{
		MaxResults:     10,
		Days:           30,
		Advanced:       true,
		IncludeDomains: []string{"reuters.com", "cnn.com"},
		ExcludeDomains: []string{"spam.com"},
	}

	_, err := client.Search("test query", opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Verify request options were applied
	if receivedRequest.MaxResults != 10 {
		t.Errorf("Expected MaxResults 10, got %d", receivedRequest.MaxResults)
	}
	if receivedRequest.Days != 30 {
		t.Errorf("Expected Days 30, got %d", receivedRequest.Days)
	}
	if receivedRequest.SearchDepth != "advanced" {
		t.Errorf("Expected SearchDepth 'advanced', got '%s'", receivedRequest.SearchDepth)
	}
	if len(receivedRequest.IncludeDomains) != 2 {
		t.Errorf("Expected 2 IncludeDomains, got %d", len(receivedRequest.IncludeDomains))
	}
	if len(receivedRequest.ExcludeDomains) != 1 {
		t.Errorf("Expected 1 ExcludeDomains, got %d", len(receivedRequest.ExcludeDomains))
	}
}

func TestTavilyClient_Search_NotConfigured(t *testing.T) {
	client := &TavilyClient{} // No API key

	_, err := client.Search("test", nil)
	if err == nil {
		t.Error("Expected error when API key is not configured")
	}
}

func TestTavilyClient_Search_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid request"}`))
	}))
	defer server.Close()

	client := &TavilyClient{
		apiKey:     "test-key",
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	_, err := client.Search("test", nil)
	if err == nil {
		t.Error("Expected error on API error response")
	}
}

func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}

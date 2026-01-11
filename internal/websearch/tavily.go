package websearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// TavilyClient provides web search functionality via Tavily's REST API
type TavilyClient struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

// TavilySearchRequest represents a search request to Tavily API
type TavilySearchRequest struct {
	APIKey            string   `json:"api_key"`
	Query             string   `json:"query"`
	SearchDepth       string   `json:"search_depth,omitempty"`        // "basic" or "advanced"
	IncludeAnswer     bool     `json:"include_answer,omitempty"`      // Include AI-generated answer
	IncludeRawContent bool     `json:"include_raw_content,omitempty"` // Include raw HTML
	MaxResults        int      `json:"max_results,omitempty"`         // Max number of results (default 5)
	IncludeDomains    []string `json:"include_domains,omitempty"`     // Only search these domains
	ExcludeDomains    []string `json:"exclude_domains,omitempty"`     // Exclude these domains
	Days              int      `json:"days,omitempty"`                // Only return results from last N days
}

// TavilySearchResponse represents a search response from Tavily API
type TavilySearchResponse struct {
	Answer  string         `json:"answer,omitempty"`
	Query   string         `json:"query"`
	Results []TavilyResult `json:"results"`
}

// TavilyResult represents a single search result
type TavilyResult struct {
	Title      string  `json:"title"`
	URL        string  `json:"url"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
	RawContent string  `json:"raw_content,omitempty"`
}

// NewTavilyClient creates a new Tavily client
func NewTavilyClient() *TavilyClient {
	apiKey := os.Getenv("TAVILY_API_KEY")

	return &TavilyClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		baseURL: "https://api.tavily.com",
	}
}

// IsConfigured returns true if the Tavily API key is configured
func (c *TavilyClient) IsConfigured() bool {
	return c.apiKey != ""
}

// Search performs a web search using Tavily API
func (c *TavilyClient) Search(query string, opts *SearchOptions) (*SearchResult, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("TAVILY_API_KEY not configured")
	}

	// Build request
	req := TavilySearchRequest{
		APIKey:        c.apiKey,
		Query:         query,
		SearchDepth:   "basic",
		IncludeAnswer: true,
		MaxResults:    5,
	}

	// Apply options
	if opts != nil {
		if opts.MaxResults > 0 {
			req.MaxResults = opts.MaxResults
		}
		if opts.Days > 0 {
			req.Days = opts.Days
		}
		if opts.Advanced {
			req.SearchDepth = "advanced"
		}
		if len(opts.IncludeDomains) > 0 {
			req.IncludeDomains = opts.IncludeDomains
		}
		if len(opts.ExcludeDomains) > 0 {
			req.ExcludeDomains = opts.ExcludeDomains
		}
	}

	// Marshal request
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make HTTP request
	httpReq, err := http.NewRequest("POST", c.baseURL+"/search", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tavily API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var tavilyResp TavilySearchResponse
	if err := json.Unmarshal(body, &tavilyResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to our result format
	result := &SearchResult{
		Query:   query,
		Answer:  tavilyResp.Answer,
		Results: make([]SearchResultItem, 0, len(tavilyResp.Results)),
	}

	for _, r := range tavilyResp.Results {
		result.Results = append(result.Results, SearchResultItem{
			Title:   r.Title,
			URL:     r.URL,
			Content: r.Content,
			Score:   r.Score,
		})
	}

	return result, nil
}

// SearchOptions configures search behavior
type SearchOptions struct {
	MaxResults     int      // Max number of results
	Days           int      // Only results from last N days
	Advanced       bool     // Use advanced search depth
	IncludeDomains []string // Only search these domains
	ExcludeDomains []string // Exclude these domains
}

// SearchResult represents web search results
type SearchResult struct {
	Query   string             `json:"query"`
	Answer  string             `json:"answer,omitempty"`
	Results []SearchResultItem `json:"results"`
}

// SearchResultItem represents a single search result
type SearchResultItem struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// FormatAsText formats search results as readable text for the LLM
func (r *SearchResult) FormatAsText() string {
	var buf bytes.Buffer

	buf.WriteString(fmt.Sprintf("Web Search Results for: %s\n\n", r.Query))

	if r.Answer != "" {
		buf.WriteString(fmt.Sprintf("Summary: %s\n\n", r.Answer))
	}

	buf.WriteString("Sources:\n")
	for i, item := range r.Results {
		buf.WriteString(fmt.Sprintf("\n[%d] %s\n", i+1, item.Title))
		buf.WriteString(fmt.Sprintf("URL: %s\n", item.URL))
		buf.WriteString(fmt.Sprintf("Content: %s\n", item.Content))
	}

	return buf.String()
}

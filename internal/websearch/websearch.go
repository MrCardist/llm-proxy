package websearch

import (
	"bytes"
	"fmt"
)

// Client is the interface for web search implementations
type Client interface {
	// IsConfigured returns true if the client is properly configured
	IsConfigured() bool

	// Search performs a web search and returns results
	Search(query string, opts *SearchOptions) (*SearchResult, error)
}

// SearchOptions contains options for a web search
type SearchOptions struct {
	MaxResults     int      // Maximum number of results to return
	Days           int      // Filter results to last N days (0 = no filter)
	Advanced       bool     // Use advanced search mode (e.g., news search)
	IncludeDomains []string // Only include results from these domains
	ExcludeDomains []string // Exclude results from these domains
}

// SearchResult represents search results
type SearchResult struct {
	Query   string             // The search query
	Answer  string             // Optional summary/answer (if available)
	Results []SearchResultItem // List of search results
}

// SearchResultItem represents a single search result
type SearchResultItem struct {
	Title   string  // Page title
	URL     string  // Page URL
	Content string  // Snippet/preview content
	Score   float64 // Relevance score (0-1, optional)
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

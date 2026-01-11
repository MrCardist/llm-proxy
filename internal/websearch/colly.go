package websearch

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

// CollyClient is a web scraping client using Colly
type CollyClient struct {
	userAgent string
	timeout   time.Duration
}

// NewCollyClient creates a new Colly-based web scraping client
func NewCollyClient() *CollyClient {
	return &CollyClient{
		userAgent: "Mozilla/5.0 (compatible; LLMProxy/1.0)",
		timeout:   30 * time.Second,
	}
}

// IsConfigured returns true (Colly doesn't require configuration)
func (c *CollyClient) IsConfigured() bool {
	return true
}

// Search performs a web search by scraping Google search results
func (c *CollyClient) Search(query string, opts *SearchOptions) (*SearchResult, error) {
	// Build Google search URL
	searchURL, err := c.buildGoogleSearchURL(query, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build search URL: %w", err)
	}

	log.Printf("Colly: Scraping Google search: %s", searchURL)

	// Create collector
	collector := colly.NewCollector(
		colly.UserAgent(c.userAgent),
		colly.AllowURLRevisit(),
	)
	collector.SetRequestTimeout(c.timeout)

	result := &SearchResult{
		Query:   query,
		Results: []SearchResultItem{},
	}

	// Parse search results
	collector.OnHTML("div.g", func(e *colly.HTMLElement) {
		title := e.ChildText("h3")
		link := e.ChildAttr("a", "href")
		snippet := e.ChildText("div.VwiC3b")

		// Clean up the link (remove Google redirect)
		if strings.HasPrefix(link, "/url?q=") {
			parsedURL, err := url.Parse(link)
			if err == nil {
				link = parsedURL.Query().Get("q")
			}
		}

		if title != "" && link != "" {
			result.Results = append(result.Results, SearchResultItem{
				Title:   title,
				URL:     link,
				Content: snippet,
				Score:   0.0,
			})
		}
	})

	// Handle errors
	collector.OnError(func(r *colly.Response, err error) {
		log.Printf("Colly: Error scraping %s: %v", r.Request.URL, err)
	})

	// Visit the search URL
	if err := collector.Visit(searchURL); err != nil {
		return nil, fmt.Errorf("failed to visit search URL: %w", err)
	}

	// Limit results if specified
	if opts != nil && opts.MaxResults > 0 && len(result.Results) > opts.MaxResults {
		result.Results = result.Results[:opts.MaxResults]
	}

	log.Printf("Colly: Found %d results for query: %s", len(result.Results), query)
	return result, nil
}

// buildGoogleSearchURL constructs a Google search URL with parameters
func (c *CollyClient) buildGoogleSearchURL(query string, opts *SearchOptions) (string, error) {
	baseURL := "https://www.google.com/search"
	params := url.Values{}
	params.Set("q", query)

	if opts != nil {
		// Add news search parameter
		if opts.Advanced {
			params.Set("tbm", "nws") // News search
		}

		// Add time filter
		if opts.Days > 0 {
			if opts.Days == 1 {
				params.Set("tbs", "qdr:d")
			} else if opts.Days == 7 {
				params.Set("tbs", "qdr:w")
			} else if opts.Days == 30 {
				params.Set("tbs", "qdr:m")
			} else {
				params.Set("tbs", fmt.Sprintf("qdr:d%d", opts.Days))
			}
		}

		// Add site filters
		if len(opts.IncludeDomains) > 0 {
			siteQuery := ""
			for _, domain := range opts.IncludeDomains {
				if siteQuery != "" {
					siteQuery += " OR "
				}
				siteQuery += "site:" + domain
			}
			params.Set("q", query+" ("+siteQuery+")")
		}

		if len(opts.ExcludeDomains) > 0 {
			excludeQuery := query
			for _, domain := range opts.ExcludeDomains {
				excludeQuery += " -site:" + domain
			}
			params.Set("q", excludeQuery)
		}
	}

	searchURL := baseURL + "?" + params.Encode()
	return searchURL, nil
}

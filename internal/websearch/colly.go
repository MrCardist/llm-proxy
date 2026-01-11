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
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		timeout:   30 * time.Second,
	}
}

// IsConfigured returns true (Colly doesn't require configuration)
func (c *CollyClient) IsConfigured() bool {
	return true
}

// Search performs a web search by scraping Google search results
func (c *CollyClient) Search(query string, opts *SearchOptions) (*SearchResult, error) {
	// Always use news search mode to avoid bot detection on regular search
	// Google serves static HTML for news, but requires JS for regular search
	if opts == nil {
		opts = &SearchOptions{}
	}
	if !opts.Advanced {
		opts.Advanced = true // Enable news mode
		// Set default time window based on query keywords
		if opts.Days == 0 {
			queryLower := strings.ToLower(query)
			if strings.Contains(queryLower, "news") ||
				strings.Contains(queryLower, "recent") ||
				strings.Contains(queryLower, "latest") ||
				strings.Contains(queryLower, "today") {
				opts.Days = 7 // Short window for news queries
			} else {
				opts.Days = 90 // Longer window for general queries
			}
		}
	}

	// Build Google search URL
	searchURL, err := c.buildGoogleSearchURL(query, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build search URL: %w", err)
	}

	log.Printf("Colly: Scraping Google News: %s (days=%d)", searchURL, opts.Days)

	// Create collector with more realistic browser headers
	collector := colly.NewCollector(
		colly.UserAgent(c.userAgent),
		colly.AllowURLRevisit(),
	)
	collector.SetRequestTimeout(c.timeout)

	// Note: Extra headers via OnRequest were found to break Colly callbacks
	// The default UserAgent set above is sufficient for news searches

	result := &SearchResult{
		Query:   query,
		Results: []SearchResultItem{},
	}

	// Try multiple selectors for Google search results (they change frequently)
	// News results typically use different containers
	// Register all callbacks directly to avoid Go closure issues with loops
	processResult := func(e *colly.HTMLElement) {
			// Try multiple ways to extract title
			title := ""
			titleSelectors := []string{"h3", "div[role='heading']", ".mCBkyc", ".n0jPhd"}
			for _, ts := range titleSelectors {
				title = e.ChildText(ts)
				if title != "" {
					break
				}
			}

			// Try multiple ways to extract link
			link := ""
			linkSelectors := []string{"a[href]", "a"}
			for _, ls := range linkSelectors {
				link = e.ChildAttr(ls, "href")
				if link != "" && !strings.HasPrefix(link, "#") {
					break
				}
			}

			// Try multiple ways to extract snippet/content
			snippet := ""
			snippetSelectors := []string{".VwiC3b", ".Y3v8qd", ".GI74Re", ".st"}
			for _, ss := range snippetSelectors {
				snippet = e.ChildText(ss)
				if snippet != "" {
					break
				}
			}

			// Clean up the link (remove Google redirect)
			if strings.HasPrefix(link, "/url?q=") {
				if parsedURL, err := url.Parse(link); err == nil {
					link = parsedURL.Query().Get("q")
				}
			}

			// Only add if we have at least title and link
			if title != "" && link != "" && !strings.HasPrefix(link, "/search") {
				// Check for duplicates
				isDuplicate := false
				for _, existing := range result.Results {
					if existing.URL == link {
						isDuplicate = true
						break
					}
				}

				if !isDuplicate {
					result.Results = append(result.Results, SearchResultItem{
						Title:   strings.TrimSpace(title),
						URL:     link,
						Content: strings.TrimSpace(snippet),
						Score:   0.0,
					})
				}
			}
	}

	// Register the callback for multiple selectors
	collector.OnHTML("div.Gx5Zad.xpd.EtOod.pkphOe", processResult) // News article container (2024+)
	collector.OnHTML("div.SoaBEf", processResult)                   // Another news container
	collector.OnHTML("div.g", processResult)                        // Traditional search result
	collector.OnHTML("article", processResult)                      // News articles

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

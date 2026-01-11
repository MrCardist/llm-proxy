package websearch

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

// BingClient is a web scraping client using Colly for Bing
type BingClient struct {
	userAgent string
	timeout   time.Duration
}

// NewBingClient creates a new Bing-based web scraping client
func NewBingClient() *BingClient {
	return &BingClient{
		userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		timeout:   30 * time.Second,
	}
}

// IsConfigured returns true (Bing doesn't require configuration)
func (b *BingClient) IsConfigured() bool {
	return true
}

// Search performs a web search by scraping Bing News results
// Bing limits results to ~14 per page, so we paginate to get more results
func (b *BingClient) Search(query string, opts *SearchOptions) (*SearchResult, error) {
	result := &SearchResult{
		Query:   query,
		Results: []SearchResultItem{},
	}

	maxResults := 20 // Default
	if opts != nil && opts.MaxResults > 0 {
		maxResults = opts.MaxResults
	}

	// Bing returns ~10-14 results per page (varies)
	// Calculate how many pages we need
	resultsPerPage := 12 // Average estimate
	pagesToFetch := (maxResults + resultsPerPage - 1) / resultsPerPage
	if pagesToFetch > 100 {
		pagesToFetch = 100 // Limit to 100 pages to avoid excessive requests
	}

	log.Printf("Bing: Fetching %d pages to get up to %d results", pagesToFetch, maxResults)

	for page := 0; page < pagesToFetch; page++ {
		// Build URL with pagination
		searchURL, err := b.buildBingSearchURL(query, opts, page*resultsPerPage)
		if err != nil {
			return nil, fmt.Errorf("failed to build search URL: %w", err)
		}

		// Create collector for this page
		collector := colly.NewCollector(
			colly.UserAgent(b.userAgent),
			colly.AllowURLRevisit(),
		)
		collector.SetRequestTimeout(b.timeout)

		pageResults := 0

		// Bing News uses different selectors than Google
		// Based on analysis of actual Bing News HTML structure
		processResult := func(e *colly.HTMLElement) {
			// Extract title from a.title element
			title := e.ChildText("a.title")

			// Extract link from a.title href attribute
			link := e.ChildAttr("a.title", "href")

			// Extract snippet from the snippet div (if available)
			snippet := e.ChildText(".snippet")

			// If no snippet, try alternative selectors
			if snippet == "" {
				snippet = e.ChildText("div[class*='snippet']")
			}

			// Bing sometimes uses relative URLs, make them absolute
			if strings.HasPrefix(link, "/") {
				link = "https://www.bing.com" + link
			}

			// Only add if we have at least title and link
			if title != "" && link != "" {
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
					pageResults++
				}
			}
		}

		// Register callback for Bing news card selector
		collector.OnHTML(".news-card.newsitem", processResult)

		// Handle errors
		collector.OnError(func(r *colly.Response, err error) {
			log.Printf("Bing: Error scraping page %d: %v", page+1, err)
		})

		// Visit the search URL
		if err := collector.Visit(searchURL); err != nil {
			log.Printf("Bing: Failed to visit page %d: %v", page+1, err)
			break // Stop pagination on error
		}

		log.Printf("Bing: Page %d returned %d results (total so far: %d)", page+1, pageResults, len(result.Results))

		// Stop if we got no results on this page (no more pages available)
		if pageResults == 0 {
			break
		}

		// Stop if we have enough results
		if len(result.Results) >= maxResults {
			break
		}

		// Small delay between requests to be respectful
		time.Sleep(500 * time.Millisecond)
	}

	// Trim to max_results if we exceeded it
	if len(result.Results) > maxResults {
		result.Results = result.Results[:maxResults]
	}

	log.Printf("Bing: Found %d total results for query: %s", len(result.Results), query)
	return result, nil
}

// buildBingSearchURL constructs a Bing News search URL with parameters
// start parameter is used for pagination (0-based index)
func (b *BingClient) buildBingSearchURL(query string, opts *SearchOptions, start int) (string, error) {
	baseURL := "https://www.bing.com/news/search"
	params := url.Values{}
	params.Set("q", query)

	// Set pagination offset
	if start > 0 {
		params.Set("first", fmt.Sprintf("%d", start+1)) // Bing uses 1-based indexing
	}

	if opts != nil {
		// Add time filter using Bing's interval codes
		// interval="4" = past week
		// interval="8" = past month
		if opts.Days > 0 {
			if opts.Days <= 7 {
				params.Set("qft", `interval="4"`) // Past week
			} else if opts.Days <= 30 {
				params.Set("qft", `interval="8"`) // Past month
			}
			// Note: Bing doesn't have as granular time filters as Google
		}

		// Add site filters (same as Google syntax)
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

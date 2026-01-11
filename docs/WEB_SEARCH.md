# Web Search Integration

Comprehensive documentation for the proxy's built-in web search capabilities.

## Overview

The LLM Proxy includes a sophisticated web search system that enables open-source models (GLM, DeepSeek, Qwen, etc.) to access real-time web information without requiring native search capabilities or external API keys.

## Architecture

### Technology Stack

- **Web Scraper**: [Colly](https://go-colly.org) v2 - Fast, elegant web scraping framework for Go
- **Search Engine**: Bing (regular search) and Bing News (for news queries)
- **No Dependencies**: Pure web scraping - no third-party APIs or keys required
- **Language**: Go 1.24.5

### How It Works

The web search feature operates as an **agentic loop**:

```
┌─────────────┐
│   Client    │
│   Request   │
└──────┬──────┘
       │
       ▼
┌─────────────────┐
│ LLM Proxy       │
│ (Tool Injection)│
└──────┬──────────┘
       │
       ▼
┌─────────────────┐
│ LLM Processes   │
│ & Uses Tool     │
└──────┬──────────┘
       │
       ▼
┌─────────────────┐
│ Proxy Intercepts│
│ web_search Call │
└──────┬──────────┘
       │
       ▼
┌─────────────────┐
│ Colly Scraper   │
│ (Bing Search)   │
└──────┬──────────┘
       │
       ▼
┌─────────────────┐
│ Parse Results   │
│ & Format        │
└──────┬──────────┘
       │
       ▼
┌─────────────────┐
│ Send Tool Result│
│ Back to LLM     │
└──────┬──────────┘
       │
       ▼
┌─────────────────┐
│ LLM Generates   │
│ Final Response  │
└──────┬──────────┘
       │
       ▼
┌─────────────────┐
│ Return to Client│
└─────────────────┘
```

## Search Modes

The proxy intelligently selects between two search modes:

### Regular Bing Search (Default)

Used for general queries - the default mode for all searches.

**When Used**:
- Default for all queries
- General information lookups
- Historical data
- Non-time-sensitive queries

**HTML Parsing**:
- Container selector: `li.b_algo`
- Title: `h2 a` (text content)
- URL: `h2 a` (href attribute)
- Snippet: `.b_caption p` (text content)

**Example Query**: "golang best practices"

### Bing News Search (Auto-detected)

Automatically triggered for news-related queries.

**When Used**:
- Query contains keywords: "news", "recent", "latest", "today"
- Explicitly requested via `Advanced: true` flag
- Time-sensitive information needs

**HTML Parsing**:
- Container selector: `div.news-card`
- Title: `a.title` (text content)
- URL: `a.title` (href attribute)
- Snippet: `div.snippet` (text content)

**Time Filters**:
- News queries: 7 days
- General queries with time filter: 90 days

**Example Query**: "latest AI news"

## Configuration

### Enable Web Search

Edit `configs/onprem.yml`:

```yaml
claude_code_cloud:
  enabled: true
  web_search:
    enabled: true              # Enable/disable web search
    provider: "colly"          # Always use "colly"
    tool_name: "web_search"    # Tool name in API (customizable)
    max_results: 100           # Max results per search (1-1000+)
```

### Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | false | Enable web search functionality |
| `provider` | string | "colly" | Search provider (only "colly" supported) |
| `tool_name` | string | "web_search" | Name of tool injected into requests |
| `max_results` | int | 5 | Maximum results to return per search |

## Pagination

The Colly scraper supports intelligent pagination to fetch as many results as needed.

### How Pagination Works

1. **Calculate Pages**: Determines pages needed based on `max_results`
   - Bing returns ~12 results per page (average)
   - Formula: `pages = (max_results + 11) / 12`

2. **Fetch Multiple Pages**: Iterates through pages sequentially
   - URL offset parameter: `first=N` (where N = page * 12)
   - Creates new Colly collector for each page
   - Deduplicates results across pages

3. **Stop Conditions**:
   - Target `max_results` reached
   - No new results found on page
   - Maximum 100 pages limit hit

### Example Pagination

```
max_results: 100
├─ Page 0 (first=0)  → 12 results (total: 12)
├─ Page 1 (first=12) → 11 results (total: 23)
├─ Page 2 (first=24) → 12 results (total: 35)
├─ ...
└─ Page 8 (first=96) → 13 results (total: 100) ✓ Stop
```

## Implementation Details

### Key Files

```
internal/websearch/
├── websearch.go        # Interface and types
├── colly.go            # Colly implementation
└── colly_test.go       # Unit tests

internal/providers/
└── claude_code_cloud.go  # Integration logic
```

### Core Types

```go
// Client interface - implemented by CollyClient
type Client interface {
    IsConfigured() bool
    Search(query string, opts *SearchOptions) (*SearchResult, error)
}

// Search options
type SearchOptions struct {
    MaxResults     int      // Max results to return
    Days           int      // Time filter (0 = no filter)
    Advanced       bool     // Use news search mode
    IncludeDomains []string // Domain whitelist
    ExcludeDomains []string // Domain blacklist
}

// Search result
type SearchResult struct {
    Query   string             // Original query
    Answer  string             // Summary (unused for Bing)
    Results []SearchResultItem // List of results
}

// Individual result item
type SearchResultItem struct {
    Title   string  // Page title
    URL     string  // Page URL
    Content string  // Snippet/preview
    Score   float64 // Relevance (unused)
}
```

### URL Construction

**Regular Bing Search**:
```
https://www.bing.com/search?q=<query>&first=<offset>&count=<max>
```

**Bing News Search**:
```
https://www.bing.com/news/search?q=<query>&first=<offset>&qft=interval%3d"7"
```

Time filter values:
- `interval="4"` - Past 24 hours
- `interval="7"` - Past week
- `interval="8"` - Past month

## Usage Examples

### Automatic Tool Injection

When web search is enabled, the proxy automatically injects the tool definition:

```json
{
  "type": "function",
  "function": {
    "name": "web_search",
    "description": "Search the web for current information...",
    "parameters": {
      "type": "object",
      "properties": {
        "query": {
          "type": "string",
          "description": "Search query"
        }
      },
      "required": ["query"]
    }
  }
}
```

### Search Execution Flow

1. **Client sends request** to `/cc/v1/messages`
2. **Proxy injects** `web_search` tool into request
3. **LLM decides** to use tool and returns:
   ```json
   {
     "type": "tool_use",
     "id": "toolu_123",
     "name": "web_search",
     "input": {"query": "golang concurrency patterns"}
   }
   ```
4. **Proxy intercepts** tool use and executes search
5. **Colly scrapes** Bing with pagination
6. **Results formatted** and sent back as tool result:
   ```json
   {
     "type": "tool_result",
     "tool_use_id": "toolu_123",
     "content": [
       {
         "type": "text",
         "text": "Web Search Results for: golang concurrency patterns\n\n[1] Concurrency Patterns in Go\nURL: https://..."
       }
     ]
   }
   ```
7. **LLM processes** results and generates final response
8. **Client receives** final answer with search context

## Performance Considerations

### Latency

- Single page fetch: ~1-3 seconds
- Multi-page (10 pages): ~5-15 seconds
- Network dependent

### Rate Limiting

Bing does not enforce strict rate limits for scraping, but consider:
- Add delays between pages if fetching 50+ pages
- Use reasonable `max_results` values (100-200)
- Monitor for CAPTCHA challenges (rare)

### Deduplication

Results are automatically deduplicated across pages:
- Checks URL uniqueness
- Prevents duplicate results in output
- Maintains insertion order

## Troubleshooting

### No Results Found

**Symptoms**: Search returns 0 results

**Possible Causes**:
1. Bing's HTML structure changed (selectors need updating)
2. Network connectivity issues
3. Bing detected scraping (rare)

**Solutions**:
- Check logs for scraping errors
- Test URL manually in browser
- Update HTML selectors in `colly.go`
- Add User-Agent rotation

### Wrong Search Mode

**Symptoms**: News results for general query or vice versa

**Solutions**:
- Check keyword detection logic in `colly.go`
- Explicitly set `Advanced: true/false` if needed
- Review query preprocessing

### Timeout Errors

**Symptoms**: Context deadline exceeded

**Solutions**:
- Increase timeout in `NewCollyClient()` (default: 30s)
- Reduce `max_results` to fewer pages
- Check network latency to Bing

## Testing

### Unit Tests

```bash
# Run web search tests
go test -v ./internal/websearch

# Run specific test
go test -v ./internal/websearch -run TestCollyClient_BuildBingSearchURL
```

### Integration Testing

```bash
# Test live search (requires internet)
go test -v ./internal/websearch -run TestCollyClient_Search

# Note: Tests may fail if Bing's HTML changes
```

### Manual Testing

```bash
# Start proxy in debug mode
./bin/llm-proxy --llm-debug

# Send request with tool use
curl -X POST http://localhost:9002/cc/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "hc/glm-4.7",
    "max_tokens": 1000,
    "messages": [{
      "role": "user",
      "content": "Search for latest Go 1.24 features"
    }]
  }'
```

## Future Enhancements

Potential improvements:

1. **Additional Search Engines**
   - Google (requires more sophisticated anti-bot measures)
   - DuckDuckGo (simpler HTML, privacy-focused)
   - Custom search APIs

2. **Caching Layer**
   - Cache search results for common queries
   - TTL-based invalidation
   - Reduce load on Bing

3. **Advanced Filtering**
   - Language detection and filtering
   - Content type filtering (PDFs, videos)
   - Domain reputation scoring

4. **Monitoring**
   - Search latency metrics
   - Success/failure rates
   - Popular queries tracking

5. **Result Enhancement**
   - Fetch full page content
   - Extract structured data
   - Summarization pre-processing

## Security Considerations

### No Authentication Required

Web search does not require API keys, making it:
- ✅ Easy to deploy
- ✅ No cost per search
- ⚠️  Subject to Bing's terms of service
- ⚠️  May be rate-limited by IP

### Data Privacy

- Search queries are sent to Bing's servers
- Results may be logged by Bing
- No user data is stored by proxy
- Consider using VPN/proxy if privacy is critical

### Input Validation

The proxy validates search queries:
- Max length: 1000 characters
- Sanitizes special characters
- Prevents command injection

## Support

For issues or questions:

1. Check logs: `sudo journalctl -u llm-proxy -f`
2. Enable debug mode: `./bin/llm-proxy --llm-debug`
3. Review test failures: `make test`
4. Open GitHub issue with logs and configuration

## References

- [Colly Documentation](https://go-colly.org/docs/)
- [Bing Search](https://www.bing.com)
- [Web Scraping Best Practices](https://www.scrapingbee.com/blog/web-scraping-best-practices/)

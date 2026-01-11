# LLM Proxy

<img height="250" alt="Screenshot 2025-09-08 at 10 10 08 AM" src="https://github.com/user-attachments/assets/5c6ecf7f-14bf-4d67-ba48-f250c80e3205" />

A simple, Go-based alternative to the `litellm` proxy, without all the extra stuff you don't need! A modular reverse proxy that forwards requests to various LLM providers (OpenAI, Anthropic, Gemini) using Go and the Gorilla web toolkit.

## Features of Dirk's Fork

This fork adds enterprise-grade features for hybrid cloud/on-premises deployments:

- **AWS Bedrock Support**: Full integration with AWS Bedrock including 28+ models (Claude, Nova, DeepSeek, GPT-OSS, Qwen, Titan)
  - Smart AWS profile precedence: `AWS_PROFILE` env var → `[bedrock]` profile → `default` profile
  - Region from `~/.aws/config` profile (optional override via `AWS_REGION`)
- **Local LLM Integration**: Support for on-premises vLLM deployments
  - `/gpt-oss/*` - Local GPT-OSS models via OpenAI-compatible endpoints
  - `/qwen/*` - Local Qwen models with automatic `<think>` tag processing
  - Multi-endpoint failover with immediate rotation
- **Multi-Provider Routing (`/multi/*`)**: Intelligent federated model routing with automatic failover
  - Primary (on-prem) → Fallback (cloud) strategy
  - Health checking with configurable intervals
  - Latency and queue depth thresholds
  - Request format transformation (OpenAI ↔ Anthropic)
  - Model aliasing support
- **Claude Code Cloud (`/cc/v1/*`)**: ⭐ Production Anthropic-compatible endpoint for open-source models
  - Generic endpoint supporting multiple backends (Fireworks AI, local vLLM, OpenAI-compatible)
  - Model mapping: `hc/glm-4.7` → Fireworks `accounts/fireworks/models/glm-4p7`
  - Full Anthropic Messages API compatibility
  - Streaming and tool use with automatic web search injection
  - Built-in web search for models lacking native search
  - Usage tracking and token counting endpoints
  - Event logging support for telemetry
- **Claude Code Proxy (`/cc-qwen/*`)**: Anthropic API format → OpenAI format converter for local models
  - Enables Claude Code compatibility with local Qwen models
  - Format conversion with parameter mapping
- **Web Search Integration** ⭐: Intelligent proxy-side search for all models
  - **Technology**: [Colly](https://go-colly.org) web scraper for paginated Bing searches
  - **No API Keys**: Pure web scraping - zero external dependencies
  - **Dual Mode**: Regular Bing (default) + Bing News (auto-detected for news queries)
  - **Agentic Loop**: Tool injection → LLM tool use → Search execution → Result integration
  - **Smart Pagination**: Fetches up to 1000+ results across multiple pages
  - **Time Filtering**: 7-day window for news, 90-day for general queries
  - **Deduplication**: Automatic duplicate detection across pages
  - **Auto-Detection**: Triggers Bing News for queries containing: "news", "recent", "latest", "today"
- **Debug Mode**: Colored curl-equivalent request/response output (`--llm-debug` flag)
  - Cyan requests, green responses, yellow info
  - Pretty-printed JSON with sensitive data redacted
- **Environment-based Configuration**: Full `.env` support with `${VAR:-default}` expansion in YAML

## Features

- **Multi-provider support**: Full support for OpenAI, Anthropic, and Gemini
- **Streaming Support**: Native streaming support for all providers
- **OpenAI Integration**: Complete OpenAI API compatibility with `/openai` prefix
- **Anthropic Integration**: Claude API support with `/anthropic` prefix
- **Gemini Integration**: Google Gemini API support with `/gemini` prefix
- **Comprehensive Logging**: Request/response monitoring with streaming detection
- **CORS Support**: Browser-based application compatibility
- **Health Check**: Detailed health status for all providers
- **Configurable Port**: Environment variable configuration (default: 9002)
- **Rate Limiting (experimental)**: Optional request/token-based limits per user/API key/model/provider

## Quick Start

```bash
# Get help on available commands
make help

# Install dependencies and build
make install build

# Run the proxy
make run

# Or run in development mode
make dev
```

### Making requests

Once the proxy is running, you can make requests to LLM providers through the proxy:

```bash
# Health check (shows all provider statuses)
curl http://localhost:9002/health

# OpenAI Chat completions (replace YOUR_API_KEY with your actual OpenAI API key)
curl -X POST http://localhost:9002/openai/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Hello, world!"}],
    "max_tokens": 50
  }'

# OpenAI Streaming
curl -X POST http://localhost:9002/openai/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
    "model": "gpt-3.5-turbo",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true,
    "stream_options": {"include_usage": true}
  }'

# Anthropic Messages
curl -X POST http://localhost:9002/anthropic/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-3-sonnet-20240229",
    "max_tokens": 100,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'

# Gemini Generate Content
curl -X POST http://localhost:9002/gemini/v1/models/gemini-pro:generateContent?key=YOUR_API_KEY \
  -H "Content-Type: application/json" \
  -d '{
    "contents": [{"parts": [{"text": "Hello!"}]}]
  }'
```

## Testing

The project includes comprehensive integration tests for all providers:

```bash
# Run all tests
make test-all

# Run tests for specific providers
make test-openai
make test-anthropic
make test-gemini

# Run health check tests only
make test-health

# Check environment variables
make env-check
```

### Setting up API Keys

To run integration tests, you need to set up environment variables:

```bash
export OPENAI_API_KEY=your_openai_key
export ANTHROPIC_API_KEY=your_anthropic_key
export GEMINI_API_KEY=your_gemini_key
```

## Configuration

- `PORT`: Environment variable to set the server port (default: 9002)

### Rate Limiting (Experimental)

- Disabled by default. Enable via config: see `configs/base.yml` and `configs/dev.yml`.
- Supports provisional token estimation with post-response reconciliation using `X-LLM-Input-Tokens` (input tokens only).
- Returns `429 Too Many Requests` with `Retry-After` and `X-RateLimit-*` headers when throttled.
 - Redis backend is currently not supported; only the in-process memory backend is available.

Minimal dev example (see `configs/dev.yml` for a full setup):

```yaml
features:
  rate_limiting:
    enabled: true
    backend: "memory" # single instance only
    estimation:
      max_sample_bytes: 20000
      bytes_per_token: 4 # Fallback to request size (Content-Length based)
      chars_per_token: 4 # Default for message-based estimation
      # Optional per-provider overrides (recommended)
      provider_chars_per_token:
        openai: 5      # ~185–190 tokens per 1k chars (from scripts/token_estimation.py)
        anthropic: 3   # ~290–315 tokens per 1k chars (from scripts/token_estimation.py)
    limits:
      requests_per_minute: 0   # 0 = unlimited (dev defaults)
      tokens_per_minute: 0
```

#### Token estimation behavior

- We currently account for and reconcile only input tokens. Output tokens are not yet considered for rate limits/credits.
- For small JSON requests (size controlled by `max_sample_bytes`), the proxy extracts textual message content via provider-specific parsers and estimates tokens by character count using `chars_per_token` (with per-provider overrides).
- Default per-provider values come from benchmarks produced by `scripts/token_estimation.py`. You can run the script to generate your own table and override values in config.
- Non-text modalities (images/videos) are not supported for estimation at this time and will fall back to credit-based only behavior essentially via `max_sample_bytes`.
- Optimistic first request: to avoid estimation blocking initial traffic, the first token-bearing request in a window (when current token count is zero) is allowed even if token limits would otherwise apply. Subsequent requests are enforced normally.

## API Endpoints

### Summary Table

| Prefix | Format | Backend | Notes |
|--------|--------|---------|-------|
| `/openai/*` | OpenAI | Real OpenAI API | Cloud only |
| `/anthropic/*` | Anthropic | Real Anthropic API | Cloud only |
| `/gemini/*` | Gemini | Real Gemini API | Cloud only |
| `/bedrock/*` | Mixed | AWS Bedrock | 28+ models (Claude, Nova, etc.) |
| `/gpt-oss/*` | OpenAI | Local vLLM | On-prem with failover |
| `/qwen/*` | OpenAI | Local vLLM | On-prem, `<think>` tag fix |
| `/cc/*` | **Anthropic** | Fireworks/Local | ⭐ Claude Code production endpoint (web search) |
| `/cc-qwen/*` | **Anthropic** | Local vLLM | Claude Code (local only) |
| `/multi/*` | OpenAI | On-prem + Cloud | Intelligent failover |
| `/meta/{userID}/*` | Various | Various | User-specific routing |
| `/health` | JSON | N/A | Status endpoint |

### General

- `GET /health` - Health check endpoint for all providers

### OpenAI

- `POST /openai/v1/chat/completions` - OpenAI chat completions endpoint (streaming supported)
- `POST /openai/v1/completions` - OpenAI completions endpoint (streaming supported)
- `*  /openai/v1/*` - All other OpenAI API endpoints

### Anthropic

- `POST /anthropic/v1/messages` - Anthropic messages endpoint (streaming supported)
- `*  /anthropic/v1/*` - All other Anthropic API endpoints

### Gemini

- `POST /gemini/v1/models/{model}:generateContent` - Gemini content generation (streaming supported)
- `POST /gemini/v1/models/{model}:streamGenerateContent` - Explicit streaming endpoint
- `*  /gemini/v1/*` - All other Gemini API endpoints

### AWS Bedrock

- `POST /bedrock/model/{modelId}/invoke` - Bedrock model invocation (streaming supported)
- `*  /bedrock/*` - All other Bedrock API endpoints

### Local LLMs

- `POST /gpt-oss/v1/chat/completions` - Local GPT-OSS models (OpenAI-compatible)
- `POST /qwen/v1/chat/completions` - Local Qwen models (OpenAI-compatible)
- `POST /cc-qwen/v1/messages` - Claude Code proxy to local Qwen (Anthropic-compatible)

### Multi-Provider (Federated Routing)

- `POST /multi/v1/chat/completions` - Intelligent routing with automatic failover
  - Example: Use `"model": "gpt-oss-120b"` to route to on-prem primary with Bedrock fallback

### Claude Code Cloud (`/cc`) ⭐

**Production Anthropic-compatible endpoint for Claude Code with open-source models and integrated web search:**

- `POST /cc/v1/messages` - Anthropic Messages API compatible
  - **With Web Search**: Automatic `web_search` tool injection
  - **Streaming Support**: Full SSE streaming with tool results
  - **Tool Use**: Complete tool call/result handling
  - **Multiple Backends**: Fireworks, local vLLM, or other OpenAI-compatible services
- `POST /cc/v1/messages/count_tokens` - Token counting endpoint
- `POST /cc/v1/api/event_logging/batch` - Telemetry event logging

**Client Configuration** (`~/.claude/settings.json`):
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://llm.example.edu/cc/v1",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "hc/glm-4.7",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "hc/glm-4.7",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "hc/deepseek-v3"
  }
}
```

**Available models** (configurable in `configs/onprem.yml`):
- `hc/glm-4.7` - GLM-4.7 via Fireworks (general purpose)
- `hc/deepseek-v3` - DeepSeek V3 via Fireworks (strong reasoning)
- `hc/kimi-k2` - Kimi K2 via Fireworks (best for tool use)

**Features**:
- Full Anthropic Messages API compatibility
- Automatic tool use and streaming support
- **Built-in web search** (see below)
- Usage tracking and token counting
- Event logging for analytics
- Multi-model support with custom routing
- Token counting and estimation

### Web Search Integration ⭐

**Bringing real-time information access to open-source models - no API keys required!**

The proxy includes built-in web search capabilities for open-source models that lack native search functionality (GLM, DeepSeek, Qwen, etc.).

#### How It Works

The web search feature operates as an agentic loop:

1. **Tool Injection**: When enabled, the proxy automatically injects a `web_search` tool into requests
2. **Search Execution**: When the LLM uses the `web_search` tool, the proxy intercepts it and executes the search
3. **Result Integration**: Search results are automatically fed back to the LLM as a tool response
4. **Continuation**: The conversation continues seamlessly with the LLM processing the search results

#### Search Technology

- **Scraper**: [Colly](https://go-colly.org) - Fast, elegant web scraping framework for Go
- **Search Engine**: Bing (regular search) and Bing News (for news queries)
- **No API Keys Required**: Pure web scraping, no third-party API dependencies
- **Pagination Support**: Fetches multiple pages to retrieve up to 1000+ results

#### Search Modes

The proxy intelligently selects the appropriate search mode:

**Regular Bing Search (Default)**:
- Used for general queries and fact-finding
- Returns web results, articles, documentation
- HTML selector: `li.b_algo`
- Extracts: title, URL, snippet from standard search results
- Time filter: 90 days (long-form content)

**Bing News Search (Auto-detected)**:
- Triggered automatically by keywords: "news", "recent", "latest", "today"
- Returns news articles, press releases, breaking news
- HTML selector: `div.news-card`
- Time filter: 7 days (fresh content)
- Can also be explicitly requested via `Advanced: true` in search options

| Query | Mode | Time Filter | Results |
|-------|------|-------------|---------|
| "golang best practices" | Regular | 90 days | Documentation, blogs, tutorials |
| "latest AI news" | News (auto) | 7 days | News articles, announcements |
| "recent database trends" | News (auto) | 7 days | Industry news, analysis |
| "python error handling" | Regular | 90 days | Documentation, Stack Overflow, blogs |

#### Configuration

Enable in `configs/onprem.yml`:

```yaml
claude_code_cloud:
  enabled: true
  web_search:
    enabled: true              # Enable web search functionality
    provider: "colly"          # Uses Colly for paginated Bing scraping
    tool_name: "web_search"    # Tool name injected into requests
    max_results: 100           # Max results per search (supports 1000+)
```

#### Architecture

**Search Flow**:
```
Client Request → Proxy → Tool Injection → LLM Response with tool_use
                   ↓
            Colly Web Scraper
                   ↓
          Bing / Bing News (with pagination)
                   ↓
         Search Results Extraction
                   ↓
    Tool Result → LLM Processing → Final Response
```

**Key Components**:
- `internal/websearch/websearch.go` - Search interface and result types
- `internal/websearch/colly.go` - Colly-based Bing scraper with pagination
- `internal/providers/claude_code_cloud.go` - Web search integration logic

**Pagination Logic**:
- Calculates pages needed based on `max_results`
- Fetches ~12 results per page (Bing's average)
- Stops when: target reached, no new results, or 100 pages limit hit
- Deduplicates results across pages

#### Usage Example

When web search is enabled, your Claude Code client can leverage real-time information:

```
User: "What are the latest developments in quantum computing?"
     ↓
Proxy injects web_search tool
     ↓
GLM-4.7 recognizes current events needed
     ↓
LLM triggers: {"type": "tool_use", "name": "web_search", "input": {"query": "...quantum computing 2026..."}}
     ↓
Proxy: Scrapes Bing News (auto-detected "latest")
Returns: 5-10 recent articles about quantum computing
     ↓
LLM: Synthesizes results with reasoning
     ↓
Response: "Recent developments include: [current, sourced information]"
```

#### Performance

- **Single search**: ~2-3 seconds (one page)
- **Multi-page (100 results)**: ~8-12 seconds (paginated)
- **Network dependent**: Latency varies with Bing responsiveness
- **No throttling**: Bing doesn't enforce strict rate limits for scraping

#### Limitations & Considerations

- **Dynamic Content**: Bing may change HTML selectors (we update as needed)
- **CAPTCHA**: Rare, but possible if excessive scraping detected
- **Terms of Service**: Web scraping may violate Bing's ToS (use responsibly)
- **Data Privacy**: Search queries sent to Bing's servers (consider privacy implications)

## Architecture

The proxy is built with a modular architecture:

- **`main.go`**: Core server setup, middleware, and provider registration
- **`providers/openai.go`**: OpenAI-specific proxy implementation with streaming support
- **`providers/anthropic.go`**: Anthropic proxy implementation with streaming support
- **`providers/gemini.go`**: Gemini proxy implementation with streaming support
- **`providers/provider.go`**: Common interfaces and provider management

Each provider implements its own:

- Route registration
- Request/response handling with streaming support
- Error handling
- Health status reporting
- Response metadata parsing

## Development

### Available Make Commands

```bash
# Get help on all available commands
make help

# Code quality
make check         # Run all code quality checks
make fmt           # Format Go code
make vet           # Run go vet
make lint          # Run golint

# Building
make build         # Build the binary
make clean         # Clean build artifacts
make install       # Install dependencies

# Running
make run           # Run the built binary
make dev           # Run in development mode

# Testing
make test          # Run unit tests
make test-all      # Run all tests including integration
make test-openai   # Run OpenAI tests only
make test-anthropic # Run Anthropic tests only
make test-gemini   # Run Gemini tests only
```

### Test Structure

Tests are organized by provider:

- **`openai_test.go`**: OpenAI integration tests (streaming and non-streaming)
- **`anthropic_test.go`**: Anthropic integration tests (streaming and non-streaming)
- **`gemini_test.go`**: Gemini integration tests (streaming and non-streaming)
- **`common_test.go`**: Health check and environment variable tests
- **`test_helpers.go`**: Shared test utilities

### Middleware

- **Logging**: Logs all incoming requests with streaming detection
- **CORS**: Adds CORS headers for browser compatibility
- **Streaming**: Optimized handling for streaming responses
- **Error Handling**: Provider-specific error handling

### Adding New Providers

To add a new provider:

1. Create a new file (e.g., `newprovider.go`)
2. Implement the `Provider` interface
3. Add streaming detection logic
4. Add response metadata parsing
5. Create corresponding test file
6. Register the provider in `main.go`

## Dependencies

- [Gorilla Mux](https://github.com/gorilla/mux) - HTTP router and URL matcher
- [Colly](https://github.com/gocolly/colly) - Web scraping framework for Go
- [AWS SDK for Go v2](https://github.com/aws/aws-sdk-go-v2) - For Bedrock integration
- [DataDog Go](https://github.com/DataDog/datadog-go) - Metrics and monitoring

## Deployment

### On-Premises Deployment

For on-premises deployments, all commands must be executed with the `appmotel` user to ensure proper permissions and security:

```bash
# Build the binary
make build

# Deploy/run as appmotel user
sudo -u appmotel ./bin/llm-proxy

# With systemd
sudo systemctl restart llm-proxy
sudo systemctl status llm-proxy

# View logs
sudo journalctl -u llm-proxy -f
```

### Production Deployment (AWS ECS)

The repository includes automated deployment scripts:

```bash
# Deploy to production
./scripts/deploy.sh production <git_sha>
```

This script:
1. Pulls the Docker image from ECR
2. Updates Terraform configuration
3. Deploys to ECS with specified CPU/memory resources
4. Validates deployment success

### Environment Variables

Required for production:
```bash
# Cloud Provider API Keys
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
export GEMINI_API_KEY=...
export FIREWORKS_API_KEY=fw-...

# AWS Configuration (for Bedrock)
export AWS_PROFILE=bedrock
export AWS_REGION=us-west-2

# Local LLM Endpoints (optional)
export GPT_OSS_ENDPOINT_1=http://192.168.1.100:8000/v1
export QWEN_ENDPOINT_1=http://192.168.1.200:8001/v1

# Server Configuration
export PORT=9002
export ENVIRONMENT=production
export LOG_LEVEL=info
export LOG_FORMAT=json
```

### Configuration Files

- `configs/base.yml` - Base configuration with model definitions
- `configs/dev.yml` - Development overrides
- `configs/onprem.yml` - On-premises deployment config
- `configs/production.yml` - Production overrides

Configuration supports environment variable expansion:
```yaml
local_llms:
  qwen:
    endpoints:
      - url: "${QWEN_ENDPOINT_1:-http://localhost:8001/v1}"
```

### Health Checks

Monitor service health:
```bash
# Basic health check
curl http://localhost:9002/health

# Response format
{
  "status": "healthy",
  "providers": {
    "openai": {"status": "configured"},
    "anthropic": {"status": "configured"},
    "cc": {"status": "ready", "web_search": "enabled"}
  }
}
```

### Debug Mode

For troubleshooting, enable debug mode to see curl-equivalent request/response output:

```bash
./bin/llm-proxy --llm-debug
```

This provides:
- Color-coded request/response logs (cyan/green/yellow)
- Pretty-printed JSON bodies
- Timing information
- Sensitive data automatically redacted

## Build Information

The binary includes build-time information:

- Git commit hash
- Build timestamp
- Go version

View build info with:

```bash
make version
```

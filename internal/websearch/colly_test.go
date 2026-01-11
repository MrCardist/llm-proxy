package websearch

import (
	"strings"
	"testing"
)

func TestCollyClient_IsConfigured(t *testing.T) {
	client := NewCollyClient()
	if !client.IsConfigured() {
		t.Error("Colly client should always be configured")
	}
}

func TestCollyClient_BuildBingSearchURL(t *testing.T) {
	client := NewCollyClient()

	tests := []struct {
		name       string
		query      string
		opts       *SearchOptions
		wantSubstr string
	}{
		{
			name:       "Basic search (regular Bing)",
			query:      "test query",
			opts:       nil,
			wantSubstr: "bing.com/search",
		},
		{
			name:       "Explicit regular search",
			query:      "golang programming",
			opts:       &SearchOptions{Advanced: false},
			wantSubstr: "bing.com/search",
		},
		{
			name:       "News search",
			query:      "nvidia",
			opts:       &SearchOptions{Advanced: true},
			wantSubstr: "bing.com/news/search",
		},
		{
			name:       "With time filter - 7 days",
			query:      "news",
			opts:       &SearchOptions{Advanced: true, Days: 7},
			wantSubstr: "qft=interval",
		},
		{
			name:       "With time filter - 30 days",
			query:      "news",
			opts:       &SearchOptions{Advanced: true, Days: 30},
			wantSubstr: "qft=interval",
		},
		{
			name:       "News with time filter",
			query:      "nvidia",
			opts:       &SearchOptions{Advanced: true, Days: 30},
			wantSubstr: "bing.com/news/search",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := client.buildBingSearchURL(tt.query, tt.opts, 0)
			if err != nil {
				t.Fatalf("buildBingSearchURL failed: %v", err)
			}

			if url == "" {
				t.Error("Expected non-empty URL")
			}

			if tt.wantSubstr != "" && !strings.Contains(url, tt.wantSubstr) {
				t.Errorf("URL %s does not contain expected substring %s", url, tt.wantSubstr)
			}
		})
	}
}

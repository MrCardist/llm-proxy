package websearch

import (
	"testing"
)

func TestCollyClient_IsConfigured(t *testing.T) {
	client := NewCollyClient()
	if !client.IsConfigured() {
		t.Error("Colly client should always be configured")
	}
}

func TestCollyClient_BuildGoogleSearchURL(t *testing.T) {
	client := NewCollyClient()

	tests := []struct {
		name     string
		query    string
		opts     *SearchOptions
		wantSubstr string
	}{
		{
			name:     "Basic search",
			query:    "test query",
			opts:     nil,
			wantSubstr: "q=test+query",
		},
		{
			name:     "News search",
			query:    "nvidia",
			opts:     &SearchOptions{Advanced: true},
			wantSubstr: "tbm=nws",
		},
		{
			name:     "With time filter - 7 days",
			query:    "news",
			opts:     &SearchOptions{Days: 7},
			wantSubstr: "tbs=qdr%3Aw",
		},
		{
			name:     "With time filter - 30 days",
			query:    "news",
			opts:     &SearchOptions{Days: 30},
			wantSubstr: "tbs=qdr%3Am",
		},
		{
			name:     "News with time filter",
			query:    "nvidia",
			opts:     &SearchOptions{Advanced: true, Days: 30},
			wantSubstr: "tbm=nws",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := client.buildGoogleSearchURL(tt.query, tt.opts)
			if err != nil {
				t.Fatalf("buildGoogleSearchURL failed: %v", err)
			}

			if url == "" {
				t.Error("Expected non-empty URL")
			}

			if tt.wantSubstr != "" && !containsString(url, tt.wantSubstr) {
				t.Errorf("URL %s does not contain expected substring %s", url, tt.wantSubstr)
			}
		})
	}
}

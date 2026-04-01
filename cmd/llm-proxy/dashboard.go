package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Instawork/llm-proxy/internal/cost"
)

//go:embed dashboard.html
var dashboardFS embed.FS

// Provider classification for routing breakdown
var (
	cloudProviders  = map[string]bool{"openai": true, "anthropic": true, "gemini": true, "bedrock": true}
	onpremProviders = map[string]bool{"gpt-oss": true, "qwen": true, "local": true}
)

// Dashboard cache
var (
	dashboardCache    map[string]*dashboardCacheEntry
	dashboardCacheMu  sync.RWMutex
	dashboardCacheTTL = 60 * time.Second
)

type dashboardCacheEntry struct {
	data      *dashboardData
	timestamp time.Time
}

func init() {
	dashboardCache = make(map[string]*dashboardCacheEntry)
}

// JSON response types

type dashboardOverview struct {
	TotalRequests    int     `json:"total_requests"`
	TotalTokens      int     `json:"total_tokens"`
	TotalInputTokens int     `json:"total_input_tokens"`
	TotalOutputTokens int    `json:"total_output_tokens"`
	TotalCost        float64 `json:"total_cost"`
	AvgCostPerReq    float64 `json:"avg_cost_per_request"`
	StreamingCount   int     `json:"streaming_count"`
	NonStreamingCount int    `json:"non_streaming_count"`
	StreamingPct     float64 `json:"streaming_pct"`
}

type modelStats struct {
	Requests     int     `json:"requests"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

type providerStats struct {
	Requests    int     `json:"requests"`
	TotalTokens int     `json:"total_tokens"`
	Cost        float64 `json:"cost"`
	Type        string  `json:"type"`
}

type userStats struct {
	Requests    int     `json:"requests"`
	TotalTokens int     `json:"total_tokens"`
	Cost        float64 `json:"cost"`
}

type routingStats struct {
	Requests int     `json:"requests"`
	Cost     float64 `json:"cost"`
}

type dailyStats struct {
	Requests    int     `json:"requests"`
	TotalTokens int     `json:"total_tokens"`
	Cost        float64 `json:"cost"`
}

type tokenSplitEntry struct {
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

type recentRequest struct {
	Timestamp    string  `json:"timestamp"`
	RequestID    string  `json:"request_id"`
	UserID       string  `json:"user_id"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	Endpoint     string  `json:"endpoint"`
	IsStreaming  bool    `json:"is_streaming"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	TotalCost    float64 `json:"total_cost"`
	FinishReason string  `json:"finish_reason"`
	IsEstimate   bool    `json:"is_estimate"`
}

type dashboardData struct {
	Overview           dashboardOverview          `json:"overview"`
	ByModel            map[string]*modelStats     `json:"by_model"`
	ByProvider         map[string]*providerStats  `json:"by_provider"`
	ByUser             map[string]*userStats      `json:"by_user"`
	Routing            map[string]*routingStats   `json:"routing"`
	DailyTrends        map[string]*dailyStats     `json:"daily_trends"`
	TopModelsTokenSplit []tokenSplitEntry         `json:"top_models_token_split"`
	RecentRequests     []recentRequest            `json:"recent_requests"`
	Uptime             string                     `json:"uptime"`
	CostFile           string                     `json:"cost_file"`
	ProxyURL           string                     `json:"proxy_url"`
	DateRange          map[string]string          `json:"date_range"`
}

func getRouteType(provider string) string {
	if cloudProviders[provider] {
		return "cloud"
	}
	if onpremProviders[provider] {
		return "on-prem"
	}
	return "hybrid"
}

func parseDaysParameter(daysParam string) (time.Time, time.Time) {
	now := time.Now()
	switch daysParam {
	case "mtd":
		start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return start, now
	case "last-month":
		firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		start := firstOfMonth.AddDate(0, -1, 0)
		return start, firstOfMonth
	default:
		days, err := strconv.Atoi(daysParam)
		if err != nil {
			days = 7
		}
		return now.AddDate(0, 0, -days), now
	}
}

func getCostFilePath() string {
	if globalCostTracker != nil {
		if p := globalCostTracker.GetCostFilePath(); p != "" {
			return p
		}
	}
	if p := os.Getenv("COST_TRACKING_FILE"); p != "" {
		return p
	}
	return "logs/cost-tracking.jsonl"
}

func readCostRecords(startDate, endDate time.Time) []cost.CostRecord {
	filePath := getCostFilePath()
	if filePath == "" {
		return nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		logger.Warn("Dashboard: could not open cost file", "path", filePath, "error", err)
		return nil
	}
	defer file.Close()

	var records []cost.CostRecord
	scanner := bufio.NewScanner(file)
	// Increase scanner buffer for large lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var record cost.CostRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		ts := record.Timestamp
		if ts.IsZero() {
			continue
		}
		if (ts.Equal(startDate) || ts.After(startDate)) && (ts.Equal(endDate) || ts.Before(endDate)) {
			records = append(records, record)
		}
	}

	// Sort by timestamp descending
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.After(records[j].Timestamp)
	})

	return records
}

func aggregateRecords(records []cost.CostRecord) *dashboardData {
	data := &dashboardData{
		ByModel:    make(map[string]*modelStats),
		ByProvider: make(map[string]*providerStats),
		ByUser:     make(map[string]*userStats),
		Routing: map[string]*routingStats{
			"cloud":  {},
			"on-prem": {},
			"hybrid": {},
		},
		DailyTrends: make(map[string]*dailyStats),
	}

	totalRequests := len(records)
	var totalTokens, totalInputTokens, totalOutputTokens, streamingCount int
	var totalCost float64

	for _, r := range records {
		totalTokens += r.TotalTokens
		totalInputTokens += r.InputTokens
		totalOutputTokens += r.OutputTokens
		totalCost += r.TotalCost
		if r.IsStreaming {
			streamingCount++
		}

		// By model
		model := r.Model
		if model == "" {
			model = "unknown"
		}
		if _, ok := data.ByModel[model]; !ok {
			data.ByModel[model] = &modelStats{}
		}
		data.ByModel[model].Requests++
		data.ByModel[model].InputTokens += r.InputTokens
		data.ByModel[model].OutputTokens += r.OutputTokens
		data.ByModel[model].TotalTokens += r.TotalTokens
		data.ByModel[model].Cost += r.TotalCost

		// By provider
		provider := r.Provider
		if provider == "" {
			provider = "unknown"
		}
		if _, ok := data.ByProvider[provider]; !ok {
			data.ByProvider[provider] = &providerStats{}
		}
		data.ByProvider[provider].Requests++
		data.ByProvider[provider].TotalTokens += r.TotalTokens
		data.ByProvider[provider].Cost += r.TotalCost
		data.ByProvider[provider].Type = getRouteType(provider)

		// By user
		user := r.UserID
		if user == "" {
			user = r.IPAddress
		}
		if user == "" {
			user = "anonymous"
		}
		if _, ok := data.ByUser[user]; !ok {
			data.ByUser[user] = &userStats{}
		}
		data.ByUser[user].Requests++
		data.ByUser[user].TotalTokens += r.TotalTokens
		data.ByUser[user].Cost += r.TotalCost

		// Routing
		routeType := getRouteType(provider)
		data.Routing[routeType].Requests++
		data.Routing[routeType].Cost += r.TotalCost

		// Daily trends
		day := r.Timestamp.Format("2006-01-02")
		if _, ok := data.DailyTrends[day]; !ok {
			data.DailyTrends[day] = &dailyStats{}
		}
		data.DailyTrends[day].Requests++
		data.DailyTrends[day].TotalTokens += r.TotalTokens
		data.DailyTrends[day].Cost += r.TotalCost
	}

	// Overview
	avgCost := 0.0
	if totalRequests > 0 {
		avgCost = totalCost / float64(totalRequests)
	}
	streamingPct := 0.0
	if totalRequests > 0 {
		streamingPct = math.Round(float64(streamingCount)/float64(totalRequests)*1000) / 10
	}
	data.Overview = dashboardOverview{
		TotalRequests:     totalRequests,
		TotalTokens:       totalTokens,
		TotalInputTokens:  totalInputTokens,
		TotalOutputTokens: totalOutputTokens,
		TotalCost:         math.Round(totalCost*10000) / 10000,
		AvgCostPerReq:     math.Round(avgCost*1000000) / 1000000,
		StreamingCount:    streamingCount,
		NonStreamingCount: totalRequests - streamingCount,
		StreamingPct:      streamingPct,
	}

	// Round costs in maps
	for _, v := range data.ByModel {
		v.Cost = math.Round(v.Cost*1000000) / 1000000
	}
	for _, v := range data.ByProvider {
		v.Cost = math.Round(v.Cost*1000000) / 1000000
	}
	for _, v := range data.ByUser {
		v.Cost = math.Round(v.Cost*1000000) / 1000000
	}
	for _, v := range data.Routing {
		v.Cost = math.Round(v.Cost*1000000) / 1000000
	}

	// Top models by cost for token split
	type modelCostPair struct {
		model string
		stats *modelStats
	}
	var modelPairs []modelCostPair
	for m, s := range data.ByModel {
		modelPairs = append(modelPairs, modelCostPair{m, s})
	}
	sort.Slice(modelPairs, func(i, j int) bool {
		return modelPairs[i].stats.Cost > modelPairs[j].stats.Cost
	})
	limit := 8
	if len(modelPairs) < limit {
		limit = len(modelPairs)
	}
	for _, mp := range modelPairs[:limit] {
		data.TopModelsTokenSplit = append(data.TopModelsTokenSplit, tokenSplitEntry{
			Model:        mp.model,
			InputTokens:  mp.stats.InputTokens,
			OutputTokens: mp.stats.OutputTokens,
		})
	}

	// Recent requests (top 50)
	recentLimit := 50
	if len(records) < recentLimit {
		recentLimit = len(records)
	}
	for _, r := range records[:recentLimit] {
		userID := r.UserID
		if userID == "" {
			userID = r.IPAddress
		}
		if userID == "" {
			userID = "anonymous"
		}
		data.RecentRequests = append(data.RecentRequests, recentRequest{
			Timestamp:    r.Timestamp.Format("2006-01-02 15:04:05"),
			RequestID:    r.RequestID,
			UserID:       userID,
			Provider:     r.Provider,
			Model:        r.Model,
			Endpoint:     r.Endpoint,
			IsStreaming:   r.IsStreaming,
			InputTokens:  r.InputTokens,
			OutputTokens: r.OutputTokens,
			TotalTokens:  r.TotalTokens,
			TotalCost:    math.Round(r.TotalCost*1000000) / 1000000,
			FinishReason: r.FinishReason,
			IsEstimate:   r.IsEstimate,
		})
	}

	return data
}

func getProxyUptime() string {
	if startTime.IsZero() {
		return "N/A"
	}
	uptime := time.Since(startTime)
	hours := int(uptime.Hours())
	minutes := int(uptime.Minutes()) % 60
	if hours > 24 {
		days := hours / 24
		hours = hours % 24
		return strconv.Itoa(days) + "d " + strconv.Itoa(hours) + "h " + strconv.Itoa(minutes) + "m"
	}
	return strconv.Itoa(hours) + "h " + strconv.Itoa(minutes) + "m"
}

// Handlers

func dashboardPageHandler(w http.ResponseWriter, r *http.Request) {
	htmlBytes, err := dashboardFS.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "Dashboard template not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(htmlBytes)
}

func dashboardDataHandler(w http.ResponseWriter, r *http.Request) {
	daysParam := r.URL.Query().Get("days")
	if daysParam == "" {
		daysParam = "7"
	}

	startDate, endDate := parseDaysParameter(daysParam)

	// Check cache
	cacheKey := startDate.Format("2006-01-02") + "_" + endDate.Format("2006-01-02")
	dashboardCacheMu.RLock()
	if entry, ok := dashboardCache[cacheKey]; ok && time.Since(entry.timestamp) < dashboardCacheTTL {
		dashboardCacheMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entry.data)
		return
	}
	dashboardCacheMu.RUnlock()

	records := readCostRecords(startDate, endDate)
	data := aggregateRecords(records)
	data.Uptime = getProxyUptime()
	data.CostFile = getCostFilePath()
	data.ProxyURL = "internal"
	data.DateRange = map[string]string{
		"start": startDate.Format("2006-01-02"),
		"end":   endDate.Format("2006-01-02"),
		"days":  daysParam,
	}

	// Update cache
	dashboardCacheMu.Lock()
	dashboardCache[cacheKey] = &dashboardCacheEntry{data: data, timestamp: time.Now()}
	dashboardCacheMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func dashboardHealthHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"providers": globalProviderManager.GetHealthStatus(),
		"features": map[string]bool{
			"cost_tracking": globalCostTracker != nil,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

func dashboardRecentHandler(w http.ResponseWriter, r *http.Request) {
	daysParam := r.URL.Query().Get("days")
	if daysParam == "" {
		daysParam = "1"
	}
	startDate, endDate := parseDaysParameter(daysParam)
	records := readCostRecords(startDate, endDate)
	data := aggregateRecords(records)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"recent_requests": data.RecentRequests,
	})
}

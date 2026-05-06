package main

import (
	"embed"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Instawork/llm-proxy/internal/apikeys"
	"github.com/gorilla/mux"
)

//go:embed admin.html
var adminFS embed.FS

// adminStore returns the underlying *apikeys.Store from the global store, or nil.
func adminStore() *apikeys.Store {
	if globalAPIKeyStore == nil {
		return nil
	}
	s, _ := globalAPIKeyStore.(*apikeys.Store)
	return s
}

// adminAuthMiddleware checks the ADMIN_TOKEN env var against the ?token= query param or
// the Authorization: Bearer <token> header.
func adminAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("ADMIN_TOKEN")
		if token == "" {
			// No token configured — deny all access
			http.Error(w, `{"error":"admin panel disabled: ADMIN_TOKEN not set"}`, http.StatusForbidden)
			return
		}

		// Check query param first (?token=xxx), then Authorization header
		provided := r.URL.Query().Get("token")
		if provided == "" {
			auth := r.Header.Get("Authorization")
			provided = strings.TrimPrefix(auth, "Bearer ")
		}

		if provided != token {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// adminPageHandler serves the admin HTML page.
func adminPageHandler(w http.ResponseWriter, r *http.Request) {
	html, err := adminFS.ReadFile("admin.html")
	if err != nil {
		http.Error(w, "admin UI not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(html)
}

// adminListKeysHandler lists all API keys.
func adminListKeysHandler(w http.ResponseWriter, r *http.Request) {
	store := adminStore()
	if store == nil {
		jsonError(w, "API key management not enabled", http.StatusServiceUnavailable)
		return
	}

	keys, err := store.ListKeys(r.Context(), "")
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type keyResponse struct {
		PK             string            `json:"pk"`
		Provider       string            `json:"provider"`
		Description    string            `json:"description"`
		DailyCostLimit int64             `json:"daily_cost_limit"`
		Enabled        bool              `json:"enabled"`
		CreatedAt      time.Time         `json:"created_at"`
		UpdatedAt      time.Time         `json:"updated_at"`
		ExpiresAt      *time.Time        `json:"expires_at,omitempty"`
		Tags           map[string]string `json:"tags,omitempty"`
	}

	resp := make([]keyResponse, 0, len(keys))
	for _, k := range keys {
		resp = append(resp, keyResponse{
			PK:             k.PK,
			Provider:       k.Provider,
			Description:    k.Description,
			DailyCostLimit: k.DailyCostLimit,
			Enabled:        k.Enabled,
			CreatedAt:      k.CreatedAt,
			UpdatedAt:      k.UpdatedAt,
			ExpiresAt:      k.ExpiresAt,
			Tags:           k.Tags,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// adminCreateKeyHandler creates a new API key.
func adminCreateKeyHandler(w http.ResponseWriter, r *http.Request) {
	store := adminStore()
	if store == nil {
		jsonError(w, "API key management not enabled", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Provider       string            `json:"provider"`
		ActualKey      string            `json:"actual_key"`
		Description    string            `json:"description"`
		DailyCostLimit int64             `json:"daily_cost_limit"`
		Tags           map[string]string `json:"tags,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Provider == "" || req.ActualKey == "" {
		jsonError(w, "provider and actual_key are required", http.StatusBadRequest)
		return
	}
	if req.DailyCostLimit <= 0 {
		req.DailyCostLimit = 10000 // default $100/day
	}

	key, err := store.CreateKey(r.Context(), req.Provider, req.ActualKey, req.Description, req.DailyCostLimit, req.Tags)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pk":               key.PK,
		"provider":         key.Provider,
		"description":      key.Description,
		"daily_cost_limit": key.DailyCostLimit,
		"enabled":          key.Enabled,
		"created_at":       key.CreatedAt,
	})
}

// adminUpdateKeyHandler enables/disables a key or updates its description/cost limit.
func adminUpdateKeyHandler(w http.ResponseWriter, r *http.Request) {
	store := adminStore()
	if store == nil {
		jsonError(w, "API key management not enabled", http.StatusServiceUnavailable)
		return
	}

	keyID := mux.Vars(r)["key"]
	if keyID == "" {
		jsonError(w, "key ID required", http.StatusBadRequest)
		return
	}

	var req struct {
		Enabled        *bool             `json:"enabled,omitempty"`
		Description    *string           `json:"description,omitempty"`
		DailyCostLimit *int64            `json:"daily_cost_limit,omitempty"`
		Tags           map[string]string `json:"tags,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	updates := map[string]interface{}{}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.DailyCostLimit != nil {
		updates["daily_cost_limit"] = *req.DailyCostLimit
	}
	if req.Tags != nil {
		updates["tags"] = req.Tags
	}

	if len(updates) == 0 {
		jsonError(w, "no fields to update", http.StatusBadRequest)
		return
	}

	if err := store.UpdateKey(r.Context(), keyID, updates); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// adminDeleteKeyHandler deletes a key.
func adminDeleteKeyHandler(w http.ResponseWriter, r *http.Request) {
	store := adminStore()
	if store == nil {
		jsonError(w, "API key management not enabled", http.StatusServiceUnavailable)
		return
	}

	keyID := mux.Vars(r)["key"]
	if keyID == "" {
		jsonError(w, "key ID required", http.StatusBadRequest)
		return
	}

	if err := store.DeleteKey(r.Context(), keyID); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

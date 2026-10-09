package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"english-teacher-app/internal/db"
)

var defaultCandidateModels = []string{
	"models/gemini-3.8-live",
	"models/gemini-3.8-flash",
	"models/gemini-3.5-flash",
	"models/gemini-flash-latest",
}

type ModelResolver struct {
	db             *db.DB
	envModel       string
	client         *http.Client
	discoveredMu   sync.RWMutex
	discovered     []string
	lastDiscovered time.Time
}

func NewModelResolver(database *db.DB, envModel string) *ModelResolver {
	return &ModelResolver{
		db:       database,
		envModel: envModel,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// DiscoverModels queries Google Gemini REST API to dynamically find models supporting bidiGenerateContent
func (r *ModelResolver) DiscoverModels(ctx context.Context, apiKey string) ([]string, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("gemini api key is empty")
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query gemini models API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini models API returned status %d", resp.StatusCode)
	}

	var listResp ModelListResponse
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return nil, fmt.Errorf("failed to decode models API response: %w", err)
	}

	var bidiModels []string
	for _, m := range listResp.Models {
		for _, method := range m.SupportedGenerationMethods {
			if method == "bidiGenerateContent" {
				bidiModels = append(bidiModels, m.Name)
				break
			}
		}
	}

	// Sort / Rank: Flash & Realtime 2.0+ first
	bidiModels = rankModels(bidiModels)

	r.discoveredMu.Lock()
	r.discovered = bidiModels
	r.lastDiscovered = time.Now()
	r.discoveredMu.Unlock()

	log.Printf("[ModelResolver] Successfully discovered %d bidiGenerateContent models: %v", len(bidiModels), bidiModels)
	return bidiModels, nil
}

// rankModels orders models with highest priority first
func rankModels(models []string) []string {
	var prioritized []string
	var others []string

	for _, m := range models {
		lower := strings.ToLower(m)
		if strings.Contains(lower, "3.8-live") || strings.Contains(lower, "live") {
			prioritized = append([]string{m}, prioritized...)
		} else if strings.Contains(lower, "3.8-flash") || strings.Contains(lower, "3.7-flash") || strings.Contains(lower, "3.5-flash") || strings.Contains(lower, "flash-latest") {
			prioritized = append(prioritized, m)
		} else {
			others = append(others, m)
		}
	}

	return append(prioritized, others...)
}

// GetCandidateModels returns the candidate models in priority order
func (r *ModelResolver) GetCandidateModels(apiKey string) []string {
	var candidates []string
	seen := make(map[string]bool)

	add := func(m string) {
		m = strings.TrimSpace(m)
		if m == "" || m == "auto" {
			return
		}
		if !strings.HasPrefix(m, "models/") {
			m = "models/" + m
		}
		if !seen[m] {
			seen[m] = true
			candidates = append(candidates, m)
		}
	}

	// 1. Explicit override from app_settings
	if r.db != nil {
		if setting, err := r.db.GetSetting("default_live_model"); err == nil && setting != "" && setting != "auto" {
			add(setting)
		}
	}

	// 2. Explicit override from ENV
	if r.envModel != "" && r.envModel != "auto" {
		add(r.envModel)
	}

	// 3. Discovered models from API
	r.discoveredMu.RLock()
	disc := r.discovered
	r.discoveredMu.RUnlock()

	for _, m := range disc {
		add(m)
	}

	// 4. Default fallback list
	for _, m := range defaultCandidateModels {
		add(m)
	}

	return candidates
}

// GetEffectiveModel returns the highest priority model
func (r *ModelResolver) GetEffectiveModel(apiKey string) string {
	candidates := r.GetCandidateModels(apiKey)
	if len(candidates) > 0 {
		return candidates[0]
	}
	return "models/gemini-3.8-live"
}

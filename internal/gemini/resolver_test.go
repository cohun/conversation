package gemini

import (
	"testing"
)

func TestModelResolverCandidateOrdering(t *testing.T) {
	resolver := NewModelResolver(nil, "auto")

	candidates := resolver.GetCandidateModels("test-key")
	if len(candidates) == 0 {
		t.Fatalf("Expected candidate models, got none")
	}

	// First default candidate should be models/gemini-3.8-live
	if candidates[0] != "models/gemini-3.8-live" {
		t.Errorf("Expected first candidate models/gemini-3.8-live, got %s", candidates[0])
	}

	// Test explicit ENV override
	resolverWithEnv := NewModelResolver(nil, "gemini-custom-model")
	envCandidates := resolverWithEnv.GetCandidateModels("test-key")
	if len(envCandidates) == 0 || envCandidates[0] != "models/gemini-custom-model" {
		t.Errorf("Expected explicit env model to be first candidate, got %v", envCandidates)
	}
}

func TestRankModels(t *testing.T) {
	raw := []string{
		"models/text-embedding-004",
		"models/gemini-1.5-pro",
		"models/gemini-3.8-flash",
		"models/gemini-3.8-live",
	}

	ranked := rankModels(raw)
	if len(ranked) != len(raw) {
		t.Fatalf("Ranked length mismatch: expected %d, got %d", len(raw), len(ranked))
	}

	// 3.8-live should be at the front
	if ranked[0] != "models/gemini-3.8-live" {
		t.Errorf("Expected gemini-3.8-live to be first, got %s", ranked[0])
	}
}

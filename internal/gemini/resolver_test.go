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

	// First default candidate should be 2.0-flash-exp
	if candidates[0] != "models/gemini-2.0-flash-exp" {
		t.Errorf("Expected first candidate models/gemini-2.0-flash-exp, got %s", candidates[0])
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
		"models/gemini-2.0-flash",
		"models/gemini-2.0-flash-realtime-exp",
	}

	ranked := rankModels(raw)
	if len(ranked) != len(raw) {
		t.Fatalf("Ranked length mismatch: expected %d, got %d", len(raw), len(ranked))
	}

	// 2.0-flash and realtime should be at the front
	if ranked[0] != "models/gemini-2.0-flash" && ranked[0] != "models/gemini-2.0-flash-realtime-exp" {
		t.Errorf("Expected flash/realtime to be prioritized, got %s", ranked[0])
	}
}

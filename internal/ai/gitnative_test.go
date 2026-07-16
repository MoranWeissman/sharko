package ai

import "testing"

// TestMergeGitNativeFromEnv_NonSecretWins verifies git-declared non-secret AI
// fields override the persisted (UI-set) blob.
func TestMergeGitNativeFromEnv_NonSecretWins(t *testing.T) {
	t.Setenv(envAIProvider, "openai")
	t.Setenv(envAICloudModel, "gpt-4o")
	t.Setenv(envAIMaxIter, "12")

	persisted := Config{Provider: ProviderClaude, CloudModel: "claude-old", MaxIterations: 8}
	merged, changed := MergeGitNativeFromEnv(persisted)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if merged.Provider != ProviderOpenAI {
		t.Errorf("provider: git should win, got %q", merged.Provider)
	}
	if merged.CloudModel != "gpt-4o" {
		t.Errorf("cloud model: git should win, got %q", merged.CloudModel)
	}
	if merged.MaxIterations != 12 {
		t.Errorf("max iterations: git should win, got %d", merged.MaxIterations)
	}
}

// TestMergeGitNativeFromEnv_PreservesAPIKey is the security gate: the API key
// is secret material and must be preserved untouched (never read from a
// non-secret env var).
func TestMergeGitNativeFromEnv_PreservesAPIKey(t *testing.T) {
	t.Setenv(envAIProvider, "openai")
	t.Setenv(envAICloudModel, "gpt-4o")

	persisted := Config{Provider: ProviderClaude, APIKey: "sk-supersecret", CloudModel: "claude-old"}
	merged, _ := MergeGitNativeFromEnv(persisted)
	if merged.APIKey != "sk-supersecret" {
		t.Errorf("API key must be preserved, got %q", merged.APIKey)
	}
}

// TestMergeGitNativeFromEnv_UndeclaredPreserved verifies UI-only fields not
// declared in env are preserved (back-compat).
func TestMergeGitNativeFromEnv_UndeclaredPreserved(t *testing.T) {
	// Only provider declared; everything else persists.
	t.Setenv(envAIProvider, "openai")

	persisted := Config{
		Provider:          ProviderClaude,
		CloudModel:        "keep-model",
		AnnotateOnSeed:    true,
		AnnotateOnSeedSet: true,
		MaxIterations:     5,
	}
	merged, _ := MergeGitNativeFromEnv(persisted)
	if merged.CloudModel != "keep-model" {
		t.Errorf("undeclared cloud model must persist, got %q", merged.CloudModel)
	}
	if !merged.AnnotateOnSeed || !merged.AnnotateOnSeedSet {
		t.Error("undeclared UI toggle (AnnotateOnSeed) must persist")
	}
	if merged.MaxIterations != 5 {
		t.Errorf("undeclared max iterations must persist, got %d", merged.MaxIterations)
	}
}

// TestMergeGitNativeFromEnv_NoDeclaredFields verifies a pristine env reports no
// change and leaves the config intact.
func TestMergeGitNativeFromEnv_NoDeclaredFields(t *testing.T) {
	persisted := Config{Provider: ProviderClaude, CloudModel: "m", MaxIterations: 8}
	merged, changed := MergeGitNativeFromEnv(persisted)
	if changed {
		t.Error("expected changed=false when nothing declared")
	}
	if merged != persisted {
		t.Error("config should be unchanged")
	}
}

// TestMergeGitNativeFromEnv_LenientMaxIter verifies a malformed AI_MAX_ITERATIONS
// is treated as undeclared (warn + keep runtime value), never crashing.
func TestMergeGitNativeFromEnv_LenientMaxIter(t *testing.T) {
	t.Setenv(envAIMaxIter, "not-a-number")
	persisted := Config{Provider: ProviderClaude, MaxIterations: 8}
	merged, changed := MergeGitNativeFromEnv(persisted)
	if changed {
		t.Error("malformed max iterations should be treated as undeclared (no change)")
	}
	if merged.MaxIterations != 8 {
		t.Errorf("max iterations should keep runtime value, got %d", merged.MaxIterations)
	}
}

// TestMergeGitNativeFromEnv_Idempotent verifies re-applying an already-merged
// value reports no change.
func TestMergeGitNativeFromEnv_Idempotent(t *testing.T) {
	t.Setenv(envAIProvider, "openai")
	t.Setenv(envAICloudModel, "gpt-4o")
	persisted := Config{Provider: ProviderOpenAI, CloudModel: "gpt-4o"}
	_, changed := MergeGitNativeFromEnv(persisted)
	if changed {
		t.Error("expected changed=false when already converged")
	}
}

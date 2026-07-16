package ai

import (
	"log/slog"
	"os"
	"strconv"
)

// V3 C2 (C2a): AI provider config is git-native for its NON-SECRET fields.
//
// The persisted AI config lives as ONE encrypted JSON blob in the
// sharko-ai-config Secret; the secret material is the API key (Config.APIKey).
// Like the connection merge (internal/config/connection_gitnative.go), a
// Helm/git-declared env var is authoritative (git wins) for the non-secret
// field it declares, while the encrypted API key is PRESERVED. Fields that are
// NOT env-declared keep the persisted (UI-set) value — so a user's
// "annotate on generate" toggle, or a provider chosen entirely in the UI, is
// left untouched (back-compat, exactly like C1's undeclared-key behavior).
//
// The API key is NEVER read from these non-secret env vars and NEVER written
// into a ConfigMap or values.yaml plaintext. It is sourced only from the chart
// Secret (AI_API_KEY, via envFrom secretRef) at boot, or entered via the UI
// and stored encrypted — both paths land in Config.APIKey, which this merge
// preserves.
//
// Env keys mirror the boot-time reads in cmd/sharko/serve.go. AI_API_KEY is
// deliberately absent here — it is the secret path and is resolved separately.
const (
	envAIProvider    = "AI_PROVIDER"
	envAICloudModel  = "AI_CLOUD_MODEL"
	envAIBaseURL     = "AI_BASE_URL"
	envAIAuthHeader  = "AI_AUTH_HEADER"
	envAIMaxIter     = "AI_MAX_ITERATIONS"
	envAIOllamaURL   = "AI_OLLAMA_URL"
	envAIOllamaModel = "AI_OLLAMA_MODEL"
	envAIAgentModel  = "AI_AGENT_MODEL"
)

// aiSetString overwrites *dst with the env value when the env var is set and
// the value differs. An unset env var leaves the persisted value in place.
func aiSetString(dst *string, env string, changed *bool) {
	if raw := os.Getenv(env); raw != "" && *dst != raw {
		*dst = raw
		*changed = true
	}
}

// MergeGitNativeFromEnv overlays the git-declared NON-SECRET AI fields from env
// onto cfg (git wins), PRESERVING the encrypted secret material (cfg.APIKey)
// and any field the env does not declare (e.g. UI-set toggles). Returns the
// merged config and whether anything changed.
//
// Lenient: a malformed AI_MAX_ITERATIONS (non-integer) is warned and treated
// as undeclared — never crashes boot on a typo.
//
// SECURITY: cfg.APIKey is neither read from env nor modified here; it is
// carried through untouched so the re-persist round-trips the encrypted key.
func MergeGitNativeFromEnv(cfg Config) (Config, bool) {
	changed := false

	if raw := os.Getenv(envAIProvider); raw != "" {
		if string(cfg.Provider) != raw {
			cfg.Provider = Provider(raw)
			changed = true
		}
	}
	aiSetString(&cfg.CloudModel, envAICloudModel, &changed)
	aiSetString(&cfg.BaseURL, envAIBaseURL, &changed)
	aiSetString(&cfg.AuthHeader, envAIAuthHeader, &changed)
	aiSetString(&cfg.OllamaURL, envAIOllamaURL, &changed)
	aiSetString(&cfg.OllamaModel, envAIOllamaModel, &changed)
	aiSetString(&cfg.AgentModel, envAIAgentModel, &changed)

	if raw := os.Getenv(envAIMaxIter); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			slog.Warn("malformed AI_MAX_ITERATIONS, treating as undeclared",
				"value", raw)
		} else if cfg.MaxIterations != n {
			cfg.MaxIterations = n
			changed = true
		}
	}

	return cfg, changed
}

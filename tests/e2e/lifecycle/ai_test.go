//go:build e2e

// Package lifecycle holds end-to-end tests that drive Sharko HTTP endpoints
// against the in-process harness from tests/e2e/harness. Each *_test.go file
// owns one functional area; this file (V2 Epic 7-1.11) covers the AI /
// agent / providers surface — 11 endpoints split across two top-level
// scenarios (config-only vs. real-AI invocation).
//
// Gating strategy:
//
//   - TestAIConfig runs unconditionally. It exercises the configuration
//     surface (GET/POST /ai/config, POST /ai/provider, POST /ai/test-config,
//     GET /providers, POST /providers/test-config) without ever invoking a
//     real LLM. The handlers either persist input or call a mock-friendly
//     code path that returns 200 with status:error on bad config — neither
//     path needs an API key.
//
//   - TestAIInvocation is skipped unless E2E_AI_API_KEY is set in the
//     environment. It exercises the invocation surface (POST /ai/test,
//     POST /agent/chat, POST /agent/reset, GET /upgrade/ai-status,
//     POST /upgrade/ai-summary, POST /providers/test) which all require
//     either a working LLM or an active ArgoCD connection. The skip is
//     informative so CI logs make the gap obvious.
//
// File-isolation: tests/e2e/lifecycle/ai_test.go +
// tests/e2e/harness/apiclient_ai.go are the only files this story owns.
// Any additional shared harness changes belong to a separate story so the
// merge stays a clean fast-forward.
package lifecycle

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestAIConfig drives the configuration surface end-to-end. None of these
// endpoints invoke a real LLM, so the test runs unconditionally.
//
// Sub-test ordering matters: SetAIProvider mutates server state that
// PostAIConfig overwrites and that subsequent reads observe. Each sub-test
// re-asserts the post-state so failures are self-contained.
func TestAIConfig(t *testing.T) {
	git := harness.StartGitFake(t)
	mock := harness.StartGitMock(t)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     git,
		GitProvider: mock,
		// AIDisabled stays at the default — the in-process harness wires
		// an empty ai.Config{} regardless, so IsEnabled() is false until
		// we POST /ai/config below.
	})
	sharko.WaitHealthy(t, 10*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())

	admin := harness.NewClient(t, sharko)

	t.Run("GetAIConfig", func(t *testing.T) {
		// Baseline: in-process boot wires ai.Config{} so Provider is
		// the zero value (""). The handler still emits the four known
		// available providers so the Settings UI can render the radio
		// list before any save has happened.
		got := admin.GetAIConfig(t)
		// Empty string or "none" — the handler echoes whatever is on
		// ai.Config.Provider. Accept both rather than over-specifying.
		if got.CurrentProvider != "" && got.CurrentProvider != "none" {
			t.Errorf("baseline current_provider: got %q want \"\" or \"none\"", got.CurrentProvider)
		}
		// Should expose at least ollama/claude/openai/gemini ids.
		want := map[string]bool{"ollama": false, "claude": false, "openai": false, "gemini": false}
		for _, p := range got.AvailableProviders {
			if _, ok := want[p.ID]; ok {
				want[p.ID] = true
			}
		}
		for id, seen := range want {
			if !seen {
				t.Errorf("available_providers missing %q (got=%v)", id, got.AvailableProviders)
			}
		}
		// AnnotateOnSeed is false when no provider configured.
		if got.AnnotateOnSeed {
			t.Errorf("annotate_on_seed: got true on unconfigured AI; want false")
		}
	})

	t.Run("SetAIProvider", func(t *testing.T) {
		// Switch to "openai" — handler validates the string is a known
		// provider and updates the active provider only (no API key
		// required, since this is just a mode switch).
		ack := admin.SetAIProvider(t, "openai")
		if status, _ := ack["status"].(string); status != "ok" {
			t.Errorf("SetAIProvider ack: status=%v want \"ok\" (full=%v)", ack["status"], ack)
		}
		if prov, _ := ack["provider"].(string); prov != "openai" {
			t.Errorf("SetAIProvider ack: provider=%v want \"openai\"", ack["provider"])
		}

		// Verify GetAIConfig reflects the new provider.
		got := admin.GetAIConfig(t)
		if got.CurrentProvider != "openai" {
			t.Errorf("after SetAIProvider: current_provider=%q want \"openai\"", got.CurrentProvider)
		}
	})

	t.Run("PostAIConfig", func(t *testing.T) {
		// Full config save. We use a dummy api_key so the handler
		// persists a complete record without us actually touching the
		// network. The model is a valid OpenAI model name so the
		// available_providers row reports it correctly on read-back.
		ack := admin.SaveAIConfig(t, harness.SaveAIConfigRequest{
			Provider: "openai",
			APIKey:   "dummy-key-for-e2e-config-test",
			Model:    "gpt-4o-mini",
		})
		if status, _ := ack["status"].(string); status != "ok" {
			t.Errorf("SaveAIConfig ack: status=%v want \"ok\" (full=%v)", ack["status"], ack)
		}

		got := admin.GetAIConfig(t)
		if got.CurrentProvider != "openai" {
			t.Errorf("after PostAIConfig: current_provider=%q want \"openai\"", got.CurrentProvider)
		}
		// Find the openai entry — should be marked configured.
		var openai harness.AIProviderInfo
		for _, p := range got.AvailableProviders {
			if p.ID == "openai" {
				openai = p
				break
			}
		}
		if openai.ID != "openai" {
			t.Fatalf("after PostAIConfig: openai entry missing in available_providers (%v)", got.AvailableProviders)
		}
		if !openai.Configured {
			t.Errorf("after PostAIConfig: openai.configured=false want true (entry=%+v)", openai)
		}
		if openai.Model != "gpt-4o-mini" {
			t.Errorf("after PostAIConfig: openai.model=%q want \"gpt-4o-mini\"", openai.Model)
		}
		// AnnotateOnSeed default-true once an enabled provider is saved
		// without an explicit annotate_on_seed value (V121-7.3 contract).
		if !got.AnnotateOnSeed {
			t.Errorf("after PostAIConfig: annotate_on_seed=false want true (default-on)")
		}
	})

	t.Run("TestConfigValidate", func(t *testing.T) {
		// handleTestAIConfig builds a temporary ai.Client and calls
		// Summarize. Two soft-failure paths to exercise:
		//
		//  1. Unsupported provider — Summarize returns
		//     "unsupported AI provider" → 200 with status:error.
		//  2. Valid provider but bogus API key — Summarize hits the
		//     network; we cannot rely on offline runners reaching the
		//     OpenAI endpoint, so we accept either status (the contract
		//     is "200 with status field present", not "always errors").
		bad := admin.TestAIConfig(t, harness.SaveAIConfigRequest{
			Provider: "definitely-not-a-real-provider",
		})
		if status, _ := bad["status"].(string); status != "error" {
			t.Errorf("test-config (bad provider): status=%v want \"error\" (full=%v)", bad["status"], bad)
		}
		if msg, _ := bad["message"].(string); msg == "" {
			t.Errorf("test-config (bad provider): empty message field (full=%v)", bad)
		}

		// Valid provider, bogus key — handler should still return 200
		// (status may be ok or error depending on whether the runner
		// has outbound network); only assert structure.
		mixed := admin.TestAIConfig(t, harness.SaveAIConfigRequest{
			Provider: "openai",
			APIKey:   "sk-not-a-real-key-just-for-shape-test",
			Model:    "gpt-4o-mini",
		})
		if _, ok := mixed["status"]; !ok {
			t.Errorf("test-config (bogus key): missing \"status\" field (full=%v)", mixed)
		}
	})

	t.Run("ListProviders", func(t *testing.T) {
		// In-process boot does not configure a credentials provider, so
		// configured_provider is null and available_types lists the
		// known credential backends (aws-sm / k8s-secrets).
		got := admin.ListProviders(t)
		if got.ConfiguredProvider != nil {
			t.Errorf("ListProviders: configured_provider=%v want nil", got.ConfiguredProvider)
		}
		// available_types is a fixed list — assert both known entries.
		want := map[string]bool{"aws-sm": false, "k8s-secrets": false}
		for _, typ := range got.AvailableTypes {
			if _, ok := want[typ]; ok {
				want[typ] = true
			}
		}
		for typ, seen := range want {
			if !seen {
				t.Errorf("ListProviders: available_types missing %q (got=%v)", typ, got.AvailableTypes)
			}
		}
	})

	t.Run("ProvidersTestConfig", func(t *testing.T) {
		// handleTestProviderConfig validates Type via providers.New —
		// unknown values return "unknown provider type" inside a 200
		// response (status:error). The handler does not 4xx for bad
		// input; it always 200s with a structured body.
		bad := admin.TestProviderConfig(t, harness.ProviderTestRequest{
			Type: "definitely-not-a-real-type",
		})
		if status, _ := bad["status"].(string); status != "error" {
			t.Errorf("providers/test-config (bad type): status=%v want \"error\" (full=%v)", bad["status"], bad)
		}
		if msg, _ := bad["message"].(string); !strings.Contains(strings.ToLower(msg), "unknown provider") &&
			!strings.Contains(strings.ToLower(msg), "no secrets") {
			// We accept either the "unknown provider type" or
			// "no secrets provider" wording — the handler emits
			// whichever providers.New returned.
			t.Errorf("providers/test-config (bad type): message=%q lacks expected prefix (full=%v)", msg, bad)
		}

		// k8s-secrets requires in-cluster config — outside K8s the
		// constructor returns an error which the handler reports as
		// status:error. Either way the response is well-formed JSON
		// with a "status" field.
		empty := admin.TestProviderConfig(t, harness.ProviderTestRequest{})
		if _, ok := empty["status"]; !ok {
			t.Errorf("providers/test-config (empty): missing \"status\" field (full=%v)", empty)
		}
	})
}

// TestAIInvocation drives the endpoints that require a working LLM (and,
// for /agent/chat, an active ArgoCD connection). Skips cleanly when
// E2E_AI_API_KEY is absent — most CI runs fall into this skip path, which
// is exactly the design intent: the configuration surface is exercised by
// TestAIConfig above, and this scenario is the optional "real backend"
// confidence layer for local + nightly runs.
//
// Note: even with E2E_AI_API_KEY set, /agent/chat will still skip with a
// 503 in the in-process harness because GetActiveArgocdClient requires a
// real connection. That's a known harness gap (no fake ArgoCD client),
// noted in the dispatch report.
func TestAIInvocation(t *testing.T) {
	apiKey := os.Getenv("E2E_AI_API_KEY")
	if apiKey == "" {
		t.Skip("E2E_AI_API_KEY not set — skipping live AI invocation tests " +
			"(set E2E_AI_API_KEY=<openai-or-anthropic-key> + optional " +
			"E2E_AI_PROVIDER=openai|claude + E2E_AI_MODEL to enable)")
	}
	provider := os.Getenv("E2E_AI_PROVIDER")
	if provider == "" {
		provider = "openai"
	}
	model := os.Getenv("E2E_AI_MODEL")
	if model == "" {
		switch provider {
		case "openai":
			model = "gpt-4o-mini"
		case "claude":
			model = "claude-3-5-haiku-20241022"
		case "gemini":
			model = "gemini-2.5-flash"
		}
	}

	git := harness.StartGitFake(t)
	mock := harness.StartGitMock(t)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     git,
		GitProvider: mock,
	})
	sharko.WaitHealthy(t, 10*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())

	admin := harness.NewClient(t, sharko)

	// Configure AI with the live key + provider so subsequent calls hit
	// a real backend. This is a single POST — the previous TestAIConfig
	// scenario already verifies the persistence path.
	ack := admin.SaveAIConfig(t, harness.SaveAIConfigRequest{
		Provider: provider,
		APIKey:   apiKey,
		Model:    model,
	})
	if status, _ := ack["status"].(string); status != "ok" {
		t.Fatalf("SaveAIConfig (live key): status=%v want \"ok\" (full=%v)", ack["status"], ack)
	}

	t.Run("TestProvider", func(t *testing.T) {
		// POST /ai/test — should hit the live endpoint and return 200
		// with {status:ok, response:string}. Generous timeout to absorb
		// LLM latency.
		resp := admin.TestAIConnection(t, harness.WithTimeout(60*time.Second))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("/ai/test: status=%d want 200; body=%s", resp.StatusCode, body)
		}
		var got map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("/ai/test: decode body: %v", err)
		}
		if status, _ := got["status"].(string); status != "ok" {
			t.Errorf("/ai/test: status=%v want \"ok\" (full=%v)", got["status"], got)
		}
		if response, _ := got["response"].(string); response == "" {
			t.Errorf("/ai/test: empty response (full=%v)", got)
		}
	})

	t.Run("AgentChat", func(t *testing.T) {
		// /agent/chat needs an active ArgoCD client — the in-process
		// harness doesn't seed one, so this typically returns 503.
		// Skip cleanly so the rest of the scenario continues; once the
		// harness gains a fake ArgoCD client (future story) this can
		// be promoted to a hard assertion.
		resp := admin.AgentChat(t, harness.AgentChatRequest{
			Message: "Say hello in one short sentence.",
		}, harness.WithTimeout(60*time.Second))
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusServiceUnavailable {
			body, _ := io.ReadAll(resp.Body)
			t.Skipf("/agent/chat: 503 (no active connection / argocd) — harness gap; body=%s", body)
		}
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("/agent/chat: status=%d want 200 or 503; body=%s", resp.StatusCode, body)
		}
		var got harness.AgentChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatalf("/agent/chat: decode: %v", err)
		}
		if got.SessionID == "" {
			t.Errorf("/agent/chat: empty session_id (full=%+v)", got)
		}
		if got.Response == "" {
			t.Errorf("/agent/chat: empty response (full=%+v)", got)
		}
	})

	t.Run("AgentReset", func(t *testing.T) {
		// Reset is idempotent and does not require AI — even an
		// unknown session id returns 200 (handler is a silent no-op).
		ack := admin.AgentReset(t, "session-that-never-existed")
		if status, _ := ack["status"].(string); status != "reset" {
			t.Errorf("/agent/reset: status=%v want \"reset\" (full=%v)", ack["status"], ack)
		}
	})

	t.Run("UpgradeAIStatus", func(t *testing.T) {
		// /upgrade/ai-status reflects aiClient.IsEnabled() — after the
		// SaveAIConfig above this should be true.
		got := admin.UpgradeAIStatus(t)
		if !got.Enabled {
			t.Errorf("/upgrade/ai-status: enabled=false want true (config saved with provider=%s)", provider)
		}
	})

	t.Run("UpgradeAISummary", func(t *testing.T) {
		// /upgrade/ai-summary first calls upgradeSvc.CheckUpgrade,
		// which fetches addon metadata via the git provider. Without a
		// real catalog this returns 500. We still POST to confirm the
		// route exists and decodes the body — soft-skip on the 500
		// path so the test reports a useful diagnostic instead of
		// failing on a known harness gap.
		resp := admin.UpgradeAISummary(t, harness.UpgradeAISummaryRequest{
			AddonName:     "argo-cd",
			TargetVersion: "1.0.0",
		}, harness.WithTimeout(60*time.Second))
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		switch resp.StatusCode {
		case http.StatusOK:
			// Happy path — verify the body has a "summary" field.
			var got map[string]string
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("/upgrade/ai-summary: decode: %v; body=%s", err, body)
			}
			if got["summary"] == "" {
				t.Errorf("/upgrade/ai-summary: empty summary (body=%s)", body)
			}
		case http.StatusInternalServerError, http.StatusServiceUnavailable:
			t.Skipf("/upgrade/ai-summary: %d — addon catalog not seeded in in-process harness; body=%s",
				resp.StatusCode, body)
		default:
			t.Fatalf("/upgrade/ai-summary: status=%d want 200 / 500 / 503; body=%s",
				resp.StatusCode, body)
		}
	})

	t.Run("ProvidersTest", func(t *testing.T) {
		// /providers/test with no body and no configured provider →
		// 501 (handler returns NotImplemented). The behaviour is
		// independent of AI; we exercise it here so the live-AI
		// scenario covers the full provider surface.
		//
		// Use Client.Do (raw response) and assert status manually —
		// the typed Do helper does not enforce expectStatus, that's
		// done by the JSON helpers (which 401-retry-once on 401).
		resp := admin.TestProvider(t, harness.ProviderTestRequest{})
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("/providers/test: status=%d want 501; body=%s", resp.StatusCode, body)
		}
	})
}

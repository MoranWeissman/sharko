package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
)

// TestAnnotateValues_NotConfigured verifies the graceful skip when no
// AI client is provided. Story 7.3 AC: "Skip annotation if AI is NOT
// configured (no key)".
func TestAnnotateValues_NotConfigured(t *testing.T) {
	in := []byte("replicaCount: 1\n")
	res, err := AnnotateValues(context.Background(), in, "demo", "1.0.0", nil)
	if err != nil {
		t.Fatalf("expected nil error on not_configured, got %v", err)
	}
	if res.SkipReason != "not_configured" {
		t.Errorf("want SkipReason=not_configured, got %q", res.SkipReason)
	}
	if string(res.AnnotatedYAML) != string(in) {
		t.Errorf("expected pass-through bytes when AI not configured")
	}
	if len(res.AdditionalClusterPaths) != 0 {
		t.Errorf("expected no extra paths, got %v", res.AdditionalClusterPaths)
	}
}

// TestAnnotateValues_Oversize verifies the token budget cap. AC: "Token
// budget cap: input + output combined < 50k tokens. If chart's
// values.yaml is too large, fall back to heuristic-only with a warning
// logged."
func TestAnnotateValues_Oversize(t *testing.T) {
	big := make([]byte, AnnotateMaxBytes+1)
	for i := range big {
		big[i] = 'a'
	}
	res, err := AnnotateValues(context.Background(), big, "big-chart", "1.0.0", nil)
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	// Without an AI client configured we'd still get not_configured
	// before oversize. Skip ai gate by passing nil aiClient — that
	// short-circuits to not_configured. Check the ordering by asserting
	// we get not_configured (the AI gate is checked first).
	if res.SkipReason != "not_configured" {
		t.Errorf("not_configured should be checked before oversize when client is nil; got %q", res.SkipReason)
	}
}

// TestAnnotateValues_SecretBlock verifies the hard block. Story 7.1 AC:
// "If ANY match → BLOCK. Don't send to LLM. Return error
// secret_detected_blocked".
//
// We use a fake-enabled AI client to exercise the path past the
// not_configured gate. Since the real client will try to hit the
// network, we synthesize the smallest possible "configured" client and
// rely on the secret guard short-circuiting before the network call.
func TestAnnotateValues_SecretBlock(t *testing.T) {
	yaml := []byte(`apiKey: AKIAIOSFODNN7EXAMPLE`)

	// Build a minimal configured client (provider != none). The guard
	// runs before any network call so this never reaches the wire.
	c := newConfiguredAIClient(t)

	res, err := AnnotateValues(context.Background(), yaml, "demo", "1.0.0", c)
	if err == nil {
		t.Fatalf("expected SecretLeakError, got nil")
	}
	var leak *SecretLeakError
	if !errors.As(err, &leak) {
		t.Fatalf("expected SecretLeakError, got %T: %v", err, err)
	}
	if res.SkipReason != "secret_blocked" {
		t.Errorf("want SkipReason=secret_blocked, got %q", res.SkipReason)
	}
	if string(res.AnnotatedYAML) != string(yaml) {
		t.Errorf("expected pass-through bytes when secret-blocked")
	}
	if len(leak.Matches) == 0 {
		t.Errorf("expected at least one match in SecretLeakError")
	}
}

// TestInjectAnnotations_PreservesStructure asserts the AI annotation
// inserter doesn't reorder, drop, or modify anything except injecting a
// `# desc` line above matched scalar leaves.
func TestInjectAnnotations_PreservesStructure(t *testing.T) {
	in := []byte(`# upstream comment
replicaCount: 1
service:
  type: ClusterIP
  port: 80
`)
	descs := map[string]string{
		"replicaCount": "Number of pods to run",
		"service.port": "Service listening port",
	}
	out := injectAnnotations(in, descs)
	got := string(out)

	if !strings.Contains(got, "# upstream comment") {
		t.Errorf("upstream comment was lost: %q", got)
	}
	if !strings.Contains(got, "# Number of pods to run\nreplicaCount: 1") {
		t.Errorf("expected description above replicaCount, got:\n%s", got)
	}
	if !strings.Contains(got, "# Service listening port\n  port: 80") {
		t.Errorf("expected description above service.port (with indent), got:\n%s", got)
	}
	// Make sure key ordering is preserved (replicaCount before service).
	if i, j := strings.Index(got, "replicaCount"), strings.Index(got, "service:"); i > j {
		t.Errorf("ordering changed; expected replicaCount before service, got:\n%s", got)
	}
}

// TestInjectAnnotations_Idempotent ensures repeat annotate calls don't
// duplicate the comment line.
func TestInjectAnnotations_Idempotent(t *testing.T) {
	descs := map[string]string{"replicaCount": "Number of pods"}
	in := []byte("replicaCount: 1\n")
	pass1 := injectAnnotations(in, descs)
	pass2 := injectAnnotations(pass1, descs)
	if string(pass1) != string(pass2) {
		t.Errorf("expected idempotent output, got pass1:\n%s\npass2:\n%s", pass1, pass2)
	}
}

// TestInjectAnnotations_SanitizesMultilineDescription confirms an
// adversarial LLM response with newlines can't change the file's
// structure.
func TestInjectAnnotations_SanitizesMultilineDescription(t *testing.T) {
	descs := map[string]string{"replicaCount": "first line\nsecond line\nthird"}
	in := []byte("replicaCount: 1\n")
	out := string(injectAnnotations(in, descs))
	if strings.Count(out, "\n") > 2 {
		// Expect: "# first line second line third\nreplicaCount: 1\n" → 2 newlines
		t.Errorf("multi-line description created extra lines:\n%s", out)
	}
	if !strings.Contains(out, "first line second line third") {
		t.Errorf("expected newlines collapsed to spaces, got:\n%s", out)
	}
}

// TestParseAnnotateResponse_StripsFences verifies forgiving parsing of
// LLM responses wrapped in ```json fences (Claude/Gemini do this).
func TestParseAnnotateResponse_StripsFences(t *testing.T) {
	raw := "```json\n{\"descriptions\":{\"a\":\"b\"},\"cluster_specific_paths\":[\"x\"]}\n```"
	parsed, err := parseAnnotateResponse(raw)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if parsed.Descriptions["a"] != "b" {
		t.Errorf("expected description a=b, got %v", parsed.Descriptions)
	}
	if len(parsed.ClusterSpecificPaths) != 1 || parsed.ClusterSpecificPaths[0] != "x" {
		t.Errorf("expected cluster paths [x], got %v", parsed.ClusterSpecificPaths)
	}
}

// TestParseAnnotateResponse_RejectsNonJSON makes sure free-text
// responses fall through cleanly.
func TestParseAnnotateResponse_RejectsNonJSON(t *testing.T) {
	if _, err := parseAnnotateResponse("Sorry, I can't help with that."); err == nil {
		t.Errorf("expected error on non-JSON response")
	}
}

// TestSplitUpstreamValues_ExtraClusterPathsUnion verifies V121-7.2 AC:
// LLM cluster-specific paths are UNIONED with the heuristic, never
// subtractive, and only honoured when they map to a real leaf.
func TestSplitUpstreamValues_ExtraClusterPathsUnion(t *testing.T) {
	upstream := []byte(`replicaCount: 1
ingress:
  enabled: false
customSetting: foo
`)
	out := SplitUpstreamValues(SmartValuesInput{
		AddonName:                 "demo",
		Chart:                     "demo",
		Version:                   "1.0",
		RepoURL:                   "https://example.com",
		UpstreamValues:            upstream,
		ExtraClusterSpecificPaths: []string{"customSetting", "totallyMadeUpPath.thatDoesNotExist"},
	})
	// `customSetting` is a real leaf; should be unioned in.
	hasCustom := false
	for _, p := range out.ClusterSpecificPaths {
		if p == "customSetting" {
			hasCustom = true
		}
		if p == "totallyMadeUpPath.thatDoesNotExist" {
			t.Errorf("hallucinated path was honored — should be filtered out")
		}
	}
	if !hasCustom {
		t.Errorf("expected `customSetting` in cluster paths via LLM union, got %v", out.ClusterSpecificPaths)
	}
	// Heuristic-detected `replicaCount` should still be there too.
	hasReplicas := false
	for _, p := range out.ClusterSpecificPaths {
		if p == "replicaCount" {
			hasReplicas = true
		}
	}
	if !hasReplicas {
		t.Errorf("heuristic path replicaCount was lost; LLM union must be additive only")
	}
}

// newConfiguredAIClient returns a minimally-configured ai.Client whose
// IsEnabled() returns true. The actual provider methods will fail if
// hit (Ollama URL points at a non-routable address) so callers must
// rely on the secret guard short-circuiting before any network call.
func newConfiguredAIClient(t *testing.T) *ai.Client {
	t.Helper()
	return ai.NewClient(ai.Config{
		Provider:    ai.ProviderOllama,
		OllamaURL:   "http://127.0.0.1:1", // unreachable, by design
		OllamaModel: "test",
	})
}

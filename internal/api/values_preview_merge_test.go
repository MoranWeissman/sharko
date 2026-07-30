// Unit tests for the additive merge engine that powers the
// preview-merge endpoint (v1.21 QA Bundle 4 Fix #4). The HTTP layer is
// thin; the merge logic is the part most likely to regress, so the
// coverage focuses there.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestPreviewMergeBodies_AddsNewKeysOnly verifies the v1.21 Bundle 5
// behaviour: current file is in the new TOP-LEVEL shape, merged output is
// in the same top-level shape, no `<addonName>:` wrap anywhere.
func TestPreviewMergeBodies_AddsNewKeysOnly(t *testing.T) {
	current := []byte(`replicaCount: 3
ingress:
  host: cert.example.com
`)
	upstream := []byte(`replicaCount: 1
ingress:
  enabled: true
  host: changeme.example.com
prometheus:
  enabled: false
`)
	mergedBody, summary, err := previewMergeBodies("cert-manager", current, upstream)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	// Decode merged body and verify shape — NO `<addonName>:` wrap.
	var root map[string]interface{}
	if err := yaml.Unmarshal([]byte(mergedBody), &root); err != nil {
		t.Fatalf("merged body did not parse as YAML: %v\nbody=%s", err, mergedBody)
	}
	if _, hasWrap := root["cert-manager"]; hasWrap {
		t.Fatalf("merged body must NOT have an `<addonName>:` wrap key, got: %#v", root)
	}

	// User's replicaCount must be preserved (3, not the upstream 1).
	if v, _ := root["replicaCount"].(int); v != 3 {
		t.Errorf("replicaCount should be preserved at 3, got %v", root["replicaCount"])
	}
	// User's ingress.host must be preserved.
	ing, _ := root["ingress"].(map[string]interface{})
	if h, _ := ing["host"].(string); h != "cert.example.com" {
		t.Errorf("ingress.host should be preserved, got %q", h)
	}
	// New upstream key ingress.enabled must have been added.
	if _, hasEnabled := ing["enabled"]; !hasEnabled {
		t.Errorf("expected new ingress.enabled to be added, ingress=%#v", ing)
	}
	// Brand-new top-level upstream key prometheus must be added.
	if _, hasProm := root["prometheus"]; !hasProm {
		t.Errorf("expected prometheus block to be added, root=%#v", root)
	}

	// Summary surfaces the new keys.
	if !strSliceHas(summary.NewKeys, "ingress.enabled") {
		t.Errorf("summary.NewKeys should include ingress.enabled, got %v", summary.NewKeys)
	}
	if !strSliceHas(summary.NewKeys, "prometheus.enabled") {
		t.Errorf("summary.NewKeys should include prometheus.enabled, got %v", summary.NewKeys)
	}
	// Summary surfaces the preserved keys.
	if !strSliceHas(summary.PreservedUserKeys, "replicaCount") {
		t.Errorf("summary.PreservedUserKeys should include replicaCount, got %v", summary.PreservedUserKeys)
	}
	if !strSliceHas(summary.PreservedUserKeys, "ingress.host") {
		t.Errorf("summary.PreservedUserKeys should include ingress.host, got %v", summary.PreservedUserKeys)
	}
	if summary.NoOp {
		t.Error("summary.NoOp should be false when there are new keys")
	}
}

// TestPreviewMergeBodies_LegacyWrappedCurrentIsUnwrapped verifies the
// transparent-unwrap path: a legacy-wrapped current file is unwrapped
// before the merge so the diff vs upstream is apples-to-apples and the
// merged output is consistently top-level.
func TestPreviewMergeBodies_LegacyWrappedCurrentIsUnwrapped(t *testing.T) {
	current := []byte(`cert-manager:
  replicaCount: 3
  ingress:
    host: cert.example.com
`)
	upstream := []byte(`replicaCount: 1
ingress:
  enabled: true
prometheus:
  enabled: false
`)
	mergedBody, summary, err := previewMergeBodies("cert-manager", current, upstream)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal([]byte(mergedBody), &root); err != nil {
		t.Fatalf("merged body did not parse as YAML: %v", err)
	}
	if _, hasWrap := root["cert-manager"]; hasWrap {
		t.Fatalf("legacy-wrapped current must produce UNWRAPPED merged output, got: %#v", root)
	}
	if v, _ := root["replicaCount"].(int); v != 3 {
		t.Errorf("preserved replicaCount=3 lost across unwrap+merge, got %v", root["replicaCount"])
	}
	if !strSliceHas(summary.PreservedUserKeys, "replicaCount") {
		t.Errorf("expected replicaCount in PreservedUserKeys, got %v", summary.PreservedUserKeys)
	}
	if !strSliceHas(summary.NewKeys, "prometheus.enabled") {
		t.Errorf("expected prometheus.enabled in NewKeys, got %v", summary.NewKeys)
	}
}

func TestPreviewMergeBodies_NoOpWhenAllUpstreamKeysSet(t *testing.T) {
	current := []byte(`replicaCount: 3
ingress:
  enabled: true
  host: cert.example.com
`)
	upstream := []byte(`replicaCount: 1
ingress:
  enabled: false
  host: changeme.example.com
`)
	_, summary, err := previewMergeBodies("cert-manager", current, upstream)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !summary.NoOp {
		t.Errorf("expected NoOp=true when nothing new in upstream, summary=%+v", summary)
	}
	if len(summary.NewKeys) != 0 {
		t.Errorf("expected NewKeys empty, got %v", summary.NewKeys)
	}
}

func TestPreviewMergeBodies_PreservesSmartValuesHeader(t *testing.T) {
	current := []byte(`# Generated by Sharko from cert-manager@1.20.1 on 2026-04-15
# Chart source: https://charts.jetstack.io
# AI annotation: disabled
# sharko: managed=true

replicaCount: 2
`)
	upstream := []byte(`replicaCount: 1
metrics:
  enabled: true
`)
	body, _, err := previewMergeBodies("cert-manager", current, upstream)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.HasPrefix(body, "# Generated by Sharko") {
		t.Errorf("expected smart-values header to be re-prepended, got:\n%s", body[:120])
	}
	if !strings.Contains(body, "# sharko: managed=true") {
		t.Errorf("expected managed=true line preserved, got:\n%s", body)
	}
	// Body must NOT wrap under `cert-manager:` — top-level keys only.
	for _, line := range strings.Split(body, "\n") {
		if line == "cert-manager:" {
			t.Fatalf("merged body must NOT have a `cert-manager:` wrapper, got:\n%s", body)
		}
	}
}

func TestPreviewMergeBodies_EmptyCurrentBecomesFullUpstream(t *testing.T) {
	upstream := []byte(`replicaCount: 1
metrics:
  enabled: true
`)
	body, summary, err := previewMergeBodies("cert-manager", []byte{}, upstream)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !strings.Contains(body, "replicaCount") {
		t.Errorf("expected replicaCount in merged body, got:\n%s", body)
	}
	if !strSliceHas(summary.NewKeys, "replicaCount") {
		t.Errorf("expected replicaCount in NewKeys, got %v", summary.NewKeys)
	}
	if summary.NoOp {
		t.Error("NoOp should be false when starting from empty")
	}
}

// TestPreviewMergeBodies_ParseErrorEchoesRawFileBytes documents the exact
// leak class the handler-side fix guards against (Wave 2 ride-along w2-q6
// item 6): gopkg.in/yaml.v3's decoder.terror echoes up to 7 bytes of the
// offending scalar VALUE — i.e. actual file content — into its own error
// message (`value[:7] + "..."` when the value is longer than 10
// characters). A malformed "current" values file therefore produces an
// error whose text contains a snippet of that file's bytes. This is why
// handlePreviewMergeAddonValues (values_preview_merge.go) must never hand
// this error's .Error() straight to writeError — see
// TestPreviewMergeBodies_ParseErrorSanitizedByWriteServerError below for
// the other half of the proof.
func TestPreviewMergeBodies_ParseErrorEchoesRawFileBytes(t *testing.T) {
	// "SECRET-TOKEN-VALUE-DO-NOT-LEAK" is a string where a map is expected
	// (replicaCount wants an int-shaped scalar for THIS test's purposes —
	// simplest reliable trigger is a top-level scalar where a mapping is
	// expected, which yaml.v3 reports via the same terror path).
	current := []byte("SECRET-TOKEN-VALUE-DO-NOT-LEAK\n")
	upstream := []byte("replicaCount: 1\n")

	_, _, err := previewMergeBodies("cert-manager", current, upstream)
	if err == nil {
		t.Fatal("expected a parse error for a scalar document where a mapping is required")
	}
	// Prove the vulnerability class is real: the raw error DOES contain a
	// prefix of the file's own bytes. If this assertion ever starts
	// failing because yaml.v3 changed its error format, that's fine — it
	// just means this specific trigger no longer demonstrates the leak;
	// the sanitization in the test below must hold regardless of why.
	if !strings.Contains(err.Error(), "SECRET-") {
		t.Skipf("this yaml.v3 version no longer echoes raw scalar bytes in TypeError text (got: %v) — trigger needs updating, sanitization test below is unaffected", err)
	}
}

// TestPreviewMergeBodies_ParseErrorSanitizedByWriteServerError is the
// other half: even though previewMergeBodies' error can carry raw file
// bytes (proven above), routing it through writeServerError — what
// handlePreviewMergeAddonValues now does instead of writeError — means
// the HTTP response body never contains them. Mirrors
// TestWriteUpstreamError_PreservesSanitization's shape for this specific
// call site.
func TestPreviewMergeBodies_ParseErrorSanitizedByWriteServerError(t *testing.T) {
	current := []byte("SECRET-TOKEN-VALUE-DO-NOT-LEAK\n")
	upstream := []byte("replicaCount: 1\n")

	_, _, mergeErr := previewMergeBodies("cert-manager", current, upstream)
	if mergeErr == nil {
		t.Fatal("expected a parse error")
	}

	w := httptest.NewRecorder()
	writeServerError(w, http.StatusBadGateway, "preview_merge_addon_values", mergeErr)

	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "SECRET-") {
		t.Errorf("response body leaked file content: %s", body)
	}
	var parsed map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if parsed["error"] != http.StatusText(http.StatusBadGateway) {
		t.Errorf("error field = %q, want %q", parsed["error"], http.StatusText(http.StatusBadGateway))
	}
	if parsed["op"] != "preview_merge_addon_values" {
		t.Errorf("op field = %q, want %q", parsed["op"], "preview_merge_addon_values")
	}
}

// strSliceHas is a small helper because this package's existing tests don't
// pull in slices.Contains, and the package already exports a different
// `contains` symbol from clusters_adopt.go.
func strSliceHas(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

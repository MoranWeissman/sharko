package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/schema"
	"gopkg.in/yaml.v3"
)

// TestMarshalDefaultAddons_RoundTrip verifies that marshalling produces valid enveloped YAML.
func TestMarshalDefaultAddons_RoundTrip(t *testing.T) {
	addons := []string{"cert-manager", "external-dns"}
	body, err := marshalDefaultAddons(addons)
	if err != nil {
		t.Fatalf("marshalDefaultAddons failed: %v", err)
	}

	// Parse back.
	parsed, err := parseDefaultAddons(body)
	if err != nil {
		t.Fatalf("parseDefaultAddons failed: %v", err)
	}

	if len(parsed) != 2 || parsed[0] != "cert-manager" || parsed[1] != "external-dns" {
		t.Errorf("round-trip mismatch: got %v, want [cert-manager external-dns]", parsed)
	}
}

// TestParseDefaultAddons_SchemaValidation verifies that the schema validator accepts the marshalled envelope.
func TestParseDefaultAddons_SchemaValidation(t *testing.T) {
	addons := []string{"addon-a"}
	body, err := marshalDefaultAddons(addons)
	if err != nil {
		t.Fatalf("marshalDefaultAddons failed: %v", err)
	}

	// Validate.
	validator, vErr := schema.DefaultValidator()
	if vErr != nil {
		t.Skipf("validator not available: %v", vErr)
	}
	if err := validator.Validate(schema.KindDefaultAddons, body); err != nil {
		t.Errorf("schema validation failed: %v", err)
	}
}

// TestParseDefaultAddons_WrongKind rejects a non-DefaultAddons envelope.
func TestParseDefaultAddons_WrongKind(t *testing.T) {
	wrongKind := `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: wrong
spec:
  addons: []
`
	_, err := parseDefaultAddons([]byte(wrongKind))
	if err == nil {
		t.Fatal("parseDefaultAddons should reject wrong kind")
	}
	// The error could be from schema validation OR from the kind check — both are acceptable rejections.
	// Just verify that it rejects.
}

// TestNormalizeAddonNames verifies whitespace trimming, deduplication, and empty filtering.
func TestNormalizeAddonNames(t *testing.T) {
	input := []string{"  cert-manager ", "external-dns", "", "cert-manager", "   "}
	normalized := normalizeAddonNames(input)
	if len(normalized) != 2 || normalized[0] != "cert-manager" || normalized[1] != "external-dns" {
		t.Errorf("normalizeAddonNames: got %v, want [cert-manager external-dns]", normalized)
	}
}

// TestParseDefaultAddons_EmptyFile verifies empty addons list parses correctly.
func TestParseDefaultAddons_EmptyFile(t *testing.T) {
	emptyDoc := schema.Envelope[config.DefaultAddonsSpec]{
		APIVersion: schema.APIVersion,
		Kind:       schema.KindDefaultAddons,
		Metadata:   schema.Metadata{Name: "default-addons"},
		Spec:       config.DefaultAddonsSpec{Addons: []string{}},
	}
	body, _ := yaml.Marshal(emptyDoc)

	parsed, err := parseDefaultAddons(body)
	if err != nil {
		t.Fatalf("parseDefaultAddons(empty): %v", err)
	}
	if len(parsed) != 0 {
		t.Errorf("empty file should parse to empty slice, got %v", parsed)
	}
}

// TestHandlePutDefaultAddons_RefuseOnV4Repo — walk finding: the #629 sweep
// gated the other nine v3-only writers (legacy catalog add/remove/configure,
// enable/disable addon) with a coded 409 on a v4 repo, but PUT
// /default-addons writes configuration/default-addons.yaml the same
// v3-only way and was missed. On a v4 repo that file is never read, so the
// UI's "Preview changes" / "Save default addons" buttons offered a write
// that silently did nothing useful. Covers both dry_run values: the
// refusal is unconditional, the same precedent as the other guarded
// writers (a preview must not imply a save that would never take effect).
func TestHandlePutDefaultAddons_RefuseOnV4Repo(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(map[bool]string{true: "dry_run=true", false: "dry_run=false"}[dryRun], func(t *testing.T) {
			gp := &v4GateGit{v4: true}
			srv := serverWithGitOverride(t, gp)
			router := NewRouter(srv, nil)

			body, _ := json.Marshal(DefaultAddonsPutRequest{
				Addons: []string{"cert-manager"},
				DryRun: dryRun,
			})
			req := httptest.NewRequest(http.MethodPut, "/api/v1/default-addons", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body: %s)", w.Code, w.Body.String())
			}
			var respBody map[string]any
			if err := json.NewDecoder(w.Body).Decode(&respBody); err != nil {
				t.Fatalf("response is not JSON: %v", err)
			}
			if respBody["code"] != CodeRepoLayout {
				t.Errorf("code = %v, want %q", respBody["code"], CodeRepoLayout)
			}
			if len(gp.writes) != 0 {
				t.Errorf("the refusal still wrote to git: %v", gp.writes)
			}
		})
	}
}

// TestHandlePutDefaultAddons_UnchangedOnV3Repo — the gate must stay inert
// on a v3 (or empty) repo; this is the existing behavior the fix must not
// regress. ArgoCD/Git are not wired up in this isolated server, so the
// request fails downstream (502) instead of succeeding — the point of this
// test is only that it does NOT fail with the v4 repo_layout refusal.
func TestHandlePutDefaultAddons_UnchangedOnV3Repo(t *testing.T) {
	gp := &v4GateGit{v4: false}
	srv := serverWithGitOverride(t, gp)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(DefaultAddonsPutRequest{Addons: []string{"cert-manager"}, DryRun: true})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/default-addons", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == http.StatusConflict {
		var respBody map[string]any
		_ = json.NewDecoder(w.Body).Decode(&respBody)
		if respBody["code"] == CodeRepoLayout {
			t.Fatalf("the v4 gate fired on a v3 repo: %d %v", w.Code, respBody)
		}
	}
}

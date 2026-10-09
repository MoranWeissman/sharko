//go:build e2e

// Package lifecycle — v4-coherence-closure lane P (e2e honesty round 2).
//
// TestCatalogEditDeleteV4 is NEW coverage for the v4-native catalog
// edit/delete door — PATCH and DELETE /api/v1/catalog/addons/{name}
// (internal/orchestrator/{catalog_edit,catalog_delete}.go, feature #774).
// Before this test, tests/e2e/lifecycle had zero coverage of this door.
// The only PATCH/DELETE catalog e2e checks that already existed
// (addon_admin_test.go's PatchCustomAddon_502_NoConnection /
// DeleteCustomAddon_*_502) exercise a DIFFERENT, older, ArgoCD-backed v3
// door — PATCH/DELETE /api/v1/addons/{name} — not this one.
//
// Why this can run in the fast, no-kind lane: EditCatalogEntry and
// DeleteFromCatalog (internal/orchestrator/catalog_edit.go,
// catalog_delete.go) are pure Git operations — neither calls a method on
// the ArgoCD client the API handler resolves. The handler asks
// connSvc.GetActiveArgocdClient() purely as a generic "is there an active
// connection at all" gate before doing any work, the same shape every
// other catalog-write handler in catalog_org.go uses, and never touches
// the resulting client. GetActiveArgocdClient itself does not dial
// anything either (internal/service/connection.go — it lazily constructs
// an *argocd.Client struct, same as connection Create() never validates
// reachability, only field shape). So a syntactically valid but
// unreachable ArgoCD URL is all this door needs — no kind, no real
// ArgoCD, unlike TestClusterLifecycle's adopt/unadopt/label-patch
// subtests which DO end up on real ArgoCD-touching code paths.
package lifecycle

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// decodeCatalogWriteResult decodes a 200 response from the catalog
// edit/delete door into a *orchestrator.GitResult, transparently unwrapping
// the {"result": ..., "attribution_warning": "..."} envelope
// withAttributionWarning (internal/api/tiered_git.go) applies whenever the
// Tier 2 write falls back to the service token — which every harness
// client hits, because the harness never seeds a per-user Git PAT. Mirrors
// tests/e2e/harness's unexported unwrapAttribution (apiclient_values.go);
// duplicated here rather than exported across the package boundary because
// this is the first lifecycle test to need it outside the harness's own
// typed wrappers.
func decodeCatalogWriteResult(t *testing.T, resp *http.Response) *orchestrator.GitResult {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("decodeCatalogWriteResult: read body: %v", err)
	}
	var probe struct {
		Result             json.RawMessage `json:"result,omitempty"`
		AttributionWarning string          `json:"attribution_warning,omitempty"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("decodeCatalogWriteResult: probe decode: %v (raw=%s)", err, raw)
	}
	var out orchestrator.GitResult
	if probe.AttributionWarning != "" && len(probe.Result) > 0 {
		if err := json.Unmarshal(probe.Result, &out); err != nil {
			t.Fatalf("decodeCatalogWriteResult: wrapped payload decode: %v (raw=%s)", err, raw)
		}
		return &out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decodeCatalogWriteResult: direct payload decode: %v (raw=%s)", err, raw)
	}
	return &out
}

// fakeArgoCDURL is a syntactically valid ArgoCD server URL that is never
// dialed by this test — see the package doc comment above for why that is
// safe for the catalog edit/delete door specifically.
const fakeArgoCDURL = "https://127.0.0.1:1"

func TestCatalogEditDeleteV4(t *testing.T) {
	gitfake := harness.StartGitFake(t)
	ghmock := harness.StartGitMock(t)

	// v4 bootstrap FIRST — same reasoning as cluster_helpers.go's
	// seedV4Bootstrap doc comment: every mutating handler refuses a write
	// on a v3-format repo, and this suite's own catalog write would
	// otherwise land on the v3 marker path.
	seedV4Bootstrap(t, ghmock, gitfake.RepoURL)

	// Seed one catalog entry directly through the mock git provider so the
	// edit/delete subtests below have something real on disk to work on.
	seedCatalog := func(entries map[string]config.AddonCatalogEntry) {
		body, err := config.SaveAddonCatalog(config.AddonCatalogSpec{Addons: entries})
		if err != nil {
			t.Fatalf("config.SaveAddonCatalog: %v", err)
		}
		if err := ghmock.CreateOrUpdateFile(t.Context(), config.AddonCatalogPath, body, "main", "seed: catalog"); err != nil {
			t.Fatalf("seed %s: %v", config.AddonCatalogPath, err)
		}
	}
	seedCatalog(map[string]config.AddonCatalogEntry{
		"cert-manager": {
			RepoURL:   "https://charts.jetstack.io",
			Chart:     "cert-manager",
			Version:   "v1.14.5",
			Namespace: "cert-manager",
		},
		"metrics-server": {
			RepoURL:   "https://kubernetes-sigs.github.io/metrics-server/",
			Chart:     "metrics-server",
			Version:   "3.11.0",
			Namespace: "kube-system",
		},
	})

	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     gitfake,
		GitProvider: ghmock,
	})
	sharko.WaitHealthy(t, 10*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())
	admin := harness.NewClient(t, sharko)

	// seedActiveConnection (cluster_helpers.go) is reused with a fake
	// ArgoCD URL instead of a real kind-provisioned one — see the package
	// doc comment for why that is enough for this door. PRAutoMerge stays
	// on (the helper's default), so a successful write lands on "main"
	// synchronously and the git-side assertions below can read it back
	// immediately.
	seedActiveConnection(t, admin, fakeArgoCDURL, "fake-argocd-token")

	t.Run("EditCatalogEntry_RealSuccess", func(t *testing.T) {
		// v4-coherence-closure lane P: this used to have no coverage at
		// all. EditCatalogEntry has no v4-unsupported gate to begin with
		// (it is the v4-native door — the OPPOSITE of adopt/unadopt/
		// label-patch, which used to refuse v4 and now support it) so
		// this subtest demands a real 200 outright, no tolerance branch.
		resp := admin.Do(t, http.MethodPatch, "/api/v1/catalog/addons/cert-manager",
			map[string]any{"version": "v1.15.0"})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("EditCatalogEntry: status=%d", resp.StatusCode)
		}
		result := decodeCatalogWriteResult(t, resp)
		if result.PRUrl == "" {
			t.Errorf("EditCatalogEntry: empty PRUrl in result: %+v", result)
		}
		if !result.Merged {
			t.Errorf("EditCatalogEntry: Merged=false; PRAutoMerge is on by default via seedActiveConnection")
		}

		// Git-side effect: catalog.yaml on main now carries the new
		// version, and every field this PATCH didn't mention (chart,
		// repoURL, namespace) survived unchanged — the merge-semantics
		// contract the handler's own doc comment promises.
		raw, err := ghmock.GetFileContent(t.Context(), config.AddonCatalogPath, "main")
		if err != nil {
			t.Fatalf("read %s after edit: %v", config.AddonCatalogPath, err)
		}
		spec, err := config.LoadAddonCatalog(raw)
		if err != nil {
			t.Fatalf("parse %s after edit: %v", config.AddonCatalogPath, err)
		}
		entry, ok := spec.Addons["cert-manager"]
		if !ok {
			t.Fatalf("cert-manager missing from catalog.yaml after edit; spec=%+v", spec)
		}
		if entry.Version != "v1.15.0" {
			t.Errorf("cert-manager.Version=%q want v1.15.0", entry.Version)
		}
		if entry.Chart != "cert-manager" || entry.RepoURL != "https://charts.jetstack.io" || entry.Namespace != "cert-manager" {
			t.Errorf("cert-manager: untouched fields changed unexpectedly: %+v", entry)
		}
	})

	t.Run("DeleteFromCatalog_Blocked_WhenEnabledOnCluster", func(t *testing.T) {
		// Real refusal, not a placeholder one: DeleteFromCatalog's LOCKED
		// design decision (catalog_delete.go doc comment) is that an addon
		// still switched on anywhere refuses with 409 before any write.
		// Seed metrics-server as enabled on a cluster and confirm the
		// refusal actually fires and names the blocking cluster — this is
		// the real v4 contract, not a "not built yet" 409.
		clusterAddonsPath, err := config.V4ClusterAddonsPath("some-cluster")
		if err != nil {
			t.Fatalf("config.V4ClusterAddonsPath: %v", err)
		}
		body, err := gitops.SetClusterAddonsAddon(nil, "some-cluster", "metrics-server", true, nil, nil)
		if err != nil {
			t.Fatalf("gitops.SetClusterAddonsAddon: %v", err)
		}
		if err := ghmock.CreateOrUpdateFile(t.Context(), clusterAddonsPath, body, "main", "seed: enable metrics-server"); err != nil {
			t.Fatalf("seed %s: %v", clusterAddonsPath, err)
		}

		resp := admin.Do(t, http.MethodDelete, "/api/v1/catalog/addons/metrics-server?confirm=true", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("DeleteFromCatalog(metrics-server, enabled on a cluster): status=%d want 409", resp.StatusCode)
		}
		var decoded map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode blocked-delete body: %v", err)
		}
		if code, _ := decoded["code"].(string); code != "addon_enabled_on_clusters" {
			t.Fatalf("blocked-delete code=%q want addon_enabled_on_clusters; body=%v", code, decoded)
		}
		clusters, _ := decoded["clusters"].([]any)
		if len(clusters) != 1 || clusters[0] != "some-cluster" {
			t.Fatalf("blocked-delete clusters=%v want [\"some-cluster\"]", decoded["clusters"])
		}

		// metrics-server must still be in the catalog — the refusal ran
		// before any write.
		raw, err := ghmock.GetFileContent(t.Context(), config.AddonCatalogPath, "main")
		if err != nil {
			t.Fatalf("read %s after blocked delete: %v", config.AddonCatalogPath, err)
		}
		spec, err := config.LoadAddonCatalog(raw)
		if err != nil {
			t.Fatalf("parse %s after blocked delete: %v", config.AddonCatalogPath, err)
		}
		if _, ok := spec.Addons["metrics-server"]; !ok {
			t.Fatalf("metrics-server missing from catalog.yaml after a REFUSED delete — the refusal must not have written anything")
		}
	})

	t.Run("DeleteFromCatalog_RealSuccess", func(t *testing.T) {
		// cert-manager is not enabled on any cluster, so the delete goes
		// through for real.
		resp := admin.Do(t, http.MethodDelete, "/api/v1/catalog/addons/cert-manager?confirm=true", nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("DeleteFromCatalog(cert-manager): status=%d", resp.StatusCode)
		}
		result := decodeCatalogWriteResult(t, resp)
		if !result.Merged {
			t.Errorf("DeleteFromCatalog: Merged=false; PRAutoMerge is on by default via seedActiveConnection")
		}

		raw, err := ghmock.GetFileContent(t.Context(), config.AddonCatalogPath, "main")
		if err != nil {
			t.Fatalf("read %s after delete: %v", config.AddonCatalogPath, err)
		}
		spec, err := config.LoadAddonCatalog(raw)
		if err != nil {
			t.Fatalf("parse %s after delete: %v", config.AddonCatalogPath, err)
		}
		if _, ok := spec.Addons["cert-manager"]; ok {
			t.Fatalf("cert-manager still in catalog.yaml after a successful delete: %+v", spec)
		}
		// metrics-server (unrelated, still blocked-enabled) must survive —
		// a delete touches exactly one entry.
		if _, ok := spec.Addons["metrics-server"]; !ok {
			t.Fatalf("metrics-server missing from catalog.yaml — an unrelated delete must not have touched it")
		}
	})
}

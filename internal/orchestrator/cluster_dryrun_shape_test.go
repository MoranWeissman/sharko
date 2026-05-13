package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// V125-1.4 (BUG-049) — DryRunResult shape parity between providers.
//
// Before V125-1.4 the kubeconfig path could return a DryRunResult with
// nil EffectiveAddons / SecretsToCreate (when the request had no addons
// and the orchestrator's secretDefs was unset). nil slices marshal to
// JSON `null`, which crashed the ClustersOverview preview panel
// (`Cannot read properties of null (reading 'length')`).
//
// These tests pin the contract that matters at the wire layer:
//
//   - Both providers populate non-empty PRTitle.
//   - Both providers ALWAYS marshal the slice fields as `[]` (not null),
//     even when there are no addons / no addon secrets.
//   - The kubeconfig PR title is distinguishable from the EKS one so the
//     preview tells the operator which credentials path will be used.
//   - Field set is identical across providers — adding a provider in
//     V125+ inherits the shape automatically.

func decodeDryRunJSON(t *testing.T, dr *DryRunResult) map[string]any {
	t.Helper()
	b, err := json.Marshal(dr)
	if err != nil {
		t.Fatalf("marshal DryRunResult: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal DryRunResult: %v", err)
	}
	return m
}

func assertSliceFieldNotNull(t *testing.T, m map[string]any, key string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("DryRunResult JSON missing required field %q", key)
		return
	}
	if v == nil {
		t.Errorf("DryRunResult.%s should marshal to [] (not null) for shape parity", key)
	}
}

func TestRegisterCluster_DryRun_Kubeconfig_NonNilArrays(t *testing.T) {
	// kind-style scenario: no credProvider, no secretDefs, no selected addons.
	// V125-1.1 introduced this path; pre-V125-1.4 it returned nil arrays.
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		DryRun:     true,
		// No addons selected — the typical generic-K8s flow.
	})
	if err != nil {
		t.Fatalf("expected dry-run success, got error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected DryRun to be populated")
	}
	if result.DryRun.PRTitle == "" {
		t.Error("expected PRTitle to be non-empty")
	}
	if !strings.Contains(result.DryRun.PRTitle, "kubeconfig") {
		t.Errorf("expected PRTitle to advertise kubeconfig provider, got %q", result.DryRun.PRTitle)
	}
	if result.DryRun.EffectiveAddons == nil {
		t.Error("EffectiveAddons must be non-nil ([]) — JSON-marshaled null crashes the FE preview panel")
	}
	if result.DryRun.SecretsToCreate == nil {
		t.Error("SecretsToCreate must be non-nil ([]) — JSON-marshaled null crashes the FE preview panel")
	}
	if len(result.DryRun.FilesToWrite) == 0 {
		t.Error("FilesToWrite must contain the values file + managed-clusters entry")
	}

	// Wire-layer assertion: the JSON must contain `[]` for every slice
	// field — this is what the FE actually sees.
	m := decodeDryRunJSON(t, result.DryRun)
	assertSliceFieldNotNull(t, m, "effective_addons")
	assertSliceFieldNotNull(t, m, "files_to_write")
	assertSliceFieldNotNull(t, m, "secrets_to_create")
}

func TestRegisterCluster_DryRun_Kubeconfig_EffectiveAddons(t *testing.T) {
	// With addons selected, EffectiveAddons mirrors the request set just
	// like the EKS path. This guards the resolve logic for the kubeconfig
	// branch — same as the EKS test pattern.
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"monitoring": true, "secrets-store": false},
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("dry-run error: %v", err)
	}
	if len(result.DryRun.EffectiveAddons) != 1 || result.DryRun.EffectiveAddons[0] != "monitoring" {
		t.Errorf("expected EffectiveAddons=[monitoring], got %v", result.DryRun.EffectiveAddons)
	}
}

func TestRegisterCluster_DryRun_EKS_NonNilArrays(t *testing.T) {
	// EKS path with no addons + no secret defs — same contract as the
	// kubeconfig path. Guards against a regression in the V125-1.4 refactor
	// where the nil-coalesce was applied to one provider but not the other.
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("dry-run error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected DryRun to be populated")
	}
	if result.DryRun.EffectiveAddons == nil {
		t.Error("EffectiveAddons must be non-nil ([]) on the EKS path too")
	}
	if result.DryRun.SecretsToCreate == nil {
		t.Error("SecretsToCreate must be non-nil ([]) on the EKS path too")
	}
	m := decodeDryRunJSON(t, result.DryRun)
	assertSliceFieldNotNull(t, m, "effective_addons")
	assertSliceFieldNotNull(t, m, "files_to_write")
	assertSliceFieldNotNull(t, m, "secrets_to_create")
}

func TestRegisterCluster_DryRun_ProviderShapeParity(t *testing.T) {
	// The two provider paths must produce identical JSON-key sets so that
	// any FE component (or downstream consumer) can rely on a single shape
	// regardless of how the cluster was registered. Pin the key set — if
	// V125+ adds a new field, both paths must add it together.
	eks := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	eksResult, err := eks.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Region: "eu-west-1",
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("eks dry-run error: %v", err)
	}

	kc := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	kcResult, err := kc.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("kubeconfig dry-run error: %v", err)
	}

	eksKeys := keySet(decodeDryRunJSON(t, eksResult.DryRun))
	kcKeys := keySet(decodeDryRunJSON(t, kcResult.DryRun))
	if !sameKeys(eksKeys, kcKeys) {
		t.Errorf("EKS and kubeconfig DryRunResult JSON-key sets differ:\n  eks=%v\n  kbc=%v", eksKeys, kcKeys)
	}

	// Distinct PR titles so the preview communicates which credentials path
	// the operator is about to commit to.
	if eksResult.DryRun.PRTitle == kcResult.DryRun.PRTitle {
		t.Errorf("EKS and kubeconfig PRTitle should differ to reflect the credentials path; both = %q", eksResult.DryRun.PRTitle)
	}
}

func keySet(m map[string]any) map[string]bool {
	s := make(map[string]bool, len(m))
	for k := range m {
		s[k] = true
	}
	return s
}

func sameKeys(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

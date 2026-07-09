package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-88.3 — "lazy credentials": Sharko demands its own spoke-cluster
// credentials ONLY at the moment it is actually needed (enabling a
// secret-bearing addon), never at registration time.
//
// catalogWithSecretsYAML seeds a catalog where "datadog" declares a
// `secrets:` block with TWO distinct Kubernetes Secrets and "cert-manager"
// declares none — the two addons every test below routes through.
const catalogWithSecretsYAML = `applicationsets:
  - name: datadog
    chart: datadog
    repoURL: https://example.com
    version: 1.0.0
    secrets:
      - secretName: datadog-keys
        namespace: datadog
        keys:
          api-key: secrets/datadog/api-key
      - secretName: datadog-app-keys
        namespace: datadog
        keys:
          app-key: secrets/datadog/app-key
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.0.0
`

// newCredsGateGit builds a mock Git provider seeded with the
// secrets-bearing catalog above and a managed-clusters.yaml entry for
// "prod-eu" with an empty values file, matching the fixture every
// EnableAddon happy-path test in this package uses.
func newCredsGateGit() *mockGitProvider {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(catalogWithSecretsYAML)
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# Cluster values for prod-eu\nclusterGlobalValues:\n")
	return git
}

// ---------------------------------------------------------------------------
// Goal 1 — registration succeeds with ZERO Sharko-side credentials.
// ---------------------------------------------------------------------------

// TestRegisterCluster_ZeroCredentials_DefaultSharkoManaged_Succeeds is the
// headline registration contract for V2-cleanup-88.3: a Sharko-managed
// (default) registration with no creds_source, no secret_path, no inline
// kubeconfig, and no configured credentials provider succeeds cleanly —
// no error, no partial status, the cluster registers as connection-only.
func TestRegisterCluster_ZeroCredentials_DefaultSharkoManaged_Succeeds(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil) // credProvider is nil

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "connection-only",
		Addons: map[string]bool{"monitoring": true},
		// ConnectionManagedBy deliberately absent — default Sharko-managed
		// mode, not the self-managed relaxation.
	})
	if err != nil {
		t.Fatalf("expected success with zero credentials, got error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected status=success (no scary warning-as-failure), got %q (%s)", result.Status, result.Error)
	}
	if !hasStep(result.CompletedSteps, "skip_credentials") {
		t.Errorf("expected the general skip_credentials step (not the self-managed variant), got %v", result.CompletedSteps)
	}
	if result.Verification != nil {
		t.Error("verification must be skipped when there is nothing to verify with")
	}
	mc := string(git.files["configuration/managed-clusters.yaml"])
	if !strings.Contains(mc, "name: connection-only") {
		t.Fatalf("cluster must still be recorded in managed-clusters.yaml, got:\n%s", mc)
	}
}

// ---------------------------------------------------------------------------
// Goal 2 — EnableAddon's pre-flight credentials gate.
// ---------------------------------------------------------------------------

// TestEnableAddon_SecretBearingAddon_NoCredentials_Rejected pins the
// enforcement moment: enabling "datadog" (2 secrets) on a cluster Sharko
// has no credentials for is rejected with the exact actionable message,
// BEFORE any Git write.
func TestEnableAddon_SecretBearingAddon_NoCredentials_Rejected(t *testing.T) {
	argocd := newMockArgocd()
	git := newCredsGateGit()
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil) // credProvider is nil

	_, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "datadog",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected a rejection when the cluster has no credentials for a secret-bearing addon")
	}
	if !IsMissingClusterCredentials(err) {
		t.Fatalf("expected *MissingClusterCredentialsError (→ 4xx), got %T: %v", err, err)
	}
	want := `addon "datadog" needs 2 secrets pushed to the cluster, but Sharko has no credentials for cluster "prod-eu" — add connection credentials (secret path or EKS role) to the cluster, or choose an addon without secrets`
	if err.Error() != want {
		t.Errorf("error message mismatch:\n got:  %s\n want: %s", err.Error(), want)
	}
	// No PR may have been opened — the gate runs before any Git write.
	if len(git.prs) != 0 {
		t.Errorf("expected no PR to be opened, got %d", len(git.prs))
	}
}

// TestEnableAddon_SecretBearingAddon_NoCredentials_DryRunAlsoRejected proves
// the gate applies to the preview too — dry-run must not promise something
// the real enable would then reject.
func TestEnableAddon_SecretBearingAddon_NoCredentials_DryRunAlsoRejected(t *testing.T) {
	argocd := newMockArgocd()
	git := newCredsGateGit()
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "datadog",
		DryRun:  true,
	})
	if !IsMissingClusterCredentials(err) {
		t.Fatalf("expected *MissingClusterCredentialsError on dry-run too, got %T: %v", err, err)
	}
}

// TestEnableAddon_SecretLessAddon_NoCredentials_Succeeds proves the gate is
// a complete no-op for an addon with no secrets — zero friction on a
// cred-less cluster, exactly like every other workload that deploys via
// Git -> ArgoCD.
func TestEnableAddon_SecretLessAddon_NoCredentials_Succeeds(t *testing.T) {
	argocd := newMockArgocd()
	git := newCredsGateGit()
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil) // credProvider is nil

	result, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("expected success for a secret-less addon on a cred-less cluster, got error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected status=success, got %q (%s)", result.Status, result.Error)
	}
}

// TestEnableAddon_SecretBearingAddon_WithCredentials_Unchanged proves the
// WITH-creds path is untouched: a cluster Sharko CAN reach still enables a
// secret-bearing addon exactly as before.
func TestEnableAddon_SecretBearingAddon_WithCredentials_Unchanged(t *testing.T) {
	argocd := newMockArgocd()
	git := newCredsGateGit()
	// managed-clusters.yaml stamps the backend creds source explicitly so
	// the router takes the backend path deterministically.
	git.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: prod-eu\n    credsSource: secret-kubeconfig\n    labels: {}\n")
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok", Raw: []byte("fake-kubeconfig")},
		},
	}
	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "datadog",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("expected success when the cluster has credentials, got error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected status=success, got %q (%s)", result.Status, result.Error)
	}
	if len(git.prs) != 1 {
		t.Errorf("expected exactly one PR to be opened, got %d", len(git.prs))
	}
}

// TestMissingClusterCredentialsError_SingularSecret pins the singular
// "1 secret" grammar (vs. "2 secrets") in the error message.
func TestMissingClusterCredentialsError_SingularSecret(t *testing.T) {
	err := &MissingClusterCredentialsError{Addon: "vault-agent", Cluster: "staging", SecretCount: 1}
	want := `addon "vault-agent" needs 1 secret pushed to the cluster, but Sharko has no credentials for cluster "staging" — add connection credentials (secret path or EKS role) to the cluster, or choose an addon without secrets`
	if err.Error() != want {
		t.Errorf("error message mismatch:\n got:  %s\n want: %s", err.Error(), want)
	}
}

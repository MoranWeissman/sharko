package orchestrator

import (
	"context"
	"strings"
	"testing"
)

// V125-1.1 — kubeconfig provider path through RegisterCluster.
//
// These tests pin the orchestrator's "inline kubeconfig" branch:
//
//   - Provider == "kubeconfig" must skip the credProvider entirely
//     (registration must be possible WITHOUT an AWS-SM/k8s-secrets
//     backend configured — the whole point of the kind-friendly path).
//   - The kubeconfig string is parsed via providers.ParseInlineKubeconfig,
//     and the resulting Server / CAData / Token flow into the same Git +
//     ArgoCD steps the EKS path uses.
//   - Cert-based / exec-plugin auth must fail at the provider whitelist
//     boundary (covered exhaustively in providers/kubeconfig_parser_test.go;
//     this file just guards the orchestrator wrapping path).
//   - The "kubeconfig" provider MUST appear in the supportedProviders set;
//     a typo there would silently regress generic-K8s registration.
const v125TestBearerKubeconfig = `apiVersion: v1
kind: Config
current-context: kind-sharko
clusters:
- name: kind-sharko
  cluster:
    server: https://127.0.0.1:60123
    certificate-authority-data: dGVzdC1jYS1ieXRlcw==
contexts:
- name: kind-sharko
  context:
    cluster: kind-sharko
    user: kind-sharko
users:
- name: kind-sharko
  user:
    token: ya29.example-bearer-token
`

func TestRegisterCluster_Kubeconfig_HappyPath_NoCredProvider(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// Pass nil credProvider on purpose — this is the kind-cluster scenario
	// where no AWS-SM / k8s-secrets backend is configured, and registration
	// must still succeed because the kubeconfig is supplied inline.
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
	if result.Cluster.Server != "https://127.0.0.1:60123" {
		t.Errorf("expected server from kubeconfig, got %q", result.Cluster.Server)
	}
	// The "parse_kubeconfig" step is the marker that the inline path was
	// actually taken (vs. the EKS "fetch_credentials" step name).
	foundParseStep := false
	for _, step := range result.CompletedSteps {
		if step == "parse_kubeconfig" {
			foundParseStep = true
			break
		}
	}
	if !foundParseStep {
		t.Errorf("expected parse_kubeconfig step in completed_steps; got %v", result.CompletedSteps)
	}
	// V125-1-8.3: kubeconfig provider used to fall through to a direct
	// argocd.RegisterCluster pre-merge call (BUG-058 root cause). That call
	// is now retired — the reconciler creates the Secret post-merge.
	if _, ok := argocd.registeredClusters["kind-sharko"]; ok {
		t.Errorf("V125-1-8.3 contract violated: kubeconfig path must NOT call argocd.RegisterCluster directly — reconciler owns Secret lifecycle")
	}
}

// TestRegisterCluster_Kubeconfig_WritesArgoSecret is the V2-cleanup-8.2
// regression test. When Sharko runs in-cluster (an argo secret manager is
// wired) and a kubeconfig is pasted, RegisterCluster must write the ArgoCD
// cluster Secret directly from the parsed bearer-token credentials. Without
// this the reconciler — which reads from a secrets backend the kubeconfig
// creds never reach — could never create the Secret, leaving the cluster
// permanently Unreachable.
func TestRegisterCluster_Kubeconfig_WritesArgoSecret(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	asm := newMockArgoSecretManager()

	// nil credProvider: the in-cluster manager must be wired independently of
	// any secrets backend (mirrors the production ungate in serve.go).
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}

	if len(asm.ensured) != 1 {
		t.Fatalf("expected exactly 1 Ensure call, got %d", len(asm.ensured))
	}
	spec := asm.ensured[0]
	if spec.Name != "kind-sharko" {
		t.Errorf("Ensure spec.Name = %q, want kind-sharko (must match the lookup key GetCredentials uses)", spec.Name)
	}
	if spec.Server != "https://127.0.0.1:60123" {
		t.Errorf("Ensure spec.Server = %q, want the kubeconfig server", spec.Server)
	}
	if spec.Token != "ya29.example-bearer-token" {
		t.Errorf("Ensure spec.Token = %q, want the kubeconfig bearer token", spec.Token)
	}
	// CAData must be base64 — the kubeconfig's certificate-authority-data was
	// already base64, decoded by clientcmd into raw bytes, then re-encoded here.
	if spec.CAData == "" {
		t.Error("Ensure spec.CAData is empty; expected the base64-encoded CA bundle")
	}
	if spec.Labels["monitoring"] != "true" {
		t.Errorf("Ensure spec.Labels[monitoring] = %q, want true (\"true\"/\"false\" form matching the reconciler)", spec.Labels["monitoring"])
	}

	// The write_argocd_secret step is the marker that the direct write ran.
	foundStep := false
	for _, step := range result.CompletedSteps {
		if step == "write_argocd_secret" {
			foundStep = true
			break
		}
	}
	if !foundStep {
		t.Errorf("expected write_argocd_secret step; got %v", result.CompletedSteps)
	}
}

// TestRegisterCluster_Kubeconfig_NoManager_NoWrite verifies graceful fallback:
// when no manager is wired (out-of-cluster), RegisterCluster does not attempt
// a direct write and still succeeds.
func TestRegisterCluster_Kubeconfig_NoManager_NoWrite(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	// No SetArgoSecretManager — manager stays nil.

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:       "kind-sharko",
		Provider:   "kubeconfig",
		Kubeconfig: v125TestBearerKubeconfig,
		Addons:     map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("expected success with nil manager, got error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status=success, got %q", result.Status)
	}
	for _, step := range result.CompletedSteps {
		if step == "write_argocd_secret" {
			t.Errorf("write_argocd_secret must not appear when no manager is wired; got %v", result.CompletedSteps)
		}
	}
}

func TestRegisterCluster_Kubeconfig_RejectsCertBased(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:     "cert-based",
		Provider: "kubeconfig",
		Kubeconfig: `apiVersion: v1
kind: Config
current-context: x
clusters:
- name: c
  cluster:
    server: https://10.0.0.1:6443
    certificate-authority-data: dGVzdA==
contexts:
- name: x
  context:
    cluster: c
    user: u
users:
- name: u
  user:
    client-certificate-data: ZmFrZQ==
    client-key-data: ZmFrZQ==
`,
	})
	if err == nil {
		t.Fatal("expected cert-based kubeconfig to be rejected at the orchestrator boundary")
	}
	if !strings.Contains(err.Error(), "bearer-token") {
		t.Errorf("error should mention bearer-token guidance, got: %v", err)
	}
}

func TestRegisterCluster_RejectsUnknownProvider(t *testing.T) {
	// Guards the supportedProviders whitelist: a future typo or accidental
	// removal of "kubeconfig" would surface as this test failing instead
	// of silently breaking the wizard's generic-K8s path.
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	_, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:     "any",
		Provider: "openshift",
	})
	if err == nil {
		t.Fatal("unknown provider must be rejected")
	}
	if !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("error should advertise kubeconfig as a supported value, got: %v", err)
	}
}

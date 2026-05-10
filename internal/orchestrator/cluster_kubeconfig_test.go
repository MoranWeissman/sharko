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
	if _, ok := argocd.registeredClusters["kind-sharko"]; !ok {
		t.Errorf("cluster should be registered in ArgoCD")
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

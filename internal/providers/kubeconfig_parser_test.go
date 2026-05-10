package providers

import (
	"errors"
	"strings"
	"testing"
)

// goodBearerKubeconfig is a minimal kubeconfig that uses bearer-token auth —
// the one and only auth shape V125-1.1 supports.  Hand-written rather than
// generated so the test is independent of any external fixture.
const goodBearerKubeconfig = `apiVersion: v1
kind: Config
current-context: kind-sharko-test
clusters:
- name: kind-sharko-test
  cluster:
    server: https://127.0.0.1:60123
    certificate-authority-data: dGVzdC1jYS1ieXRlcw==
contexts:
- name: kind-sharko-test
  context:
    cluster: kind-sharko-test
    user: kind-sharko-test
users:
- name: kind-sharko-test
  user:
    token: ya29.example-bearer-token
`

const certBasedKubeconfig = `apiVersion: v1
kind: Config
current-context: cert-ctx
clusters:
- name: cert-cluster
  cluster:
    server: https://10.0.0.1:6443
    certificate-authority-data: dGVzdC1jYS1ieXRlcw==
contexts:
- name: cert-ctx
  context:
    cluster: cert-cluster
    user: cert-user
users:
- name: cert-user
  user:
    client-certificate-data: dGVzdC1jZXJ0
    client-key-data: dGVzdC1rZXk=
`

const execPluginKubeconfig = `apiVersion: v1
kind: Config
current-context: eks-ctx
clusters:
- name: eks-cluster
  cluster:
    server: https://abc.gr7.us-east-1.eks.amazonaws.com
    certificate-authority-data: dGVzdC1jYS1ieXRlcw==
contexts:
- name: eks-ctx
  context:
    cluster: eks-cluster
    user: eks-user
users:
- name: eks-user
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws-iam-authenticator
      args:
        - token
        - -i
        - my-cluster
`

// kubeconfigMissingCluster references a cluster name that has no matching
// "clusters:" entry — exercises the "missing cluster section" rejection.
const kubeconfigMissingCluster = `apiVersion: v1
kind: Config
current-context: orphan-ctx
contexts:
- name: orphan-ctx
  context:
    cluster: ghost-cluster
    user: orphan-user
users:
- name: orphan-user
  user:
    token: any-token
`

func TestParseInlineKubeconfig_HappyPath(t *testing.T) {
	kc, err := ParseInlineKubeconfig(goodBearerKubeconfig)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if kc.Server != "https://127.0.0.1:60123" {
		t.Errorf("Server: got %q, want %q", kc.Server, "https://127.0.0.1:60123")
	}
	if kc.Token != "ya29.example-bearer-token" {
		t.Errorf("Token: got %q, want %q", kc.Token, "ya29.example-bearer-token")
	}
	if string(kc.CAData) != "test-ca-bytes" {
		t.Errorf("CAData: got %q, want %q", string(kc.CAData), "test-ca-bytes")
	}
	if len(kc.Raw) == 0 {
		t.Error("Raw should be populated for downstream remoteclient verification")
	}
}

func TestParseInlineKubeconfig_RejectMalformedYAML(t *testing.T) {
	_, err := ParseInlineKubeconfig("not: [valid: yaml")
	if err == nil {
		t.Fatal("expected malformed YAML to be rejected")
	}
	if !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("error should mention kubeconfig, got: %v", err)
	}
}

func TestParseInlineKubeconfig_RejectEmpty(t *testing.T) {
	_, err := ParseInlineKubeconfig("   \n  ")
	if err == nil {
		t.Fatal("expected empty kubeconfig to be rejected")
	}
}

func TestParseInlineKubeconfig_RejectMissingClusterEntry(t *testing.T) {
	_, err := ParseInlineKubeconfig(kubeconfigMissingCluster)
	if err == nil {
		t.Fatal("expected missing-cluster reference to be rejected")
	}
	if !strings.Contains(err.Error(), "missing cluster") {
		t.Errorf("error should describe missing cluster, got: %v", err)
	}
}

func TestParseInlineKubeconfig_RejectCertBasedAuth(t *testing.T) {
	_, err := ParseInlineKubeconfig(certBasedKubeconfig)
	if err == nil {
		t.Fatal("expected cert-based kubeconfig to be rejected (bearer-token only in v1.25)")
	}
	if !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
		t.Errorf("error should wrap ErrUnsupportedKubeconfigAuth, got: %v", err)
	}
	if !strings.Contains(err.Error(), "bearer-token") {
		t.Errorf("error should mention bearer-token guidance, got: %v", err)
	}
	if !strings.Contains(err.Error(), "kubectl create token") {
		t.Errorf("error should include the kubectl create token recipe, got: %v", err)
	}
}

func TestParseInlineKubeconfig_RejectExecPluginAuth(t *testing.T) {
	_, err := ParseInlineKubeconfig(execPluginKubeconfig)
	if err == nil {
		t.Fatal("expected exec-plugin kubeconfig to be rejected (bearer-token only in v1.25)")
	}
	if !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
		t.Errorf("error should wrap ErrUnsupportedKubeconfigAuth, got: %v", err)
	}
	if !strings.Contains(err.Error(), "exec-plugin") {
		t.Errorf("error should mention exec-plugin, got: %v", err)
	}
}

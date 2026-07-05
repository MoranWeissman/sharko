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

// TestParseInlineKubeconfig_AcceptsCertPair pins V2-cleanup-56.1: a
// client-certificate kubeconfig (kind / kubeadm / on-prem) is ACCEPTED and
// the cert pair bytes are carried on the returned Kubeconfig so the ArgoCD
// secret writers can emit the plain-TLS cluster secret shape.
func TestParseInlineKubeconfig_AcceptsCertPair(t *testing.T) {
	kc, err := ParseInlineKubeconfig(certBasedKubeconfig)
	if err != nil {
		t.Fatalf("expected cert-pair kubeconfig to be accepted (V2-cleanup-56.1), got: %v", err)
	}
	if kc.Server != "https://10.0.0.1:6443" {
		t.Errorf("Server: got %q, want %q", kc.Server, "https://10.0.0.1:6443")
	}
	if kc.Token != "" {
		t.Errorf("Token should be empty for a cert-pair kubeconfig, got %q", kc.Token)
	}
	if string(kc.CertData) != "test-cert" {
		t.Errorf("CertData: got %q, want %q", string(kc.CertData), "test-cert")
	}
	if string(kc.KeyData) != "test-key" {
		t.Errorf("KeyData: got %q, want %q", string(kc.KeyData), "test-key")
	}
	if string(kc.CAData) != "test-ca-bytes" {
		t.Errorf("CAData: got %q, want %q", string(kc.CAData), "test-ca-bytes")
	}
	if len(kc.Raw) == 0 {
		t.Error("Raw should be populated for downstream remoteclient verification")
	}
}

// TestParseInlineKubeconfig_RejectHalfCertPair: cert without key must NOT be
// treated as cert auth — it is rejected with guidance, exactly as before
// V2-cleanup-56.1.
func TestParseInlineKubeconfig_RejectHalfCertPair(t *testing.T) {
	halfPair := strings.Replace(certBasedKubeconfig, "    client-key-data: dGVzdC1rZXk=\n", "", 1)
	_, err := ParseInlineKubeconfig(halfPair)
	if err == nil {
		t.Fatal("expected half cert pair (cert without key) to be rejected")
	}
	if !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
		t.Errorf("error should wrap ErrUnsupportedKubeconfigAuth, got: %v", err)
	}
	if !strings.Contains(err.Error(), "incomplete client certificate pair") {
		t.Errorf("error should describe the incomplete pair, got: %v", err)
	}
}

// TestParseInlineKubeconfig_RejectCertByFilePath: cert/key referenced by file
// path cannot be resolved server-side (the files live on the caller's
// machine) — rejected with flatten guidance.
func TestParseInlineKubeconfig_RejectCertByFilePath(t *testing.T) {
	filePathKubeconfig := `apiVersion: v1
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
    client-certificate: /home/someone/.minikube/client.crt
    client-key: /home/someone/.minikube/client.key
`
	_, err := ParseInlineKubeconfig(filePathKubeconfig)
	if err == nil {
		t.Fatal("expected file-path cert kubeconfig to be rejected")
	}
	if !errors.Is(err, ErrUnsupportedKubeconfigAuth) {
		t.Errorf("error should wrap ErrUnsupportedKubeconfigAuth, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--flatten") {
		t.Errorf("error should include the kubectl config view --flatten recipe, got: %v", err)
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

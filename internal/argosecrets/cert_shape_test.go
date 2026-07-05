package argosecrets

// V2-cleanup-56.1 — client-certificate cluster secret shape.
//
// Live bug pinned here: a cluster registered from a secret backend whose
// secret held a CLIENT-CERTIFICATE kubeconfig (kind / kubeadm / on-prem) was
// silently written in the EKS exec shape (execProviderConfig:
// argocd-k8s-auth aws ...) because ClusterSecretSpec had no cert fields and
// buildSecretConfig knew only two shapes (token → bearer, else → exec).
// ArgoCD then ran argocd-k8s-auth from a non-AWS environment → exit code 20
// → connection Failed forever.
//
// The fix adds a third shape with precedence cert pair > token > exec, and
// these tests additionally pin the bearer and exec outputs BYTE-IDENTICAL to
// their pre-56.1 JSON so existing clusters see zero secret churn.

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// certSpec returns a spec shaped like a kind/kubeadm cluster registered from
// a client-certificate kubeconfig: cert pair + CA, no token, no RoleARN.
func certSpec() ClusterSecretSpec {
	return ClusterSecretSpec{
		Name:   "kind-onprem",
		Server: "https://10.0.0.1:6443",
		// base64("fake-cert") / base64("fake-key") / base64("fake-ca-cert")
		CertData: "ZmFrZS1jZXJ0",
		KeyData:  "ZmFrZS1rZXk=",
		CAData:   "ZmFrZS1jYS1jZXJ0",
		Labels:   map[string]string{"addon-monitoring": "enabled"},
	}
}

// TestBuildSecretConfig_CertShape_Golden pins the exact JSON emitted for a
// cert-pair spec: ArgoCD's plain-TLS shape — no execProviderConfig, no
// bearerToken.
func TestBuildSecretConfig_CertShape_Golden(t *testing.T) {
	configJSON, err := buildSecretConfig(certSpec())
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}

	want := `{
  "tlsClientConfig": {
    "insecure": false,
    "certData": "ZmFrZS1jZXJ0",
    "keyData": "ZmFrZS1rZXk=",
    "caData": "ZmFrZS1jYS1jZXJ0"
  }
}`
	if configJSON != want {
		t.Errorf("cert shape JSON drifted.\ngot:\n%s\nwant:\n%s", configJSON, want)
	}
}

// TestBuildSecretConfig_BearerShape_ByteIdenticalPin freezes the bearer-token
// output to its exact pre-56.1 bytes. If this test fails, existing
// bearer-token clusters would see secret churn on the next reconcile tick.
func TestBuildSecretConfig_BearerShape_ByteIdenticalPin(t *testing.T) {
	spec := ClusterSecretSpec{
		Name:   "token-cluster",
		Server: "https://10.0.0.2:6443",
		Token:  "sha256~fake-bearer-token",
		CAData: "ZmFrZS1jYS1jZXJ0",
	}
	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}

	want := `{
  "bearerToken": "sha256~fake-bearer-token",
  "tlsClientConfig": {
    "insecure": false,
    "caData": "ZmFrZS1jYS1jZXJ0"
  }
}`
	if configJSON != want {
		t.Errorf("bearer shape is no longer byte-identical to pre-56.1 output.\ngot:\n%s\nwant:\n%s", configJSON, want)
	}
}

// TestBuildSecretConfig_ExecShape_ByteIdenticalPin freezes the EKS exec
// output to its exact pre-56.1 bytes. If this test fails, existing EKS/IAM
// clusters would see secret churn on the next reconcile tick.
func TestBuildSecretConfig_ExecShape_ByteIdenticalPin(t *testing.T) {
	spec := ClusterSecretSpec{
		Name:    "eks-cluster",
		Server:  "https://ABC123.gr7.us-east-1.eks.amazonaws.com",
		Region:  "us-east-1",
		RoleARN: "arn:aws:iam::123456789012:role/argocd-manager",
		CAData:  "ZmFrZS1jYS1jZXJ0",
	}
	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}

	want := `{
  "execProviderConfig": {
    "command": "argocd-k8s-auth",
    "args": [
      "aws",
      "--cluster-name",
      "eks-cluster",
      "--role-arn",
      "arn:aws:iam::123456789012:role/argocd-manager"
    ],
    "apiVersion": "client.authentication.k8s.io/v1beta1"
  },
  "tlsClientConfig": {
    "insecure": false,
    "caData": "ZmFrZS1jYS1jZXJ0"
  }
}`
	if configJSON != want {
		t.Errorf("exec shape is no longer byte-identical to pre-56.1 output.\ngot:\n%s\nwant:\n%s", configJSON, want)
	}
}

// TestBuildSecretConfig_Precedence walks the full precedence table:
// cert pair > token > exec, with half pairs never taking the cert branch.
func TestBuildSecretConfig_Precedence(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*ClusterSecretSpec)
		wantShape string // "cert", "bearer", "exec"
	}{
		{
			name:      "cert pair only → cert shape",
			mutate:    func(s *ClusterSecretSpec) {},
			wantShape: "cert",
		},
		{
			name: "cert pair AND token → cert wins",
			mutate: func(s *ClusterSecretSpec) {
				s.Token = "some-token"
			},
			wantShape: "cert",
		},
		{
			name: "cert WITHOUT key + token → falls through to bearer",
			mutate: func(s *ClusterSecretSpec) {
				s.KeyData = ""
				s.Token = "some-token"
			},
			wantShape: "bearer",
		},
		{
			name: "key WITHOUT cert + token → falls through to bearer",
			mutate: func(s *ClusterSecretSpec) {
				s.CertData = ""
				s.Token = "some-token"
			},
			wantShape: "bearer",
		},
		{
			name: "cert WITHOUT key, no token → falls through to exec",
			mutate: func(s *ClusterSecretSpec) {
				s.KeyData = ""
			},
			wantShape: "exec",
		},
		{
			name: "key WITHOUT cert, no token → falls through to exec",
			mutate: func(s *ClusterSecretSpec) {
				s.CertData = ""
			},
			wantShape: "exec",
		},
		{
			name: "no cert pair, token → bearer",
			mutate: func(s *ClusterSecretSpec) {
				s.CertData = ""
				s.KeyData = ""
				s.Token = "some-token"
			},
			wantShape: "bearer",
		},
		{
			name: "no cert pair, no token → exec",
			mutate: func(s *ClusterSecretSpec) {
				s.CertData = ""
				s.KeyData = ""
			},
			wantShape: "exec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := certSpec()
			tt.mutate(&spec)
			configJSON, err := buildSecretConfig(spec)
			if err != nil {
				t.Fatalf("buildSecretConfig() error: %v", err)
			}

			hasCert := strings.Contains(configJSON, `"certData"`)
			hasBearer := strings.Contains(configJSON, `"bearerToken"`)
			hasExec := strings.Contains(configJSON, `"execProviderConfig"`)

			var got string
			switch {
			case hasCert && !hasBearer && !hasExec:
				got = "cert"
			case hasBearer && !hasCert && !hasExec:
				got = "bearer"
			case hasExec && !hasCert && !hasBearer:
				got = "exec"
			default:
				t.Fatalf("ambiguous shape (cert=%v bearer=%v exec=%v):\n%s", hasCert, hasBearer, hasExec, configJSON)
			}
			if got != tt.wantShape {
				t.Errorf("shape = %q, want %q\nconfig:\n%s", got, tt.wantShape, configJSON)
			}
		})
	}
}

// TestEnsure_CertPair_RegressionPin is the end-to-end pin of the live bug:
// a cert-pair spec written through Manager.Ensure (the same writer the
// registration adapter and both reconcilers use) must produce a cluster
// Secret whose config carries certData+keyData and NEITHER
// execProviderConfig NOR bearerToken.
func TestEnsure_CertPair_RegressionPin(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)
	spec := certSpec()

	changed, err := mgr.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	if !changed {
		t.Error("Ensure() should report changed=true on create")
	}

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found after Ensure(): %v", err)
	}
	config := secret.StringData["config"]

	if !strings.Contains(config, `"certData": "ZmFrZS1jZXJ0"`) {
		t.Errorf("config missing certData:\n%s", config)
	}
	if !strings.Contains(config, `"keyData": "ZmFrZS1rZXk="`) {
		t.Errorf("config missing keyData:\n%s", config)
	}
	if strings.Contains(config, "execProviderConfig") {
		t.Errorf("LIVE BUG REGRESSED: cert-based cluster written as EKS exec shape:\n%s", config)
	}
	if strings.Contains(config, "bearerToken") {
		t.Errorf("cert-based cluster must not carry bearerToken:\n%s", config)
	}
	if strings.Contains(config, "argocd-k8s-auth") {
		t.Errorf("cert-based cluster must not reference argocd-k8s-auth:\n%s", config)
	}
}

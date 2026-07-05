package clusterreconciler

// V2-cleanup-56.1 — regression pin of the LIVE bug (verified 2026-07-05 on a
// kind environment): a cluster registered from a secret backend whose secret
// holds a CLIENT-CERTIFICATE kubeconfig passed Sharko's own Test/Diagnose
// (client-go consumes the kubeconfig directly) but the ArgoCD cluster Secret
// this reconciler wrote carried execProviderConfig (argocd-k8s-auth aws ...)
// with only caData — no certData/keyData. ArgoCD then tried AWS auth from a
// non-AWS environment → exit code 20 → connection Failed forever.

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// certClusterSecretShape parses the config JSON enough to distinguish the
// three shapes (cert / bearer / exec).
type certClusterSecretShape struct {
	BearerToken        string           `json:"bearerToken"`
	ExecProviderConfig *json.RawMessage `json:"execProviderConfig"`
	TLSClientConfig    struct {
		CertData string `json:"certData"`
		KeyData  string `json:"keyData"`
		CAData   string `json:"caData"`
	} `json:"tlsClientConfig"`
}

// TestPollOnce_CertKubeconfigClusterGetsCertShape — the live-bug pin. Vault
// credentials carrying a client-certificate pair (kind / kubeadm / on-prem
// kubeconfig, no bearer token) must produce a Secret in ArgoCD's plain-TLS
// shape: certData+keyData present, NO execProviderConfig, NO bearerToken.
// Before V2-cleanup-56.1 the cert bytes were dropped and this cluster was
// silently written as EKS exec.
func TestPollOnce_CertKubeconfigClusterGetsCertShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("kind-onprem")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"kind-onprem": {
				Server:   "https://10.0.0.1:6443",
				CAData:   []byte("fake-ca-bytes"),
				CertData: []byte("fake-cert-bytes"),
				KeyData:  []byte("fake-key-bytes"),
				// No Token — pure client-certificate cluster.
			},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "kind-onprem", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting secret: %v", err)
	}
	raw := secret.StringData["config"]
	if raw == "" {
		raw = string(secret.Data["config"])
	}
	var cfg certClusterSecretShape
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parsing config: %v\nconfig=%s", err, raw)
	}

	// base64("fake-cert-bytes") / base64("fake-key-bytes")
	if cfg.TLSClientConfig.CertData != "ZmFrZS1jZXJ0LWJ5dGVz" {
		t.Fatalf("config.tlsClientConfig.certData = %q, want base64 of the vault cert bytes\nconfig=%s", cfg.TLSClientConfig.CertData, raw)
	}
	if cfg.TLSClientConfig.KeyData != "ZmFrZS1rZXktYnl0ZXM=" {
		t.Fatalf("config.tlsClientConfig.keyData = %q, want base64 of the vault key bytes\nconfig=%s", cfg.TLSClientConfig.KeyData, raw)
	}
	if cfg.ExecProviderConfig != nil {
		t.Fatalf("LIVE BUG REGRESSED: cert-based cluster written as EKS exec shape; got %s", string(*cfg.ExecProviderConfig))
	}
	if cfg.BearerToken != "" {
		t.Fatalf("cert-based cluster must NOT get a bearerToken; got %q", cfg.BearerToken)
	}
}

// TestPollOnce_HalfCertPairFallsThroughToExec — cert WITHOUT key must never
// take the cert branch; with no token either, the spec falls through to the
// exec shape exactly as before V2-cleanup-56.1.
func TestPollOnce_HalfCertPairFallsThroughToExec(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("half-pair")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"half-pair": {
				Server:   "https://10.0.0.2:6443",
				CAData:   []byte("fake-ca-bytes"),
				CertData: []byte("fake-cert-bytes"),
				// No KeyData, no Token.
			},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "half-pair", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting secret: %v", err)
	}
	raw := secret.StringData["config"]
	if raw == "" {
		raw = string(secret.Data["config"])
	}
	var cfg certClusterSecretShape
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("parsing config: %v\nconfig=%s", err, raw)
	}

	if cfg.TLSClientConfig.CertData != "" {
		t.Fatalf("half pair must NOT emit certData; got %q", cfg.TLSClientConfig.CertData)
	}
	if cfg.ExecProviderConfig == nil {
		t.Fatal("half pair with no token must keep the execProviderConfig shape (pre-56.1 behavior)")
	}
}

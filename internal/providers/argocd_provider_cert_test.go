package providers

// V2-cleanup-56.1 — reader side of the client-certificate cluster secret
// shape. The writer half (argosecrets.buildSecretConfig cert branch) is
// golden-pinned in internal/argosecrets/cert_shape_test.go; the JSON used
// here mirrors that golden byte-for-byte so the two tests together form the
// write→read round-trip (the packages cannot import each other — argosecrets
// already imports providers).

import (
	"encoding/base64"
	"errors"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	fakeCertB64 = base64.StdEncoding.EncodeToString([]byte("fake-cert"))
	fakeKeyB64  = base64.StdEncoding.EncodeToString([]byte("fake-key"))
)

// TestArgoCD_CertShape_HappyPath covers the plain-TLS (client-certificate)
// shape: tlsClientConfig with certData+keyData+caData, no bearerToken, no
// exec. Asserts CertData/KeyData/CAData are populated (decoded) and Raw
// round-trips through clientcmd.RESTConfigFromKubeConfig with the cert pair
// intact — i.e. a working rest.Config.
func TestArgoCD_CertShape_HappyPath(t *testing.T) {
	// Mirrors argosecrets.buildSecretConfig's cert-branch golden output.
	configJSON := `{
  "tlsClientConfig": {
    "insecure": false,
    "certData": "` + fakeCertB64 + `",
    "keyData": "` + fakeKeyB64 + `",
    "caData": "` + fakeCAB64 + `"
  }
}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"kind-onprem", "kind-onprem",
		"https://10.0.0.1:6443",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	kc, err := provider.GetCredentials("kind-onprem")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if kc.Server != "https://10.0.0.1:6443" {
		t.Errorf("Server = %q, want %q", kc.Server, "https://10.0.0.1:6443")
	}
	if kc.Token != "" {
		t.Errorf("Token = %q, want empty for the cert shape", kc.Token)
	}
	if string(kc.CertData) != "fake-cert" {
		t.Errorf("CertData = %q, want %q (base64-decoded)", string(kc.CertData), "fake-cert")
	}
	if string(kc.KeyData) != "fake-key" {
		t.Errorf("KeyData = %q, want %q (base64-decoded)", string(kc.KeyData), "fake-key")
	}
	if string(kc.CAData) != "fake-ca-data" {
		t.Errorf("CAData = %q, want %q (base64-decoded)", string(kc.CAData), "fake-ca-data")
	}
	if len(kc.Raw) == 0 {
		t.Fatal("Raw kubeconfig is empty")
	}

	// Round-trip check: the synthesized kubeconfig must yield a rest.Config
	// carrying the cert pair + CA — this is exactly what
	// remoteclient.NewClientFromKubeconfig consumes downstream.
	parsed, err := clientcmd.RESTConfigFromKubeConfig(kc.Raw)
	if err != nil {
		t.Fatalf("synthesized Raw kubeconfig failed clientcmd round-trip: %v", err)
	}
	if parsed.Host != "https://10.0.0.1:6443" {
		t.Errorf("round-trip Host = %q, want %q", parsed.Host, "https://10.0.0.1:6443")
	}
	if string(parsed.TLSClientConfig.CertData) != "fake-cert" {
		t.Errorf("round-trip CertData = %q, want %q", string(parsed.TLSClientConfig.CertData), "fake-cert")
	}
	if string(parsed.TLSClientConfig.KeyData) != "fake-key" {
		t.Errorf("round-trip KeyData = %q, want %q", string(parsed.TLSClientConfig.KeyData), "fake-key")
	}
	if string(parsed.TLSClientConfig.CAData) != "fake-ca-data" {
		t.Errorf("round-trip CAData = %q, want %q", string(parsed.TLSClientConfig.CAData), "fake-ca-data")
	}
	if parsed.BearerToken != "" {
		t.Errorf("round-trip BearerToken = %q, want empty", parsed.BearerToken)
	}
}

// TestArgoCD_CertShape_HalfPair_StillUnsupported: certData without keyData
// must NOT take the cert arm — it falls through to the unsupported-auth
// error exactly as before V2-cleanup-56.1 (existing arms untouched).
func TestArgoCD_CertShape_HalfPair_StillUnsupported(t *testing.T) {
	configJSON := `{
  "tlsClientConfig": {
    "insecure": false,
    "certData": "` + fakeCertB64 + `",
    "caData": "` + fakeCAB64 + `"
  }
}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"half-pair", "half-pair",
		"https://10.0.0.9:6443",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("half-pair")
	if err == nil {
		t.Fatal("expected half cert pair to be rejected as unsupported auth")
	}
	if !errors.Is(err, ErrArgoCDProviderUnsupportedAuth) {
		t.Errorf("error should match ErrArgoCDProviderUnsupportedAuth, got: %v", err)
	}
}

// TestArgoCD_CertShape_BadBase64 surfaces a corrupt certData at fetch time
// with a descriptive error instead of failing later inside remoteclient.
func TestArgoCD_CertShape_BadBase64(t *testing.T) {
	configJSON := `{
  "tlsClientConfig": {
    "insecure": false,
    "certData": "!!!not-base64!!!",
    "keyData": "` + fakeKeyB64 + `"
  }
}`

	client := fake.NewSimpleClientset(argoCDSecret(
		"bad-b64", "bad-b64",
		"https://10.0.0.8:6443",
		configJSON,
	))
	provider := newArgoCDProviderWithClient(client, "argocd")

	_, err := provider.GetCredentials("bad-b64")
	if err == nil {
		t.Fatal("expected corrupt certData base64 to error at fetch time")
	}
}

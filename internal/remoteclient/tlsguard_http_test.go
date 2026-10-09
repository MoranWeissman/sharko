package remoteclient

import (
	"context"
	"errors"
	"testing"
)

// tlsguard_http_test.go — task #152 story 152-I finding.
//
// Lane C refuses to deliver a secret over a connection that skips TLS
// certificate checks (insecure-skip-tls-verify / ArgoCD insecure:true). It
// originally reasoned only about the Insecure FLAG, and let a plaintext
// `http://` destination through untouched — even though http is strictly
// worse: the value travels in the clear with no MITM even needed. These
// tests pin the closed gap and prove the original skip-verify and https
// behavior is unchanged.

func kubeconfigWithServer(server, extra string) []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: ` + server + extra + `
  name: c
contexts:
- context:
    cluster: c
    user: u
  name: ctx
current-context: ctx
users:
- name: u
  user:
    token: tok
`)
}

func TestCheckDestinationTLS_RefusesPlaintextHTTP(t *testing.T) {
	kc := kubeconfigWithServer("http://plain.example.com", "")
	if err := CheckDestinationTLS(kc); !errors.Is(err, ErrUnverifiedDestination) {
		t.Fatalf("expected ErrUnverifiedDestination for an http:// destination, got %v", err)
	}
}

func TestNewClientFromKubeconfig_MarksPlaintextHTTPUnverified(t *testing.T) {
	kc := kubeconfigWithServer("http://plain.example.com", "")
	client, err := NewClientFromKubeconfig(kc)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	if !destinationUnverified(client) {
		t.Fatal("http:// client should be marked unverified so EnsureSecret refuses it")
	}
}

func TestEnsureSecret_RefusesPlaintextHTTP_WithoutDialing(t *testing.T) {
	kc := kubeconfigWithServer("http://plain.example.com", "")
	client, err := NewClientFromKubeconfig(kc)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	// If the gate holds, EnsureSecret returns ErrUnverifiedDestination
	// before any API call — so this never depends on network/DNS.
	err = EnsureSecret(context.Background(), client, "ns", "n",
		map[string][]byte{"k": []byte("fake-value")}, nil)
	if !errors.Is(err, ErrUnverifiedDestination) {
		t.Fatalf("EnsureSecret should refuse an http:// destination with ErrUnverifiedDestination, got %v", err)
	}
}

func TestCheckDestinationTLS_AllowsHTTPS(t *testing.T) {
	// A plain https destination (no skip-verify) must still be allowed — the
	// fix only ADDS a refusal for explicit http, it does not tighten https.
	kc := kubeconfigWithServer("https://real.example.com", "")
	if err := CheckDestinationTLS(kc); err != nil {
		t.Fatalf("https destination should be allowed, got %v", err)
	}
	client, err := NewClientFromKubeconfig(kc)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	if destinationUnverified(client) {
		t.Fatal("a plain https client must not be marked unverified")
	}
}

func TestCheckDestinationTLS_StillRefusesSkipVerify(t *testing.T) {
	// Pre-existing behavior preserved: https + insecure-skip-tls-verify.
	kc := kubeconfigWithServer("https://real.example.com", "\n    insecure-skip-tls-verify: true")
	if err := CheckDestinationTLS(kc); !errors.Is(err, ErrUnverifiedDestination) {
		t.Fatalf("expected ErrUnverifiedDestination for skip-verify, got %v", err)
	}
}

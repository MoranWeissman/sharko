//go:build !sharko_unverified_dest_ok

package remoteclient

import (
	"context"
	"errors"
	"testing"
)

// tlsguard_guard_test.go — what a NORMAL build (no bypass tag) does. These
// run in the default `go test ./...` and would rightly fail under
// `-tags sharko_unverified_dest_ok`, so they carry the inverse tag.

// TestBypassIsOffInANormalBuild is the direct proof for the story's
// "cannot be switched on in a normal production build": in any build that
// did not pass -tags sharko_unverified_dest_ok, the bypass is a
// compile-time false. There is no runtime knob to test because none
// exists — no env var, no config field, no API.
func TestBypassIsOffInANormalBuild(t *testing.T) {
	if allowUnverifiedDestinations {
		t.Fatal("allowUnverifiedDestinations = true in a build without the sharko_unverified_dest_ok tag — the production guard is off")
	}
}

// TestCheckDestinationTLS_RefusesSkipVerifyKubeconfig covers the first of
// the two entry shapes: a destination kubeconfig that says
// insecure-skip-tls-verify: true. (The second shape — ArgoCD cluster
// config with insecure: true — becomes this same shape before it reaches
// this package: internal/providers/argocd_provider.go writes
// insecure-skip-tls-verify: true into the kubeconfig it synthesizes.)
func TestCheckDestinationTLS_RefusesSkipVerifyKubeconfig(t *testing.T) {
	err := CheckDestinationTLS([]byte(insecureKubeconfigYAML))
	if !errors.Is(err, ErrUnverifiedDestination) {
		t.Fatalf("CheckDestinationTLS(insecure kubeconfig) = %v, want ErrUnverifiedDestination", err)
	}
}

// TestCheckDestinationTLS_AllowsVerifiedKubeconfig pins that an ordinary
// kubeconfig — one that verifies certificates — is untouched.
func TestCheckDestinationTLS_AllowsVerifiedKubeconfig(t *testing.T) {
	if err := CheckDestinationTLS([]byte(secureKubeconfigYAML)); err != nil {
		t.Fatalf("CheckDestinationTLS(verified kubeconfig) = %v, want nil", err)
	}
}

// TestNewClientFromKubeconfig_MarksSkipVerifyDestination proves the full
// wire-up: a client built from a skip-verify kubeconfig comes back marked,
// and EnsureSecret through it refuses with ErrUnverifiedDestination —
// without any network traffic, because the refusal precedes every API
// call (the server in the fixture does not exist).
func TestNewClientFromKubeconfig_MarksSkipVerifyDestination(t *testing.T) {
	client, err := NewClientFromKubeconfig([]byte(insecureKubeconfigYAML))
	if err != nil {
		t.Fatalf("NewClientFromKubeconfig(insecure kubeconfig): %v", err)
	}
	if !destinationUnverified(client) {
		t.Fatal("client built from a skip-verify kubeconfig is not marked")
	}
	err = EnsureSecret(context.Background(), client, "monitoring", "datadog-secret",
		map[string][]byte{"api-key": []byte("fake-value")}, nil)
	if !errors.Is(err, ErrUnverifiedDestination) {
		t.Fatalf("EnsureSecret over a skip-verify connection: err = %v, want ErrUnverifiedDestination", err)
	}
}

// TestNewClientFromKubeconfig_VerifiedDestinationNotMarked pins the other
// side: a kubeconfig that verifies certificates yields an unmarked client.
func TestNewClientFromKubeconfig_VerifiedDestinationNotMarked(t *testing.T) {
	client, err := NewClientFromKubeconfig([]byte(secureKubeconfigYAML))
	if err != nil {
		t.Fatalf("NewClientFromKubeconfig(verified kubeconfig): %v", err)
	}
	if destinationUnverified(client) {
		t.Fatal("client built from a certificate-verifying kubeconfig is marked as unverified — the guard is refusing too broadly")
	}
}

//go:build sharko_unverified_dest_ok

package remoteclient

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// tlsguard_bypass_test.go — what a TEST build compiled with
// `-tags sharko_unverified_dest_ok` does. These do not run in the default
// `go test ./...`; run them explicitly:
//
//	go test -tags sharko_unverified_dest_ok ./internal/remoteclient/
//
// Together with tlsguard_guard_test.go's inverse-tagged assertions, this
// pins the bypass to exactly the tagged build path and nowhere else.

// TestBypass_IsOnOnlyInTheTaggedBuild documents which build this is.
func TestBypass_IsOnOnlyInTheTaggedBuild(t *testing.T) {
	if !allowUnverifiedDestinations {
		t.Fatal("allowUnverifiedDestinations = false under -tags sharko_unverified_dest_ok")
	}
}

// TestBypass_SkipVerifyDestinationDeliverable proves the bypass actually
// bypasses: with the tag on, a skip-verify kubeconfig is not refused by
// CheckDestinationTLS, the built client is not marked, and EnsureSecret
// through an (unmarked) client writes normally — which is what lets a
// kind-based e2e harness or a demo estate deliver to clusters that only
// speak self-signed TLS.
func TestBypass_SkipVerifyDestinationDeliverable(t *testing.T) {
	if err := CheckDestinationTLS([]byte(insecureKubeconfigYAML)); err != nil {
		t.Fatalf("CheckDestinationTLS(insecure kubeconfig) under the bypass tag = %v, want nil", err)
	}
	client, err := NewClientFromKubeconfig([]byte(insecureKubeconfigYAML))
	if err != nil {
		t.Fatalf("NewClientFromKubeconfig(insecure kubeconfig): %v", err)
	}
	if destinationUnverified(client) {
		t.Fatal("client is marked as unverified even though the bypass tag is on")
	}
	// And the write path is genuinely open: EnsureSecret against a fake
	// clientset (stand-in for the reachable kind cluster the tagged build
	// exists for) writes without a refusal.
	fakeClient := fake.NewSimpleClientset()
	if err := EnsureSecret(context.Background(), fakeClient, "monitoring", "datadog-secret",
		map[string][]byte{"api-key": []byte("fake-value")}, nil); err != nil {
		t.Fatalf("EnsureSecret under the bypass tag: %v", err)
	}
	if _, err := fakeClient.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); err != nil {
		t.Fatalf("secret was not created under the bypass tag: %v", err)
	}
}

//go:build e2e

package harness

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestHarnessKindMultiCluster is the hello-world test that proves the
// harness primitives work end-to-end against a real Docker daemon. It:
//
//  1. Cleans up any stale e2e clusters from prior failed runs.
//  2. Provisions 1 mgmt + 1 target cluster in parallel.
//  3. Verifies kubectl reachability + at least one Ready node per cluster.
//  4. Confirms the sentinel label is present on the mgmt cluster's nodes.
//  5. Tears both clusters down via t.Cleanup.
//
// Skips (rather than fails) when kind / kubectl / docker are not on PATH —
// keeps the suite friendly to CI matrix entries without container tooling.
func TestHarnessKindMultiCluster(t *testing.T) {
	if _, err := exec.LookPath("kind"); err != nil {
		t.Skip("kind binary not found in PATH")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl binary not found in PATH")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker binary not found in PATH")
	}

	// Defensive: clean up any stragglers from prior failed runs. Safe — only
	// touches clusters that carry the e2e.sharko.io/test=true sentinel.
	DestroyAllStaleE2EClusters(t)

	clusters := ProvisionTopology(t, ProvisionRequest{NumTargets: 1})
	t.Cleanup(func() { DestroyTopology(t, clusters) })

	if got, want := len(clusters), 2; got != want {
		t.Fatalf("expected %d clusters (mgmt + 1 target), got %d", want, got)
	}
	if clusters[0].Role != "mgmt" {
		t.Fatalf("expected clusters[0].Role==mgmt, got %q", clusters[0].Role)
	}
	if clusters[1].Role != "target-1" {
		t.Fatalf("expected clusters[1].Role==target-1, got %q", clusters[1].Role)
	}

	// Verify both clusters reachable via kubectl.
	for _, c := range clusters {
		WaitClusterReady(t, c, 90*time.Second)
		out, err := exec.Command("kubectl",
			"--kubeconfig", c.Kubeconfig,
			"get", "nodes", "-o", "name",
		).CombinedOutput()
		if err != nil {
			t.Fatalf("kubectl get nodes failed for %s: %v\noutput: %s", c.Name, err, out)
		}
		if strings.TrimSpace(string(out)) == "" {
			t.Fatalf("no nodes returned for %s", c.Name)
		}
	}

	// Verify sentinel labels are applied (probe the mgmt cluster — same code
	// path applies to every cluster).
	out, err := exec.Command("kubectl",
		"--kubeconfig", clusters[0].Kubeconfig,
		"get", "nodes",
		"-o", `jsonpath={.items[0].metadata.labels.e2e\.sharko\.io/test}`,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("kubectl jsonpath probe failed: %v\noutput: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "true" {
		t.Fatalf("expected sentinel label e2e.sharko.io/test=true, got %q", got)
	}
}

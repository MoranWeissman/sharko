//go:build e2e

package harness

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	defaultArgoCDNamespace = "argocd"
	defaultArgoCDInstallURL = "https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml"
	defaultArgoCDWait       = 3 * time.Minute
)

// InstallArgoCD installs the standard upstream ArgoCD distribution into the
// given kind cluster. Idempotent — silently succeeds when ArgoCD is already
// installed.
//
// The implementation mirrors tests/e2e/setup.sh:
//   - kubectl create namespace argocd      (ignore "already exists")
//   - kubectl apply --server-side --force-conflicts -n argocd -f <install.yaml>
//   - kubectl wait --for=condition=available --timeout=120s deployment/argocd-server -n argocd
//
// Server-side apply is mandatory because the ApplicationSet CRD metadata
// exceeds the 256 KiB limit of client-side apply's last-applied-configuration
// annotation. See V124-3.6 for the original fix in the bash harness.
//
// NOT invoked by 7-1.2's hello-world tests (too slow — ~90s); lives in the
// harness so downstream story 7-1.4+ lifecycle tests can reuse it.
//
// Calls t.Fatalf on failure — does not return on error.
func InstallArgoCD(t *testing.T, cluster KindCluster) {
	t.Helper()
	if cluster.Kubeconfig == "" {
		t.Fatalf("InstallArgoCD: cluster.Kubeconfig is empty (cluster=%s)", cluster.Name)
	}

	kubectl := defaultKubectlBinFromEnv()
	ns := defaultArgoCDNamespace

	// Idempotent namespace create.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := exec.CommandContext(ctx, kubectl,
			"--kubeconfig", cluster.Kubeconfig,
			"create", "namespace", ns,
		).CombinedOutput()
		cancel()
		if err != nil && !strings.Contains(string(out), "AlreadyExists") {
			t.Fatalf("InstallArgoCD: create namespace %s: %v\noutput: %s", ns, err, out)
		}
	}

	// Server-side apply of the upstream install manifest.
	{
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		out, err := exec.CommandContext(ctx, kubectl,
			"--kubeconfig", cluster.Kubeconfig,
			"apply",
			"--server-side", "--force-conflicts",
			"-n", ns,
			"-f", defaultArgoCDInstallURL,
		).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("InstallArgoCD: apply manifest on %s: %v\noutput: %s", cluster.Name, err, out)
		}
		t.Logf("harness: argocd manifest applied on %s", cluster.Name)
	}

	// Wait for argocd-server deployment to become available.
	{
		ctx, cancel := context.WithTimeout(context.Background(), defaultArgoCDWait)
		out, err := exec.CommandContext(ctx, kubectl,
			"--kubeconfig", cluster.Kubeconfig,
			"wait",
			"--for=condition=available",
			"--timeout=120s",
			"-n", ns,
			"deployment/argocd-server",
		).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("InstallArgoCD: wait for argocd-server on %s: %v\noutput: %s", cluster.Name, err, out)
		}
		t.Logf("harness: argocd-server available on %s", cluster.Name)
	}
}

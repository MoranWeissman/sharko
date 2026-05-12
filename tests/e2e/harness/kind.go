//go:build e2e

package harness

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// Sentinel labels applied to every node in every harness-provisioned cluster.
// These are the load-bearing safety contract for DestroyAllStaleE2EClusters.
// See doc.go for the full invariant description.
const (
	LabelTest   = "e2e.sharko.io/test"
	LabelRunID  = "e2e.sharko.io/run-id"
	LabelRole   = "e2e.sharko.io/role"
	SentinelOn  = "true"
	clusterName = "sharko-e2e"

	defaultKindImage     = "kindest/node:v1.31.0"
	defaultKindBin       = "kind"
	defaultKubectlBin    = "kubectl"
	defaultProvisionWait = 5 * time.Minute
)

// KindCluster represents a provisioned kind cluster owned by the e2e harness.
type KindCluster struct {
	// Name is the kind cluster name without the "kind-" context prefix
	// (e.g. "sharko-e2e-mgmt-1234567890").
	Name string
	// Context is the kubectl context name for this cluster (e.g.
	// "kind-sharko-e2e-mgmt-1234567890"). Always equals "kind-" + Name.
	Context string
	// Kubeconfig is the absolute path to a per-cluster kubeconfig file
	// written under t.TempDir() during provisioning.
	Kubeconfig string
	// Role is "mgmt" for the management cluster (index 0 in the returned
	// slice) or "target-N" (1-indexed) for the Nth target cluster.
	Role string
	// RunID is the shared run identifier across every cluster from a
	// single ProvisionTopology call. Used as the value of the
	// e2e.sharko.io/run-id node label.
	RunID string
}

// ProvisionRequest declares the desired e2e topology.
//
// Zero-value fields fall back to environment variables (E2E_KIND_IMAGE,
// E2E_KIND_BIN, E2E_KUBECTL_BIN) and then to package defaults — see doc.go.
type ProvisionRequest struct {
	// NumTargets is the number of target clusters to spin up in addition
	// to the single mgmt cluster. Must be >= 0.
	NumTargets int
	// Image is the kindest/node image. Defaults to the package's
	// supported version when empty (or to E2E_KIND_IMAGE if set).
	Image string
	// KindBinary is the path to the kind binary. Defaults to "kind".
	KindBinary string
	// KubectlBinary is the path to the kubectl binary. Defaults to "kubectl".
	KubectlBinary string
}

// resolved returns a ProvisionRequest with environment-variable and default
// fallbacks applied for every empty field.
func (r ProvisionRequest) resolved() ProvisionRequest {
	out := r
	if out.Image == "" {
		if v := os.Getenv("E2E_KIND_IMAGE"); v != "" {
			out.Image = v
		} else {
			out.Image = defaultKindImage
		}
	}
	if out.KindBinary == "" {
		if v := os.Getenv("E2E_KIND_BIN"); v != "" {
			out.KindBinary = v
		} else {
			out.KindBinary = defaultKindBin
		}
	}
	if out.KubectlBinary == "" {
		if v := os.Getenv("E2E_KUBECTL_BIN"); v != "" {
			out.KubectlBinary = v
		} else {
			out.KubectlBinary = defaultKubectlBin
		}
	}
	return out
}

// generateRunID returns a short identifier (~10 chars) suitable for embedding
// in a kind cluster name. Format: "<unix-mod-1e6><rand-0000-9999>".
//
// Kind cluster names are capped at 63 chars — keeping the run ID short
// leaves headroom for the "sharko-e2e-target-N-" prefix (~21 chars).
func generateRunID() string {
	// nolint:gosec // not used for cryptographic purposes — uniqueness only
	return fmt.Sprintf("%d%d", time.Now().Unix()%1000000, rand.Intn(10000))
}

// roleFor returns the role string for cluster index i in a topology with
// numTargets targets. i==0 is "mgmt"; i in [1, numTargets] is "target-i".
func roleFor(i int) string {
	if i == 0 {
		return "mgmt"
	}
	return fmt.Sprintf("target-%d", i)
}

// nameFor returns the kind cluster name for a given role and runID.
func nameFor(role, runID string) string {
	return fmt.Sprintf("%s-%s-%s", clusterName, role, runID)
}

// ProvisionTopology spins up 1 mgmt + req.NumTargets target kind clusters with
// a unique shared RunID. Returns []KindCluster (index 0 = mgmt, indices 1..N
// = targets in declaration order).
//
// Sentinel labels (e2e.sharko.io/test=true, e2e.sharko.io/run-id=<RunID>,
// e2e.sharko.io/role=<role>) are applied to every node so that
// DestroyAllStaleE2EClusters can safely identify harness-owned clusters.
//
// Provisioning happens in parallel via errgroup. On any failure, every
// cluster successfully created up to that point is torn down before the
// error is reported, leaving the host in a clean state.
//
// All temp files (per-cluster kind configs and kubeconfigs) live under
// t.TempDir() so they are removed automatically when the test ends.
//
// Calls t.Fatalf on failure — does not return on error.
func ProvisionTopology(t *testing.T, req ProvisionRequest) []KindCluster {
	t.Helper()
	if req.NumTargets < 0 {
		t.Fatalf("ProvisionTopology: NumTargets must be >= 0, got %d", req.NumTargets)
	}
	req = req.resolved()
	runID := generateRunID()
	tempDir := t.TempDir()

	total := 1 + req.NumTargets
	clusters := make([]KindCluster, total)
	for i := 0; i < total; i++ {
		role := roleFor(i)
		name := nameFor(role, runID)
		clusters[i] = KindCluster{
			Name:       name,
			Context:    "kind-" + name,
			Kubeconfig: filepath.Join(tempDir, name+".kubeconfig"),
			Role:       role,
			RunID:      runID,
		}
	}

	t.Logf("harness: provisioning %d kind cluster(s) [run-id=%s]: %s",
		total, runID, strings.Join(clusterNames(clusters), ", "))

	// Provision in parallel with a shared overall timeout. Use t.Context
	// when available so cancellation propagates from `go test -timeout`.
	gctx, cancel := context.WithTimeout(context.Background(), defaultProvisionWait)
	defer cancel()
	g, gctx := errgroup.WithContext(gctx)

	// Track which indices succeeded so we can clean up on partial failure.
	var doneMu sync.Mutex
	done := make(map[int]bool)

	for i := range clusters {
		i := i
		c := clusters[i]
		g.Go(func() error {
			if err := provisionOne(gctx, t, c, req); err != nil {
				return fmt.Errorf("provision %s: %w", c.Name, err)
			}
			doneMu.Lock()
			done[i] = true
			doneMu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		// Roll back any clusters that did come up.
		var rollback []KindCluster
		doneMu.Lock()
		for i := range clusters {
			if done[i] {
				rollback = append(rollback, clusters[i])
			}
		}
		doneMu.Unlock()
		if len(rollback) > 0 {
			t.Logf("harness: rolling back %d partially-provisioned cluster(s) after error", len(rollback))
			DestroyTopology(t, rollback)
		}
		t.Fatalf("ProvisionTopology failed: %v", err)
	}

	t.Logf("harness: provisioned %d cluster(s) successfully [run-id=%s]", total, runID)
	return clusters
}

// clusterNames is a small helper to produce a comma-separated name list for
// logging.
func clusterNames(clusters []KindCluster) []string {
	out := make([]string, len(clusters))
	for i, c := range clusters {
		out[i] = c.Name
	}
	return out
}

// provisionOne creates a single kind cluster and applies sentinel labels.
// Returns an error rather than failing the test directly so the caller can
// orchestrate rollback across parallel provisioning.
func provisionOne(ctx context.Context, t *testing.T, c KindCluster, req ProvisionRequest) error {
	t.Helper()
	configPath := WriteKindConfig(t, c)

	// kind create cluster --name <name> --image <image> --config <cfg>
	//                     --kubeconfig <kc> --wait 60s
	args := []string{
		"create", "cluster",
		"--name", c.Name,
		"--image", req.Image,
		"--config", configPath,
		"--kubeconfig", c.Kubeconfig,
		"--wait", "60s",
	}
	cmd := exec.CommandContext(ctx, req.KindBinary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kind create cluster: %w\noutput: %s", err, out)
	}

	// Apply the sentinel labels to every node. Using `kubectl label node --all`
	// keeps us robust to multi-node configs even though our defaults are
	// single-node. Label values are stable for the cluster's lifetime.
	for _, label := range []string{
		fmt.Sprintf("%s=%s", LabelTest, SentinelOn),
		fmt.Sprintf("%s=%s", LabelRunID, c.RunID),
		fmt.Sprintf("%s=%s", LabelRole, c.Role),
	} {
		labelCmd := exec.CommandContext(ctx,
			req.KubectlBinary,
			"--kubeconfig", c.Kubeconfig,
			"label", "node", "--all",
			label,
			"--overwrite",
		)
		if out, err := labelCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("kubectl label node --all %s: %w\noutput: %s", label, err, out)
		}
	}

	return nil
}

// DestroyTopology destroys the provided clusters (idempotent — silently
// succeeds for clusters that no longer exist on the host).
//
// Intended for use as t.Cleanup(func() { DestroyTopology(t, clusters) }).
// Logs every destroy attempt to t.Log so flaky cleanup is debuggable.
//
// Never calls t.Fatal — cleanup failures are logged but do not fail the
// test. The caller's primary assertions take precedence.
func DestroyTopology(t *testing.T, clusters []KindCluster) {
	t.Helper()
	if len(clusters) == 0 {
		return
	}
	req := ProvisionRequest{}.resolved() // pick up env-var binary overrides

	// Snapshot the live cluster list so we can short-circuit deletes for
	// names that already vanished (idempotency).
	live := liveKindClusters(req.KindBinary)
	liveSet := make(map[string]bool, len(live))
	for _, n := range live {
		liveSet[n] = true
	}

	for _, c := range clusters {
		if !liveSet[c.Name] {
			t.Logf("harness: destroy %s — already absent, skipping", c.Name)
			continue
		}
		t.Logf("harness: destroying kind cluster %s", c.Name)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cmd := exec.CommandContext(ctx, req.KindBinary, "delete", "cluster", "--name", c.Name)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Logf("harness: WARNING destroy %s failed: %v\noutput: %s", c.Name, err, out)
			continue
		}
		t.Logf("harness: destroyed %s", c.Name)
	}
}

// DestroyAllStaleE2EClusters scans every kind cluster on the host and
// destroys any whose nodes carry the e2e.sharko.io/test=true label
// (regardless of run-id). Used at suite startup to mop up stragglers from
// prior failed runs.
//
// SAFETY: only destroys clusters that carry the sentinel label. Clusters
// without the label (the maintainer's dev clusters, sharko-target-N from
// sharko-dev.sh, etc.) are never touched.
//
// Errors during enumeration or label probing are logged but do not fail
// the test — this is a best-effort cleanup helper.
func DestroyAllStaleE2EClusters(t *testing.T) {
	t.Helper()
	req := ProvisionRequest{}.resolved()

	clusters := liveKindClusters(req.KindBinary)
	if len(clusters) == 0 {
		t.Logf("harness: no kind clusters present, no stale cleanup needed")
		return
	}

	for _, name := range clusters {
		if !hasSentinelLabel(t, name, req) {
			// Not a harness cluster — leave alone. No log here to avoid
			// noisy output when the maintainer has many dev clusters.
			continue
		}
		t.Logf("harness: stale cleanup — destroying e2e cluster %s", name)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cmd := exec.CommandContext(ctx, req.KindBinary, "delete", "cluster", "--name", name)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Logf("harness: WARNING stale destroy %s failed: %v\noutput: %s", name, err, out)
		}
	}
}

// liveKindClusters returns the names of every kind cluster currently on the
// host, or nil on enumeration failure (treated as "no clusters" for
// best-effort cleanup contexts).
func liveKindClusters(kindBin string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, kindBin, "get", "clusters").CombinedOutput()
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		// kind prints "No kind clusters found." (to stderr) when empty;
		// CombinedOutput merges streams, so filter the message defensively.
		if line == "" || strings.HasPrefix(line, "No kind clusters") {
			continue
		}
		names = append(names, line)
	}
	return names
}

// hasSentinelLabel returns true iff at least one node in the cluster carries
// e2e.sharko.io/test=true. Probes via `kind get kubeconfig` piped to `kubectl
// get nodes -o jsonpath`. Returns false on any error (defensive — a probe
// failure means we cannot prove the sentinel is present, so leave the
// cluster alone).
func hasSentinelLabel(t *testing.T, clusterName string, req ProvisionRequest) bool {
	t.Helper()

	// Get an in-memory kubeconfig for this cluster.
	kcCtx, kcCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer kcCancel()
	kcOut, err := exec.CommandContext(kcCtx, req.KindBinary,
		"get", "kubeconfig", "--name", clusterName).Output()
	if err != nil {
		return false
	}

	// Write to a tempfile so kubectl can read it.
	f, err := os.CreateTemp("", "harness-probe-*.kubeconfig")
	if err != nil {
		return false
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(kcOut); err != nil {
		_ = f.Close()
		return false
	}
	if err := f.Close(); err != nil {
		return false
	}

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer probeCancel()
	// jsonpath dot in the label key MUST be escaped: e2e\.sharko\.io/test
	jsonpath := `{.items[*].metadata.labels.e2e\.sharko\.io/test}`
	out, err := exec.CommandContext(probeCtx, req.KubectlBinary,
		"--kubeconfig", f.Name(),
		"get", "nodes",
		"-o", "jsonpath="+jsonpath,
	).Output()
	if err != nil {
		return false
	}
	// jsonpath returns space-separated values across all nodes; any "true"
	// in there is enough to confirm the sentinel.
	for _, v := range strings.Fields(string(out)) {
		if v == SentinelOn {
			return true
		}
	}
	return false
}

// WaitClusterReady blocks until kubectl can list nodes on the cluster AND at
// least one node reports Ready=True. Default timeout 90s when timeout==0.
//
// Calls t.Fatalf on timeout — does not return on error.
func WaitClusterReady(t *testing.T, cluster KindCluster, timeout time.Duration) {
	t.Helper()
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second

	var lastErr error
	var lastOut string
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, err := exec.CommandContext(ctx, defaultKubectlBinFromEnv(),
			"--kubeconfig", cluster.Kubeconfig,
			"get", "nodes",
			"-o", `jsonpath={range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}`,
		).CombinedOutput()
		cancel()
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if strings.TrimSpace(line) == "True" {
					return
				}
			}
			lastErr = fmt.Errorf("no node reported Ready=True yet")
			lastOut = string(out)
		} else {
			lastErr = err
			lastOut = string(out)
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("WaitClusterReady(%s) timed out after %s: %v\nlast output: %s",
		cluster.Name, timeout, lastErr, lastOut)
}

// defaultKubectlBinFromEnv resolves the kubectl binary from env or default.
// Used by WaitClusterReady which does not receive a ProvisionRequest.
func defaultKubectlBinFromEnv() string {
	if v := os.Getenv("E2E_KUBECTL_BIN"); v != "" {
		return v
	}
	return defaultKubectlBin
}

// WriteKindConfig generates a kind cluster config YAML for a given role and
// writes it to a tempfile under t.TempDir(). Returns the absolute path.
//
// Mgmt clusters get extraPortMappings for ports 8080 (sharko HTTP) and
// 30000-30099 (NodePort range for fixtures). Targets get a minimal config
// with one control-plane node.
//
// The config is hand-written YAML rather than gopkg.in/yaml.v3 marshaling
// to keep the output stable + readable in test logs.
func WriteKindConfig(t *testing.T, cluster KindCluster) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("kind: Cluster\n")
	b.WriteString("apiVersion: kind.x-k8s.io/v1alpha4\n")
	b.WriteString("name: " + cluster.Name + "\n")
	b.WriteString("nodes:\n")
	b.WriteString("- role: control-plane\n")

	if cluster.Role == "mgmt" {
		b.WriteString("  extraPortMappings:\n")
		// Sharko HTTP port — used in story 7-1.2 to reach sharko via host.
		b.WriteString("  - containerPort: 8080\n")
		b.WriteString("    hostPort: 8080\n")
		b.WriteString("    protocol: TCP\n")
		// NodePort range for fixtures (e.g. fake git server, mock providers).
		// Range is small (100 ports) to keep kind happy on Docker Desktop.
		for port := 30000; port <= 30099; port++ {
			fmt.Fprintf(&b, "  - containerPort: %d\n", port)
			fmt.Fprintf(&b, "    hostPort: %d\n", port)
			b.WriteString("    protocol: TCP\n")
		}
	}

	path := filepath.Join(t.TempDir(), "kind-"+cluster.Name+".yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("WriteKindConfig: %v", err)
	}
	return path
}

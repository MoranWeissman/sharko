//go:build e2e

package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Story V125-1-13.1 — Helm-install harness primitive
//
// installSharkoHelm boots a real Sharko pod inside a kind cluster via the
// chart at charts/sharko/. This is the foundation that Story 13.3 wires into
// SharkoModeHelm in sharko.go to replace today's t.Skip stub.
//
// Implementation pattern: shell out via exec.CommandContext to docker / kind /
// helm / kubectl. This matches the existing harness convention (kind.go,
// argocd.go) — the Helm Go SDK and kind Go SDK are heavier than what we
// need and would diverge from the rest of the harness. The plan §13.1
// explicitly accepts shell-out: "If the Go SDK feels heavy, shelling out via
// exec.CommandContext is acceptable (matches existing harness patterns —
// verify against kind.go)."
//
// Caching: when SHARKO_E2E_IMAGE_TAG is set AND `docker image inspect
// sharko:<tag>` succeeds, the docker build step is skipped — the env var
// certifies the image is already present in the local Docker host. The
// kind load step ALWAYS runs unconditionally: each kind cluster has its
// own containerd image store and starts empty per fresh provision, and
// `kind load docker-image` is fast (~5-10s) and idempotent. Skipping the
// build is the load-bearing optimisation for `make test-e2e-helm` — the
// build dominates total test time (~60-90s on a warm Docker cache,
// several minutes cold), whereas the load is negligible.
//
// Cleanup: helm uninstall + kubectl delete ns are registered via t.Cleanup
// at the end of the install path. Both are best-effort — failures are logged
// via t.Logf but never fail the test (cleanup-on-failure must not mask the
// underlying assertion failure).
// ---------------------------------------------------------------------------

const (
	defaultHelmReleaseName = "sharko"
	defaultHelmNamespace   = "sharko"
	defaultHelmBin         = "helm"
	defaultDockerBin       = "docker"
	defaultRolloutWait     = 180 * time.Second
	defaultHelmInstallWait = 5 * time.Minute
)

// HelmHandle describes a Sharko release installed into a kind cluster via
// Helm. Returned by installSharkoHelm. Story 13.3 wires this through
// StartSharko's mode selector; Story 13.2 layers a port-forward + auth
// bundle on top to give tests a typed API client.
//
// All fields are populated by installSharkoHelm; Namespace and Deployment
// fall back to "sharko" when HelmInstallConfig leaves them empty.
type HelmHandle struct {
	// Namespace is the K8s namespace where Sharko was installed
	// (defaults to "sharko").
	Namespace string
	// Deployment is the deployment name (always equals the helm release
	// name; defaults to "sharko").
	Deployment string
	// Service is the service name (always equals the helm release name;
	// defaults to "sharko").
	Service string
	// BaseURL is the in-cluster service URL —
	// "http://<svc>.<ns>.svc.cluster.local:<port>" — for use by Story
	// 13.2's port-forward primitive (the port-forward target uses
	// svc.<ns> and a local random port).
	BaseURL string
	// ImageTag is the e2e-<short> tag built and loaded into the kind
	// cluster (e.g. "e2e-a1b2c3d4"). The image repository is always
	// "sharko" for the e2e harness (Helm value image.repository=sharko).
	ImageTag string
	// ReleaseName is the helm release name (always "sharko" for now;
	// kept as a field so the cleanup hook reads consistently).
	ReleaseName string
	// KindClusterName is the kind cluster the release lives in. Captured
	// for logging + uninstall context.
	KindClusterName string
	// Kubeconfig is the kubeconfig path used for kubectl/helm operations
	// against the kind cluster. Captured for the cleanup hook so
	// deletion does not depend on the test's kubeconfig environment.
	Kubeconfig string
}

// HelmInstallConfig declares knobs for installSharkoHelm.
//
// Zero-value defaults are sane for the common e2e topology — a single mgmt
// kind cluster with ArgoCD pre-installed in the "argocd" namespace.
type HelmInstallConfig struct {
	// KindClusterName is the kind cluster to load the image into and
	// install Sharko against. Required — installSharkoHelm fails fast
	// when empty (the kindCluster.Name argument is the canonical source,
	// but this is also captured here so a future caller can install
	// against an arbitrary cluster name without holding a *KindCluster).
	KindClusterName string
	// Namespace overrides the install namespace. Defaults to "sharko".
	Namespace string
	// Timeout caps the rollout wait. Defaults to 180s
	// (defaultRolloutWait). The same bound applies to the helm install
	// CLI invocation via --timeout.
	Timeout time.Duration
	// SetValues are extra `--set key=value` overrides to layer onto the
	// install. The harness always sets image.repository, image.tag, and
	// image.pullPolicy itself; entries here win on conflict.
	SetValues map[string]string
	// HelmBin overrides the helm binary path (defaults to "helm" on
	// PATH; honours E2E_HELM_BIN env var if set).
	HelmBin string
	// DockerBin overrides the docker binary path (defaults to "docker"
	// on PATH; honours E2E_DOCKER_BIN env var if set).
	DockerBin string
}

// resolved returns a HelmInstallConfig with environment-variable + default
// fallbacks applied for every empty field. Mirrors ProvisionRequest.resolved
// in kind.go.
func (c HelmInstallConfig) resolved() HelmInstallConfig {
	out := c
	if out.Namespace == "" {
		out.Namespace = defaultHelmNamespace
	}
	if out.Timeout <= 0 {
		out.Timeout = defaultRolloutWait
	}
	if out.HelmBin == "" {
		if v := os.Getenv("E2E_HELM_BIN"); v != "" {
			out.HelmBin = v
		} else {
			out.HelmBin = defaultHelmBin
		}
	}
	if out.DockerBin == "" {
		if v := os.Getenv("E2E_DOCKER_BIN"); v != "" {
			out.DockerBin = v
		} else {
			out.DockerBin = defaultDockerBin
		}
	}
	return out
}

// installSharkoHelm builds the Sharko Docker image from the worktree, loads
// it into the supplied kind cluster, helm-installs the chart, waits for
// rollout, and returns a populated *HelmHandle.
//
// The full sequence:
//
//  1. Resolve image tag (SHARKO_E2E_IMAGE_TAG cache hit, or a fresh
//     "e2e-<8-hex>" tag).
//  2. If cache miss: docker build -t sharko:<tag> . from the worktree root
//     (resolved via runtime.Caller so the build always uses the same source
//     tree the test was compiled from).
//  3. kind load docker-image sharko:<tag> --name <kindCluster.Name>.
//  4. helm upgrade --install sharko ./charts/sharko --namespace <ns>
//     --create-namespace --set image.repository=sharko --set image.tag=<tag>
//     --set image.pullPolicy=IfNotPresent <plus caller overrides>.
//  5. kubectl rollout status deployment/sharko -n <ns> --timeout=<bound>.
//  6. Populate *HelmHandle and register t.Cleanup (helm uninstall + ns delete).
//
// Idempotency: step 4 uses `helm upgrade --install` so a re-run with the
// same tag in the same cluster is safe (Helm reconciles to the desired
// state instead of erroring). The cleanup hook makes intra-test repeats
// rare in practice — the typical re-run scenario is a fresh test process
// reusing a kept-alive cluster.
//
// Error mode discipline: each step's error message names the step, the
// command attempted, and includes the combined output from the subprocess
// when the failure is a subprocess exit. The rollout-wait timeout
// additionally dumps the deployment's current state (`kubectl describe`)
// + the most-recent pod's logs (`kubectl logs --tail=30`) so the failure
// is debuggable from CI logs without re-running locally.
//
// Calls t.Helper but does NOT call t.Fatalf — returns the error so callers
// can layer their own t.Fatalf with additional context (Story 13.3's
// SharkoModeHelm wrapper does exactly this).
func installSharkoHelm(t *testing.T, kindCluster *KindCluster, cfg HelmInstallConfig) (*HelmHandle, error) {
	t.Helper()

	if kindCluster == nil {
		return nil, fmt.Errorf("installSharkoHelm: kindCluster is nil")
	}
	if kindCluster.Name == "" {
		return nil, fmt.Errorf("installSharkoHelm: kindCluster.Name is empty")
	}
	if kindCluster.Kubeconfig == "" {
		return nil, fmt.Errorf("installSharkoHelm: kindCluster.Kubeconfig is empty (cluster=%s)", kindCluster.Name)
	}

	// Reconcile the cluster name: the kindCluster argument is canonical,
	// but a non-empty cfg.KindClusterName must match (catches the
	// configuration mistake of passing two different clusters).
	if cfg.KindClusterName != "" && cfg.KindClusterName != kindCluster.Name {
		return nil, fmt.Errorf("installSharkoHelm: cfg.KindClusterName=%q does not match kindCluster.Name=%q",
			cfg.KindClusterName, kindCluster.Name)
	}

	cfg = cfg.resolved()
	ns := cfg.Namespace

	// Find the worktree root from the package directory so docker build
	// and helm install always operate on the same source tree the test
	// was compiled from. Using os.Getwd would pick up the test process's
	// cwd which differs depending on how `go test` is invoked.
	worktreeRoot, err := worktreeRootFromCallerFile()
	if err != nil {
		return nil, fmt.Errorf("installSharkoHelm: resolve worktree root: %w", err)
	}
	chartPath := filepath.Join(worktreeRoot, "charts", "sharko")
	if _, err := os.Stat(filepath.Join(chartPath, "Chart.yaml")); err != nil {
		return nil, fmt.Errorf("installSharkoHelm: chart not found at %s: %w", chartPath, err)
	}
	dockerfile := filepath.Join(worktreeRoot, "Dockerfile")
	if _, err := os.Stat(dockerfile); err != nil {
		return nil, fmt.Errorf("installSharkoHelm: Dockerfile not found at %s: %w", dockerfile, err)
	}

	// 1. Resolve the image tag.
	tag, skipBuild, err := resolveImageTag(t, cfg)
	if err != nil {
		return nil, fmt.Errorf("installSharkoHelm: resolve image tag: %w", err)
	}
	imageRef := "sharko:" + tag

	// 2. Build the image (skipped only when SHARKO_E2E_IMAGE_TAG is set
	// AND the image already exists in the local Docker host).
	if skipBuild {
		t.Logf("harness: skipping docker build (SHARKO_E2E_IMAGE_TAG=%s already in local Docker)", tag)
	} else {
		t.Logf("harness: docker build sharko:%s from worktree root", tag)
		buildCtx, buildCancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer buildCancel()
		if err := dockerBuild(buildCtx, t, cfg.DockerBin, worktreeRoot, imageRef, tag); err != nil {
			return nil, fmt.Errorf("installSharkoHelm: docker build: %w", err)
		}
	}

	// 3. ALWAYS load into kind — fresh per-test clusters start with an
	// empty containerd image store. `kind load docker-image` is fast
	// (~5-10s) and idempotent, so unconditional loading is the safe
	// default; conditional skipping was the source of V125-1-13.1's
	// ImagePullBackOff bug (the probe checked the wrong containerd and
	// the cache always reported a false hit).
	t.Logf("harness: kind load docker-image sharko:%s into %s", tag, kindCluster.Name)
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer loadCancel()
	if err := kindLoadImage(loadCtx, t, kindCluster.Name, imageRef); err != nil {
		return nil, fmt.Errorf("installSharkoHelm: kind load docker-image: %w", err)
	}

	// 4. helm upgrade --install. We always run with --create-namespace and
	// rely on `upgrade --install` for idempotency. The chart's default
	// values + the harness overrides keep optional subsystems off (AI,
	// Ollama, ingress, persistence) so install completes deterministically
	// in CI without external dependencies.
	helmCtx, helmCancel := context.WithTimeout(context.Background(), defaultHelmInstallWait)
	defer helmCancel()
	if err := helmUpgradeInstall(helmCtx, t, cfg, kindCluster.Kubeconfig, chartPath, ns, tag); err != nil {
		return nil, fmt.Errorf("installSharkoHelm: helm upgrade --install: %w", err)
	}

	// 5. Wait for rollout. On failure, attach diagnostic snippets so the
	// CI log is actionable without a second run.
	rolloutCtx, rolloutCancel := context.WithTimeout(context.Background(), cfg.Timeout+30*time.Second)
	defer rolloutCancel()
	if err := waitForRollout(rolloutCtx, t, kindCluster.Kubeconfig, ns, defaultHelmReleaseName, cfg.Timeout); err != nil {
		// Best-effort diagnostic dump — surface as t.Logf so the
		// caller's t.Fatalf carries the install-time error string.
		dumpDeploymentState(t, kindCluster.Kubeconfig, ns, defaultHelmReleaseName)
		return nil, fmt.Errorf("installSharkoHelm: rollout wait: %w", err)
	}

	handle := &HelmHandle{
		Namespace:       ns,
		Deployment:      defaultHelmReleaseName,
		Service:         defaultHelmReleaseName,
		BaseURL:         fmt.Sprintf("http://%s.%s.svc.cluster.local:80", defaultHelmReleaseName, ns),
		ImageTag:        tag,
		ReleaseName:     defaultHelmReleaseName,
		KindClusterName: kindCluster.Name,
		Kubeconfig:      kindCluster.Kubeconfig,
	}

	// 6. Cleanup hook — best-effort; cleanup failures must never mask
	// real test failures.
	t.Cleanup(func() { uninstallSharkoHelm(t, handle) })

	t.Logf("harness: sharko helm install ready in cluster %s [ns=%s, tag=%s, baseURL=%s]",
		handle.KindClusterName, handle.Namespace, handle.ImageTag, handle.BaseURL)
	return handle, nil
}

// uninstallSharkoHelm runs helm uninstall + kubectl delete ns. Best-effort:
// every error is logged via t.Logf and the function never panics or
// re-fails the test. Idempotent — re-runs after the namespace is gone are
// silent.
func uninstallSharkoHelm(t *testing.T, h *HelmHandle) {
	if h == nil {
		return
	}
	cfg := HelmInstallConfig{}.resolved()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	uninstall := exec.CommandContext(ctx, cfg.HelmBin,
		"uninstall", h.ReleaseName,
		"--namespace", h.Namespace,
		"--kubeconfig", h.Kubeconfig,
		"--ignore-not-found",
	)
	if out, err := uninstall.CombinedOutput(); err != nil {
		t.Logf("harness: WARNING helm uninstall %s/%s failed: %v\noutput: %s",
			h.Namespace, h.ReleaseName, err, out)
	} else {
		t.Logf("harness: helm uninstall %s/%s done", h.Namespace, h.ReleaseName)
	}

	deleteNS := exec.CommandContext(ctx,
		defaultKubectlBinFromEnv(),
		"--kubeconfig", h.Kubeconfig,
		"delete", "namespace", h.Namespace,
		"--wait=false",
		"--ignore-not-found",
	)
	if out, err := deleteNS.CombinedOutput(); err != nil {
		t.Logf("harness: WARNING delete namespace %s failed: %v\noutput: %s",
			h.Namespace, err, out)
	}
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// resolveImageTag returns (tag, skipBuild) for this install.
//
// When SHARKO_E2E_IMAGE_TAG is set AND `docker image inspect sharko:<tag>`
// exits 0, we know the image already exists in the local Docker host and
// can skip the (slow) docker build. The kind load step still happens
// unconditionally — each kind cluster has its own containerd image store
// and starts empty.
//
// When SHARKO_E2E_IMAGE_TAG is unset OR the image is missing from the
// local Docker host, we generate a fresh e2e-<sha> tag (or reuse the env
// tag) and rebuild.
//
// Predecessor bug (V125-1-13.1): the probe shelled into the kind node and
// ran `crictl images -q <ref>` — but the positional ref arg to that
// command does NOT filter (it returns every image in the cluster). The
// cache always reported a hit, the load step was skipped, and pods sat
// in ImagePullBackOff until helm --wait timed out at 180s. Fixed by
// probing the local Docker host (which is what the env var actually
// certifies) and by always loading into kind regardless of cache state.
func resolveImageTag(t *testing.T, cfg HelmInstallConfig) (string, bool, error) {
	t.Helper()
	if v := strings.TrimSpace(os.Getenv("SHARKO_E2E_IMAGE_TAG")); v != "" {
		exists, err := imageExistsLocallyFn(cfg.DockerBin, "sharko:"+v)
		if err != nil {
			t.Logf("harness: docker image inspect for sharko:%s failed (%v); will rebuild", v, err)
			return v, false, nil
		}
		if exists {
			return v, true, nil
		}
		t.Logf("harness: SHARKO_E2E_IMAGE_TAG=%s but image not present in local Docker; rebuilding under same tag", v)
		return v, false, nil
	}
	suffix, err := randHex8()
	if err != nil {
		return "", false, fmt.Errorf("generate image tag suffix: %w", err)
	}
	return "e2e-" + suffix, false, nil
}

// imageExistsLocallyFn is the seam resolveImageTag uses to probe the local
// Docker host. Production code points it at imageExistsLocally; tests
// override it to exercise the env-var-set / image-present / image-absent
// branches without requiring a real Docker daemon. Same pattern as
// inClusterConfigFn in internal/providers/provider.go.
var imageExistsLocallyFn = imageExistsLocally

// imageExistsLocally returns true when the local Docker daemon has an
// image with the given ref. Used by resolveImageTag to decide whether
// SHARKO_E2E_IMAGE_TAG can skip the rebuild step.
//
// Probes via `docker image inspect <ref>` — exits 0 on hit, non-zero on
// miss. Stdout + stderr are suppressed to avoid log noise on the miss
// path; only genuine probe errors (docker binary absent, daemon down)
// bubble up via the non-ExitError branch.
func imageExistsLocally(dockerBin, imageRef string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, dockerBin, "image", "inspect", imageRef)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		// ExitError with non-zero exit code means "image missing" —
		// the standard signal from `docker image inspect`. Differentiate
		// from "docker not found" / "daemon unreachable" by checking
		// err type so genuine probe failures surface as errors.
		if _, ok := err.(*exec.ExitError); ok {
			return false, nil
		}
		return false, fmt.Errorf("docker image inspect: %w", err)
	}
	return true, nil
}

// dockerBuild runs `docker build -t <ref> --build-arg CACHE_BUST=<tag> .`
// from the worktree root. CACHE_BUST is fed into the Dockerfile to
// invalidate the go-build layer per tag (matches the Dockerfile contract
// at line 22 of Dockerfile).
func dockerBuild(ctx context.Context, t *testing.T, dockerBin, worktreeRoot, imageRef, tag string) error {
	t.Helper()
	args := []string{
		"build",
		"-t", imageRef,
		"--build-arg", "CACHE_BUST=" + tag,
		"--build-arg", "VERSION=" + tag,
		worktreeRoot,
	}
	t.Logf("harness: docker build %s (CACHE_BUST=%s) — this can take 1-3 minutes on a cold cache",
		imageRef, tag)
	cmd := exec.CommandContext(ctx, dockerBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker build %s: %w\noutput: %s", imageRef, err, out)
	}
	t.Logf("harness: docker build %s done", imageRef)
	return nil
}

// kindLoadImage runs `kind load docker-image <ref> --name <cluster>` to copy
// the locally-built image into the kind node's containerd, bypassing the
// need for a registry. Uses the kind binary resolved the same way kind.go
// resolves it (E2E_KIND_BIN env or default "kind").
func kindLoadImage(ctx context.Context, t *testing.T, kindClusterName, imageRef string) error {
	t.Helper()
	kindBin := defaultKindBin
	if v := os.Getenv("E2E_KIND_BIN"); v != "" {
		kindBin = v
	}
	t.Logf("harness: kind load docker-image %s --name %s", imageRef, kindClusterName)
	cmd := exec.CommandContext(ctx, kindBin,
		"load", "docker-image", imageRef,
		"--name", kindClusterName,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kind load %s into %s: %w\noutput: %s", imageRef, kindClusterName, err, out)
	}
	return nil
}

// helmUpgradeInstall runs `helm upgrade --install` with the harness's
// always-on overrides plus any caller-supplied SetValues. `upgrade --install`
// makes re-runs idempotent — on first run helm creates the release; on
// re-runs it reconciles to the new desired state.
//
// The harness always sets:
//   - image.repository=sharko    (matches the locally-built image)
//   - image.tag=<resolved tag>
//   - image.pullPolicy=IfNotPresent (the image is in containerd, never pull)
//
// Caller overrides via cfg.SetValues are appended AFTER the always-on
// flags, so they win on conflict (helm honours last --set wins).
func helmUpgradeInstall(ctx context.Context, t *testing.T, cfg HelmInstallConfig,
	kubeconfig, chartPath, ns, tag string) error {
	t.Helper()

	args := []string{
		"upgrade", "--install", defaultHelmReleaseName, chartPath,
		"--namespace", ns,
		"--create-namespace",
		"--kubeconfig", kubeconfig,
		"--wait",
		"--timeout", helmTimeoutString(cfg.Timeout),
		"--set", "image.repository=sharko",
		"--set", "image.tag=" + tag,
		"--set", "image.pullPolicy=IfNotPresent",
	}
	for k, v := range cfg.SetValues {
		args = append(args, "--set", k+"="+v)
	}

	t.Logf("harness: helm upgrade --install %s in %s (image=sharko:%s)", defaultHelmReleaseName, ns, tag)
	cmd := exec.CommandContext(ctx, cfg.HelmBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm upgrade --install: %w\noutput: %s", err, out)
	}
	t.Logf("harness: helm upgrade --install %s done", defaultHelmReleaseName)
	return nil
}

// waitForRollout polls `kubectl rollout status deployment/<name> -n <ns>`
// until the deployment has fully rolled out or the timeout elapses. Helm's
// own --wait flag covers the normal happy path; this is a belt-and-braces
// check so the harness fails with a clear "rollout wait" message rather
// than silently returning a half-ready handle when the helm CLI bug is in
// play (helm has historically reported success on partial rollouts in some
// edge cases, see GH helm/helm#5170).
func waitForRollout(ctx context.Context, t *testing.T, kubeconfig, ns, name string, timeout time.Duration) error {
	t.Helper()
	cmd := exec.CommandContext(ctx,
		defaultKubectlBinFromEnv(),
		"--kubeconfig", kubeconfig,
		"rollout", "status",
		"deployment/"+name,
		"-n", ns,
		"--timeout="+humanDuration(timeout),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl rollout status deployment/%s -n %s: %w\noutput: %s",
			name, ns, err, out)
	}
	return nil
}

// dumpDeploymentState writes diagnostic info to t.Logf when a rollout times
// out. Best-effort — every step swallows its own errors. Captures:
//   - kubectl describe deployment (current replicas + conditions)
//   - last 30 log lines from the most-recent pod (tells us if the binary
//     crashed during boot, e.g. config parse error, missing env var, etc.)
func dumpDeploymentState(t *testing.T, kubeconfig, ns, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	describe := exec.CommandContext(ctx,
		defaultKubectlBinFromEnv(),
		"--kubeconfig", kubeconfig,
		"describe", "deployment/"+name,
		"-n", ns,
	)
	if out, err := describe.CombinedOutput(); err == nil {
		t.Logf("harness: kubectl describe deployment/%s -n %s:\n%s", name, ns, out)
	}

	logsCtx, logsCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer logsCancel()
	logs := exec.CommandContext(logsCtx,
		defaultKubectlBinFromEnv(),
		"--kubeconfig", kubeconfig,
		"logs",
		"-n", ns,
		"-l", "app.kubernetes.io/name="+name,
		"--tail=30",
		"--all-containers=true",
	)
	if out, err := logs.CombinedOutput(); err == nil && len(out) > 0 {
		t.Logf("harness: kubectl logs -n %s -l app.kubernetes.io/name=%s --tail=30:\n%s", ns, name, out)
	}
}

// worktreeRootFromCallerFile walks up from this file's path (the harness
// package directory) to find the repo root — the directory that contains
// charts/sharko/Chart.yaml AND a Dockerfile.
//
// Using runtime.Caller rather than os.Getwd guarantees the build/install
// always uses the same source tree as the test binary, regardless of how
// `go test` was invoked. This is load-bearing for worktree-based development:
// when the test binary was compiled from a worktree at
// /Users/foo/sharko.worktrees/feat-X, the build must use that worktree's
// Dockerfile + chart, not the maintainer's main checkout at /Users/foo/sharko.
func worktreeRootFromCallerFile() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller(0) failed")
	}
	// file is .../tests/e2e/harness/sharko_helm.go — walk up looking
	// for both Dockerfile and charts/sharko/Chart.yaml as the marker.
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ { // hard cap so a misplaced file can't loop forever
		dockerfile := filepath.Join(dir, "Dockerfile")
		chart := filepath.Join(dir, "charts", "sharko", "Chart.yaml")
		if statOk(dockerfile) && statOk(chart) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("worktree root not found above %s (no Dockerfile + charts/sharko/Chart.yaml)", file)
}

// statOk returns true if path exists (any file mode).
func statOk(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// humanDuration formats a duration for the kubectl --timeout flag (e.g.
// "180s"). kubectl rejects fractional seconds and unrecognised units, so
// always emit whole-second strings.
func humanDuration(d time.Duration) string {
	if d <= 0 {
		d = defaultRolloutWait
	}
	secs := int(d / time.Second)
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%ds", secs)
}

// helmTimeoutString formats a duration for `helm --timeout`. Helm accepts
// the same Go-style duration strings (e.g. "5m", "180s") so we use the
// stdlib formatter directly.
func helmTimeoutString(d time.Duration) string {
	if d <= 0 {
		d = defaultRolloutWait
	}
	return d.String()
}

// randHex8 returns 8 hex characters of crypto-random data. Suitable for
// e2e image-tag suffixes; not for security-sensitive use.
func randHex8() (string, error) {
	buf := make([]byte, 4) // 4 bytes -> 8 hex chars
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ---------------------------------------------------------------------------
// V125-1-13 hotfix — host-loopback URL rewrite for Pod-reachability
// ---------------------------------------------------------------------------

// hostLoopbackNames is the set of hostnames that mean "localhost on the test
// host" — addresses that are reachable from the test process but NOT from
// inside a kind Pod (where the same names resolve to the Pod itself).
var hostLoopbackNames = map[string]struct{}{
	"127.0.0.1": {},
	"localhost": {},
	"::1":       {},
}

// RewriteHostLoopbackForPod takes a URL whose host is "127.0.0.1", "localhost",
// or "[::1]" (i.e. reachable from the test process on the host) and rewrites
// the host portion to "host.docker.internal" — the conventional DNS name that
// resolves to the host's gateway from inside a kind Pod on Docker Desktop.
// Other host shapes (in-cluster DNS, real DNS, non-loopback IPs) pass through
// unchanged. Non-http/https schemes also pass through.
//
// Use this when a URL produced by the test (gitfake, port-forwarded ArgoCD,
// etc.) must be reachable from a Helm-mode Sharko Pod, not just from the
// test process.
//
// Platform notes:
//   - macOS / Windows (Docker Desktop): host.docker.internal works
//     out-of-the-box.
//   - Linux: kind clusters need extraHosts:["host.docker.internal:host-gateway"]
//     in the kind config (or equivalent network setup). See
//     docs/site/developer-guide/e2e-testing.md.
//
// If url.Parse fails, the original string is returned unchanged (callers may
// pass non-URL values defensively).
func RewriteHostLoopbackForPod(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	// Only operate on http/https URLs — other schemes (git://, ssh://, etc.)
	// have their own host-reachability semantics and are out of scope here.
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return rawURL
	}
	host := u.Hostname()
	if host == "" {
		return rawURL
	}
	if _, ok := hostLoopbackNames[host]; !ok {
		return rawURL
	}
	// Reassemble the Host portion preserving the port (if any).
	newHost := "host.docker.internal"
	if port := u.Port(); port != "" {
		newHost = newHost + ":" + port
	}
	u.Host = newHost
	return u.String()
}

//go:build e2e

package harness

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Story V125-1-13.x.2 — In-cluster gitfake Deployment + Service primitive
//
// installGitfakeIntoKind is the harness primitive that deploys a
// gitfake-server Pod inside a kind cluster so the Sharko Pod can clone /
// push against a git endpoint reachable from within the cluster network
// (Service DNS). This unblocks helm-mode lifecycle tests whose git-host
// allowlist would otherwise refuse the test's loopback or host.docker.internal
// URL.
//
// The companion image is built by V125-1-13.x.1's `make build-gitfake-image`
// target (Dockerfile at tests/e2e/harness/gitfake/Dockerfile, binary at
// tests/e2e/harness/gitfake/cmd/gitfake-server/). The Makefile pins
// `SHARKO_GITFAKE_IMAGE_TAG ?= e2e-<git-short-sha>` so a fresh commit forces
// a rebuild and a re-run on the same commit reuses the cached image. This
// file mirrors that convention exactly so make-built images are reused by
// installGitfakeIntoKind without surprise re-tagging.
//
// Implementation pattern intentionally mirrors installSharkoHelm in
// sharko_helm.go: shell out to docker / kind / kubectl via exec.CommandContext,
// inline-YAML for the Deployment + Service (no separate manifest file —
// keeps the primitive self-contained and readable in test logs), and
// best-effort cleanup via t.Cleanup.
// ---------------------------------------------------------------------------

const (
	defaultGitfakeImageRepo  = "sharko-gitfake"
	defaultGitfakeNamespace  = "default"
	defaultGitfakeService    = "gitfake"
	defaultGitfakeDeployment = "gitfake"
	defaultGitfakeRepoName   = "sharko-e2e"
	defaultGitfakeSeedBranch = "main"
	defaultGitfakePort       = 8080
	defaultGitfakeRolloutTO  = 60 * time.Second
	defaultGitfakeCleanupTO  = 30 * time.Second
)

// GitfakeInstallConfig declares knobs for installGitfakeIntoKind.
//
// Zero-value defaults match the V125-1-13.x.1 Makefile + binary defaults:
// image tag from SHARKO_GITFAKE_IMAGE_TAG (or e2e-<short-sha>), default
// namespace, service name "gitfake", repo "sharko-e2e" (the .git suffix is
// applied by callers — RepoURL includes it; the binary accepts both forms).
type GitfakeInstallConfig struct {
	// ImageTag is the gitfake image tag to load + reference. Defaults to
	// the same resolution chain as the Makefile target
	// (SHARKO_GITFAKE_IMAGE_TAG env var, falling back to e2e-<short-sha>).
	ImageTag string
	// Namespace is the K8s namespace for the Deployment + Service.
	// Defaults to "default" (matches the spec — gitfake is shared infra
	// across the test pod set and does not need its own namespace).
	Namespace string
	// ServiceName is the Service name (also used as the Deployment name
	// and pod label value). Defaults to "gitfake".
	ServiceName string
	// RepoName is the repo path segment the binary will serve at
	// /<RepoName>.git. Defaults to "sharko-e2e".
	RepoName string
	// SeedBranch is the default branch on the empty repo. Defaults to "main".
	SeedBranch string
	// SeedFile is the path of an initial committed file. Optional —
	// when empty no seed commit is created.
	SeedFile string
	// SeedContent is the content of SeedFile. Ignored when SeedFile is empty.
	SeedContent string
}

// resolved returns a GitfakeInstallConfig with environment-variable + default
// fallbacks applied for every empty field. Mirrors HelmInstallConfig.resolved
// in sharko_helm.go.
func (c GitfakeInstallConfig) resolved() GitfakeInstallConfig {
	out := c
	if out.Namespace == "" {
		out.Namespace = defaultGitfakeNamespace
	}
	if out.ServiceName == "" {
		out.ServiceName = defaultGitfakeService
	}
	if out.RepoName == "" {
		out.RepoName = defaultGitfakeRepoName
	}
	if out.SeedBranch == "" {
		out.SeedBranch = defaultGitfakeSeedBranch
	}
	return out
}

// GitfakeHandle describes a gitfake server deployed into a kind cluster.
// Returned by installGitfakeIntoKind.
//
// InClusterURL + RepoURL are the load-bearing fields for downstream
// callers: tests configure Sharko (or whatever subject under test) to use
// RepoURL as the git remote, and the in-cluster DNS Service resolves to the
// gitfake Pod regardless of which node the test pod lands on.
type GitfakeHandle struct {
	// Namespace is the K8s namespace where the Deployment + Service live
	// (defaults to "default").
	Namespace string
	// ServiceName is the Service name (defaults to "gitfake"). The
	// Deployment shares the same name.
	ServiceName string
	// PodName is the running gitfake Pod name (captured for diagnostics
	// and convenience — callers wanting per-test logs can `kubectl logs`
	// this directly).
	PodName string
	// ImageRef is the full image reference loaded into kind (e.g.
	// "sharko-gitfake:e2e-d7c49afc").
	ImageRef string
	// InClusterURL is the Service URL reachable from any Pod in the
	// cluster: "http://<service>.<namespace>.svc.cluster.local" (port 80
	// implicit).
	InClusterURL string
	// RepoURL is the full git remote URL — InClusterURL plus the
	// "/<RepoName>.git" path. This is the value callers hand to Sharko
	// (or whatever wants to clone the repo).
	RepoURL string
	// Kubeconfig is the kubeconfig path used for kubectl operations
	// against the kind cluster. Captured so cleanup is independent of the
	// caller's environment.
	Kubeconfig string
	// KindClusterName is the kind cluster the deployment lives in.
	// Captured for diagnostics and logs.
	KindClusterName string

	// cleanupFn is registered via t.Cleanup at install time. Stored so
	// callers wanting to short-circuit cleanup (e.g. for keep-alive
	// debugging) have a handle to it — but the registered Cleanup will
	// still run.
	cleanupFn func() //nolint:unused // reserved for future caller-driven teardown
}

// installGitfakeIntoKind builds (or reuses) the sharko-gitfake image, loads
// it into the kind cluster, applies a Deployment + Service to the namespace,
// waits for the Pod to be Ready, and returns a populated *GitfakeHandle.
//
// The full sequence:
//
//  1. Resolve image tag from cfg.ImageTag, env SHARKO_GITFAKE_IMAGE_TAG, or
//     fall back to e2e-<short-sha>.
//  2. Probe the local Docker host via `docker image inspect`. If absent,
//     shell out to `make build-gitfake-image` with SHARKO_GITFAKE_IMAGE_TAG
//     exported so the Makefile target builds the right tag.
//  3. `kind load docker-image sharko-gitfake:<tag> --name <cluster>` — always
//     run (idempotent, mirrors Sharko's pattern; per-cluster containerd is
//     its own image store).
//  4. Apply the inline Deployment + Service YAML to cfg.Namespace via
//     `kubectl apply --server-side --force-conflicts -f -`.
//  5. `kubectl rollout status deployment/<name> -n <ns> --timeout=60s`. On
//     failure, dump `kubectl describe pod` + `kubectl logs --tail=30` so
//     the failure is debuggable from a single CI run.
//  6. Resolve the running Pod's name via `kubectl get pod -o jsonpath`.
//  7. Register t.Cleanup to delete the Deployment + Service (best-effort).
//
// Calls t.Helper but does NOT call t.Fatalf — returns the error so callers
// can layer their own t.Fatalf with additional context (Story 13.x.5's
// installSharkoHelm integration does exactly this).
func installGitfakeIntoKind(t *testing.T, kindCluster *KindCluster, cfg GitfakeInstallConfig) (*GitfakeHandle, error) {
	t.Helper()

	if kindCluster == nil {
		return nil, fmt.Errorf("installGitfakeIntoKind: kindCluster is nil")
	}
	if kindCluster.Name == "" {
		return nil, fmt.Errorf("installGitfakeIntoKind: kindCluster.Name is empty")
	}
	if kindCluster.Kubeconfig == "" {
		return nil, fmt.Errorf("installGitfakeIntoKind: kindCluster.Kubeconfig is empty (cluster=%s)", kindCluster.Name)
	}

	cfg = cfg.resolved()

	// Resolve the worktree root the same way installSharkoHelm does, so
	// `make build-gitfake-image` (if invoked) runs against the same source
	// tree the test binary was compiled from. Worktree-aware: when the test
	// binary was built from a worktree at /Users/foo/sharko.worktrees/feat-X,
	// the make call must execute there, not in the maintainer's main checkout.
	worktreeRoot, err := worktreeRootFromCallerFile()
	if err != nil {
		return nil, fmt.Errorf("installGitfakeIntoKind: resolve worktree root: %w", err)
	}

	// Resolve binary paths via env-overridable defaults. These mirror
	// installSharkoHelm — same env vars, same fallback chain.
	dockerBin := defaultDockerBin
	if v := os.Getenv("E2E_DOCKER_BIN"); v != "" {
		dockerBin = v
	}
	kubectlBin := defaultKubectlBinFromEnv()

	// 1. Resolve image tag.
	tag, err := resolveGitfakeImageTag(t, dockerBin, cfg.ImageTag, worktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("installGitfakeIntoKind: resolve image tag: %w", err)
	}
	imageRef := defaultGitfakeImageRepo + ":" + tag

	// 2. Build the image only if missing from the local Docker host.
	exists, err := imageExistsLocallyFn(dockerBin, imageRef)
	if err != nil {
		// imageExistsLocallyFn returns nil error on a "miss" — only
		// genuine probe failures bubble up. Log and continue: the make
		// target is itself idempotent and includes the same probe, so
		// the worst case is a slower no-op.
		t.Logf("harness: docker image inspect for %s failed (%v); will attempt build via make", imageRef, err)
		exists = false
	}
	if exists {
		t.Logf("harness: gitfake image %s already in local Docker — skipping build", imageRef)
	} else {
		t.Logf("harness: building gitfake image %s via make build-gitfake-image", imageRef)
		buildCtx, buildCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer buildCancel()
		if err := makeBuildGitfakeImage(buildCtx, t, worktreeRoot, tag); err != nil {
			return nil, fmt.Errorf("installGitfakeIntoKind: make build-gitfake-image: %w", err)
		}
	}

	// 3. ALWAYS load into kind — fresh per-test clusters have empty
	// containerd image stores. `kind load docker-image` is fast and
	// idempotent (same rationale as installSharkoHelm). The same
	// kindLoadImage helper from sharko_helm.go works here unchanged.
	t.Logf("harness: kind load docker-image %s into %s", imageRef, kindCluster.Name)
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer loadCancel()
	if err := kindLoadImage(loadCtx, t, kindCluster.Name, imageRef); err != nil {
		return nil, fmt.Errorf("installGitfakeIntoKind: kind load docker-image: %w", err)
	}

	// 4. Apply Deployment + Service. Inline YAML so the primitive stays
	// self-contained — no separate manifest file to keep in sync with the
	// Go code.
	manifest := renderGitfakeManifest(imageRef, cfg)
	applyCtx, applyCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer applyCancel()
	if err := kubectlApplyStdin(applyCtx, t, kubectlBin, kindCluster.Kubeconfig, cfg.Namespace, manifest); err != nil {
		return nil, fmt.Errorf("installGitfakeIntoKind: kubectl apply: %w", err)
	}

	// 5. Wait for rollout. On failure, attach diagnostic snippets so the
	// CI log carries enough context to skip a second run.
	rolloutCtx, rolloutCancel := context.WithTimeout(context.Background(), defaultGitfakeRolloutTO+30*time.Second)
	defer rolloutCancel()
	if err := waitForGitfakeRollout(rolloutCtx, t, kubectlBin, kindCluster.Kubeconfig, cfg.Namespace, cfg.ServiceName); err != nil {
		dumpGitfakeState(t, kubectlBin, kindCluster.Kubeconfig, cfg.Namespace, cfg.ServiceName)
		return nil, fmt.Errorf("installGitfakeIntoKind: rollout wait: %w", err)
	}

	// 6. Resolve Pod name for diagnostics.
	podName := resolveGitfakePodName(t, kubectlBin, kindCluster.Kubeconfig, cfg.Namespace, cfg.ServiceName)

	handle := &GitfakeHandle{
		Namespace:       cfg.Namespace,
		ServiceName:     cfg.ServiceName,
		PodName:         podName,
		ImageRef:        imageRef,
		InClusterURL:    fmt.Sprintf("http://%s.%s.svc.cluster.local", cfg.ServiceName, cfg.Namespace),
		Kubeconfig:      kindCluster.Kubeconfig,
		KindClusterName: kindCluster.Name,
	}
	handle.RepoURL = handle.InClusterURL + "/" + cfg.RepoName + ".git"

	// 7. Best-effort cleanup. Failures must never mask real test failures.
	cleanupFn := func() { uninstallGitfakeFromKind(t, kubectlBin, handle) }
	handle.cleanupFn = cleanupFn
	t.Cleanup(cleanupFn)

	t.Logf("harness: gitfake ready in cluster %s [ns=%s, svc=%s, pod=%s, image=%s, repo=%s]",
		handle.KindClusterName, handle.Namespace, handle.ServiceName,
		handle.PodName, handle.ImageRef, handle.RepoURL)
	return handle, nil
}

// uninstallGitfakeFromKind deletes the gitfake Deployment + Service.
// Best-effort: failures are logged and never re-fail the test. Idempotent —
// re-runs after the resources are gone are silent (--ignore-not-found).
func uninstallGitfakeFromKind(t *testing.T, kubectlBin string, h *GitfakeHandle) {
	if h == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultGitfakeCleanupTO)
	defer cancel()

	// Delete Deployment + Service in one call so we don't pay two round-trips.
	del := exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", h.Kubeconfig,
		"delete",
		"deployment/"+h.ServiceName,
		"service/"+h.ServiceName,
		"-n", h.Namespace,
		"--ignore-not-found",
		"--wait=false",
	)
	if out, err := del.CombinedOutput(); err != nil {
		t.Logf("harness: WARNING gitfake cleanup (delete deployment+service %s/%s) failed: %v\noutput: %s",
			h.Namespace, h.ServiceName, err, out)
		return
	}
	t.Logf("harness: gitfake cleanup done [ns=%s, svc=%s]", h.Namespace, h.ServiceName)
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// resolveGitfakeImageTag returns the image tag to use for the gitfake image.
// Resolution order:
//
//  1. Explicit cfg.ImageTag (when non-empty after trim).
//  2. SHARKO_GITFAKE_IMAGE_TAG env var (matches the Makefile contract).
//  3. e2e-<git-short-sha> computed from the worktree root (matches the
//     Makefile default).
//
// Returns (tag, nil). The probe-and-skip decision for docker build happens
// in the caller — this helper only resolves the tag.
func resolveGitfakeImageTag(t *testing.T, dockerBin, explicit, worktreeRoot string) (string, error) {
	t.Helper()
	_ = dockerBin // reserved for future probe enrichment; the caller probes after this returns
	if v := strings.TrimSpace(explicit); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv("SHARKO_GITFAKE_IMAGE_TAG")); v != "" {
		return v, nil
	}
	sha, err := gitShortSHA(worktreeRoot)
	if err != nil {
		return "", fmt.Errorf("resolve git short sha (worktree=%s): %w", worktreeRoot, err)
	}
	return "e2e-" + sha, nil
}

// gitShortSHA runs `git -C <root> rev-parse --short HEAD` and returns the
// trimmed output. Mirrors what the Makefile target does at line 187 so the
// computed tags match exactly.
func gitShortSHA(worktreeRoot string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", worktreeRoot, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --short HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// makeBuildGitfakeImage runs `make build-gitfake-image` from the worktree
// root with SHARKO_GITFAKE_IMAGE_TAG=<tag> exported. The Makefile target is
// itself idempotent (it probes `docker image inspect` and skips on hit) but
// we still gate the call on a local probe (in the caller) so the make
// invocation only fires when actually needed — saves the make-process
// startup cost on every test run.
func makeBuildGitfakeImage(ctx context.Context, t *testing.T, worktreeRoot, tag string) error {
	t.Helper()
	cmd := exec.CommandContext(ctx, "make", "build-gitfake-image")
	cmd.Dir = worktreeRoot
	cmd.Env = append(os.Environ(), "SHARKO_GITFAKE_IMAGE_TAG="+tag)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("make build-gitfake-image (tag=%s): %w\noutput: %s", tag, err, out)
	}
	t.Logf("harness: make build-gitfake-image done (tag=%s)", tag)
	return nil
}

// renderGitfakeManifest builds the inline YAML for the gitfake Deployment +
// Service. Kept as a string template (not k8s.io/api types) so the YAML is
// readable in test logs and the file has no extra type-graph dependency
// outside the harness package.
//
// Manifest shape matches the story spec:
//
//   - Deployment: 1 replica, image with imagePullPolicy=Never (kind-loaded),
//     port 8080, env LISTEN_ADDR/REPO_NAME/SEED_BRANCH (+ SEED_FILE/CONTENT
//     when present), HTTP readiness + liveness on /healthz, tiny resource
//     bounds (10m/16Mi requests, 200m/64Mi limits).
//   - Service: ClusterIP, port 80 → targetPort 8080, selector app=<name>.
//
// labels:
//
//	app                          = ServiceName (selector + display)
//	app.kubernetes.io/managed-by = "sharko-e2e"  (keeps Sharko's "managed-by:
//	                                              sharko" filter from matching
//	                                              this test infra)
//	e2e.sharko.io/test           = "true"        (lines up with the kind
//	                                              sentinel for future
//	                                              suite-wide cleanup hooks)
func renderGitfakeManifest(imageRef string, cfg GitfakeInstallConfig) []byte {
	var sb strings.Builder

	// --- Deployment ---
	sb.WriteString("apiVersion: apps/v1\n")
	sb.WriteString("kind: Deployment\n")
	sb.WriteString("metadata:\n")
	sb.WriteString("  name: " + cfg.ServiceName + "\n")
	sb.WriteString("  namespace: " + cfg.Namespace + "\n")
	sb.WriteString("  labels:\n")
	sb.WriteString("    app: " + cfg.ServiceName + "\n")
	sb.WriteString("    app.kubernetes.io/managed-by: sharko-e2e\n")
	sb.WriteString("    e2e.sharko.io/test: \"true\"\n")
	sb.WriteString("spec:\n")
	sb.WriteString("  replicas: 1\n")
	sb.WriteString("  selector:\n")
	sb.WriteString("    matchLabels:\n")
	sb.WriteString("      app: " + cfg.ServiceName + "\n")
	sb.WriteString("  template:\n")
	sb.WriteString("    metadata:\n")
	sb.WriteString("      labels:\n")
	sb.WriteString("        app: " + cfg.ServiceName + "\n")
	sb.WriteString("        app.kubernetes.io/managed-by: sharko-e2e\n")
	sb.WriteString("        e2e.sharko.io/test: \"true\"\n")
	sb.WriteString("    spec:\n")
	sb.WriteString("      containers:\n")
	sb.WriteString("      - name: gitfake\n")
	sb.WriteString("        image: " + imageRef + "\n")
	sb.WriteString("        imagePullPolicy: Never\n")
	sb.WriteString("        ports:\n")
	sb.WriteString("        - name: http\n")
	sb.WriteString("          containerPort: 8080\n")
	sb.WriteString("        env:\n")
	sb.WriteString("        - name: LISTEN_ADDR\n")
	sb.WriteString("          value: \":8080\"\n")
	sb.WriteString("        - name: REPO_NAME\n")
	sb.WriteString("          value: " + yamlQuote(cfg.RepoName) + "\n")
	sb.WriteString("        - name: SEED_BRANCH\n")
	sb.WriteString("          value: " + yamlQuote(cfg.SeedBranch) + "\n")
	if cfg.SeedFile != "" {
		sb.WriteString("        - name: SEED_FILE\n")
		sb.WriteString("          value: " + yamlQuote(cfg.SeedFile) + "\n")
		sb.WriteString("        - name: SEED_CONTENT\n")
		sb.WriteString("          value: " + yamlQuote(cfg.SeedContent) + "\n")
	}
	sb.WriteString("        readinessProbe:\n")
	sb.WriteString("          httpGet:\n")
	sb.WriteString("            path: /healthz\n")
	sb.WriteString("            port: 8080\n")
	sb.WriteString("          initialDelaySeconds: 1\n")
	sb.WriteString("          periodSeconds: 2\n")
	sb.WriteString("          failureThreshold: 15\n")
	sb.WriteString("        livenessProbe:\n")
	sb.WriteString("          httpGet:\n")
	sb.WriteString("            path: /healthz\n")
	sb.WriteString("            port: 8080\n")
	sb.WriteString("          initialDelaySeconds: 1\n")
	sb.WriteString("          periodSeconds: 2\n")
	sb.WriteString("          failureThreshold: 15\n")
	sb.WriteString("        resources:\n")
	sb.WriteString("          requests:\n")
	sb.WriteString("            cpu: 10m\n")
	sb.WriteString("            memory: 16Mi\n")
	sb.WriteString("          limits:\n")
	sb.WriteString("            cpu: 200m\n")
	sb.WriteString("            memory: 64Mi\n")

	// --- Service ---
	sb.WriteString("---\n")
	sb.WriteString("apiVersion: v1\n")
	sb.WriteString("kind: Service\n")
	sb.WriteString("metadata:\n")
	sb.WriteString("  name: " + cfg.ServiceName + "\n")
	sb.WriteString("  namespace: " + cfg.Namespace + "\n")
	sb.WriteString("  labels:\n")
	sb.WriteString("    app: " + cfg.ServiceName + "\n")
	sb.WriteString("    app.kubernetes.io/managed-by: sharko-e2e\n")
	sb.WriteString("    e2e.sharko.io/test: \"true\"\n")
	sb.WriteString("spec:\n")
	sb.WriteString("  type: ClusterIP\n")
	sb.WriteString("  selector:\n")
	sb.WriteString("    app: " + cfg.ServiceName + "\n")
	sb.WriteString("  ports:\n")
	sb.WriteString("  - name: http\n")
	sb.WriteString("    port: 80\n")
	sb.WriteString("    targetPort: 8080\n")
	sb.WriteString("    protocol: TCP\n")

	return []byte(sb.String())
}

// yamlQuote double-quotes a YAML scalar and escapes embedded backslashes +
// double quotes. Sufficient for our env-var values which are short ASCII
// (repo name, branch name, seed file path, seed content). YAML's
// double-quoted style accepts most C-style escapes; we only need to handle
// the two characters that would terminate the string or alter parsing.
func yamlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// kubectlApplyStdin runs `kubectl apply --server-side --force-conflicts -n <ns>
// -f -` with manifest piped on stdin. Server-side apply matches the convention
// in argocd.go and is robust against large CRDs (which we don't have here,
// but consistency with the rest of the harness avoids surprises).
func kubectlApplyStdin(ctx context.Context, t *testing.T, kubectlBin, kubeconfig, namespace string, manifest []byte) error {
	t.Helper()
	cmd := exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", kubeconfig,
		"apply",
		"--server-side", "--force-conflicts",
		"-n", namespace,
		"-f", "-",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	go func() {
		defer stdin.Close()
		_, _ = io.WriteString(stdin, string(manifest))
	}()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply -n %s: %w\noutput: %s\nmanifest:\n%s",
			namespace, err, out, manifest)
	}
	t.Logf("harness: kubectl apply (gitfake) -n %s done", namespace)
	return nil
}

// waitForGitfakeRollout polls kubectl rollout status until the gitfake
// deployment has fully rolled out. Mirrors waitForRollout in sharko_helm.go
// but with the gitfake-specific timeout.
func waitForGitfakeRollout(ctx context.Context, t *testing.T, kubectlBin, kubeconfig, namespace, name string) error {
	t.Helper()
	cmd := exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", kubeconfig,
		"rollout", "status",
		"deployment/"+name,
		"-n", namespace,
		"--timeout="+humanDuration(defaultGitfakeRolloutTO),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl rollout status deployment/%s -n %s: %w\noutput: %s",
			name, namespace, err, out)
	}
	t.Logf("harness: gitfake rollout complete (deployment/%s -n %s)", name, namespace)
	return nil
}

// dumpGitfakeState writes diagnostic info to t.Logf when a rollout fails.
// Best-effort — every step swallows its own errors. Captures `kubectl describe
// pod` (events, conditions) and the last 30 log lines from the gitfake Pod
// (tells us if the binary crashed during boot — bad SEED_FILE, port already
// in use inside the container, etc.). Mirrors dumpDeploymentState.
func dumpGitfakeState(t *testing.T, kubectlBin, kubeconfig, namespace, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	describe := exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", kubeconfig,
		"describe", "pod",
		"-l", "app="+name,
		"-n", namespace,
	)
	if out, err := describe.CombinedOutput(); err == nil {
		t.Logf("harness: kubectl describe pod -l app=%s -n %s:\n%s", name, namespace, out)
	}

	logsCtx, logsCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer logsCancel()
	logs := exec.CommandContext(logsCtx, kubectlBin,
		"--kubeconfig", kubeconfig,
		"logs",
		"-n", namespace,
		"-l", "app="+name,
		"--tail=30",
		"--all-containers=true",
	)
	if out, err := logs.CombinedOutput(); err == nil && len(out) > 0 {
		t.Logf("harness: kubectl logs -l app=%s -n %s --tail=30:\n%s", name, namespace, out)
	}
}

// resolveGitfakePodName returns the name of the running gitfake Pod, or "" on
// any error (logged via t.Logf — Pod name is for diagnostics only, missing it
// must not fail the install).
func resolveGitfakePodName(t *testing.T, kubectlBin, kubeconfig, namespace, name string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", kubeconfig,
		"get", "pod",
		"-n", namespace,
		"-l", "app="+name,
		"-o", "jsonpath={.items[0].metadata.name}",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Logf("harness: WARNING could not resolve gitfake pod name: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

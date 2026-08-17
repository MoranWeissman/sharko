package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cmdUp implements the `playground up` subcommand — spin up hub + N spokes,
// install ArgoCD + Sharko + GitFake, register spokes.
func cmdUp(ctx context.Context) error {
	fmt.Println("==> Starting playground setup")

	// 1. Determine number of spokes from PLAYGROUND_SPOKES env (default 2).
	numSpokes := DefaultSpokes
	if s := os.Getenv("PLAYGROUND_SPOKES"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid PLAYGROUND_SPOKES=%s (must be integer >= 1)", s)
		}
		numSpokes = n
	}
	spokeNames := SpokeDisplayNames(numSpokes)
	fmt.Printf("    Hub: %s\n", ClusterHub)
	fmt.Printf("    Spokes (%d): %v\n", numSpokes, spokeNames)

	// 2. Build the Sharko + GitFake images BEFORE any kind cluster exists.
	//    This is the slowest, least predictable step (cold Sharko build can
	//    take up to ~15 minutes) — running it first means it never has to
	//    compete with cluster startup and ArgoCD's own image pulls inside
	//    the same Docker VM. See sharkoImageBuildTimeout's doc comment.
	if err := buildImages(); err != nil {
		return fmt.Errorf("build images: %w", err)
	}

	// 3. Create or reuse hub kind cluster.
	if err := provisionHub(); err != nil {
		return fmt.Errorf("provision hub: %w", err)
	}

	// 4. Create or reuse spoke kind clusters.
	for i := 0; i < numSpokes; i++ {
		if err := provisionSpoke(i); err != nil {
			return fmt.Errorf("provision spoke %d: %w", i, err)
		}
	}

	// 5. Install ArgoCD on the hub.
	if err := installArgoCD(); err != nil {
		return fmt.Errorf("install ArgoCD: %w", err)
	}

	// 6. Load the already-built Sharko + GitFake images onto the hub — the
	//    hub cluster now exists (step 3), so `kind load` has somewhere to
	//    put them.
	if err := loadImagesOntoHub(); err != nil {
		return fmt.Errorf("load images onto hub: %w", err)
	}

	// 7. Determine which git backend to use (Gitea by default, GitFake when PLAYGROUND_GIT_BACKEND=gitfake).
	gitBackend := os.Getenv("PLAYGROUND_GIT_BACKEND")
	if gitBackend == "" {
		gitBackend = "gitea"
	}
	fmt.Printf("==> Playground git backend: %s\n", gitBackend)

	var giteaURL, giteaToken string
	var gitfakeURL string
	var err error

	if gitBackend == "gitfake" {
		// Deploy in-cluster GitFake Pod on the hub, seeded with managed-clusters.yaml.
		gitfakeURL, err = deployGitFake(spokeNames)
		if err != nil {
			return fmt.Errorf("deploy GitFake: %w", err)
		}
	} else {
		// Deploy Gitea (real git server) on the hub.
		giteaURL, giteaToken, err = deployGitea(spokeNames)
		if err != nil {
			return fmt.Errorf("deploy Gitea: %w", err)
		}
	}

	// 8. Install Sharko on the hub via helm. For Gitea backend, allowlist
	//    the in-cluster Gitea host.
	if err := installSharko(gitBackend, gitfakeURL, giteaURL); err != nil {
		return fmt.Errorf("install Sharko: %w", err)
	}

	// 9. Register the N spokes as Sharko-managed clusters via REST API.
	//    For Gitea backend, also create a gitea-typed connection.
	if err := registerSpokes(numSpokes, spokeNames, gitBackend, giteaURL, giteaToken); err != nil {
		return fmt.Errorf("register spokes: %w", err)
	}

	// 10. Print access instructions and next steps.
	if err := printSuccessMessage(); err != nil {
		// Non-fatal — just log the error and continue.
		fmt.Printf("    Warning: could not retrieve all credentials: %v\n", err)
	}

	// 11. Show the status snapshot.
	if err := showStatusSnapshot(); err != nil {
		// Non-fatal — just log the warning.
		fmt.Printf("    Warning: status snapshot unavailable: %v\n", err)
	}

	return nil
}

// provisionHub creates or reuses the hub kind cluster.
func provisionHub() error {
	if kindClusterExists(ClusterHub) {
		fmt.Printf("==> Hub cluster '%s' already exists (reusing)\n", ClusterHub)
		return nil
	}

	fmt.Printf("==> Creating hub cluster '%s'\n", ClusterHub)
	_, stderr, err := runCmd(3*time.Minute, "kind", "create", "cluster",
		"--name", ClusterHub,
		"--wait", "60s")
	if err != nil {
		return fmt.Errorf("kind create cluster %s: %w (stderr=%s)", ClusterHub, err, stderr)
	}
	fmt.Printf("    Hub cluster created\n")
	return nil
}

// provisionSpoke creates or reuses spoke cluster i (0-based).
func provisionSpoke(i int) error {
	name := SpokeClusterName(i)
	if kindClusterExists(name) {
		fmt.Printf("==> Spoke cluster '%s' already exists (reusing)\n", name)
		return nil
	}

	fmt.Printf("==> Creating spoke cluster '%s'\n", name)
	_, stderr, err := runCmd(3*time.Minute, "kind", "create", "cluster",
		"--name", name,
		"--wait", "60s")
	if err != nil {
		return fmt.Errorf("kind create cluster %s: %w (stderr=%s)", name, err, stderr)
	}
	fmt.Printf("    Spoke cluster created\n")
	return nil
}

// installArgoCD installs ArgoCD stable manifests on the hub (idempotent).
func installArgoCD() error {
	fmt.Println("==> Installing ArgoCD on hub")
	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	// Create argocd namespace (idempotent).
	_, _, _ = runCmd(15*time.Second, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "create", "namespace", ArgoCDNamespace)

	// Apply ArgoCD manifests.
	_, stderr, err := runCmd(3*time.Minute, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"apply", "--server-side", "--force-conflicts",
		"-n", ArgoCDNamespace,
		"-f", "https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml")
	if err != nil {
		return fmt.Errorf("kubectl apply argocd manifests: %w (stderr=%s)", err, stderr)
	}

	// Wait for argocd-server deployment to be ready.
	fmt.Println("    Waiting for argocd-server to be ready (up to 3 minutes)...")
	if err := kubectlWait(kubeconfigPath, ContextHub, ArgoCDNamespace, "deployment", "argocd-server", "available", 3*time.Minute); err != nil {
		return fmt.Errorf("wait for argocd-server: %w", err)
	}

	// Grant the admin account's ArgoCD RBAC role explicitly (walk finding:
	// recurring non-fatal 403 on app-refresh remediation). See
	// grantArgoCDAdminRBAC's doc comment for why this is needed even for the
	// built-in admin account on a stock install.
	if err := grantArgoCDAdminRBAC(kubeconfigPath); err != nil {
		return fmt.Errorf("grant argocd admin RBAC: %w", err)
	}

	fmt.Println("    ArgoCD installed")
	return nil
}

// grantArgoCDAdminRBAC ensures argocd-rbac-cm's policy.csv contains
// "g, admin, role:admin" — an explicit group-membership grant of ArgoCD's
// built-in admin role to the admin account.
//
// Why a stock `kubectl apply -f install.yaml` (what installArgoCD does
// above) is not enough on its own: mintArgoCDToken (cmd_up.go) mints the
// token Sharko uses for its ArgoCD connection by logging in as admin via
// POST /api/v1/session. sharko-dev.sh's `argocd-token` subcommand (used for
// the equivalent problem in the non-playground local-dev flow) already
// documents the underlying ArgoCD RBAC nuance in detail (see its "Account
// RBAC grant" step, V2-cleanup-10 / PR #393): a fresh ArgoCD install ships
// an EMPTY argocd-rbac-cm policy.csv, and depending on exactly which
// identity/session shape a given ArgoCD version and token type resolves to,
// a request against that empty policy can be denied with 403 even for the
// admin account — the remediation flow's periodic RefreshApplication calls
// are the ones that surface it, because they run automatically and keep
// retrying (and logging) instead of failing once and being noticed.
//
// This mirrors the exact, already-proven fix from sharko-dev.sh: a single
// "g, admin, role:admin" line in policy.csv. It is the narrowest known-good
// grant — a group membership binding to ArgoCD's BUILT-IN role:admin
// (compiled into the ArgoCD binary, not user-editable), not a
// policy.default change that would affect anonymous/other accounts. Unlike
// sharko-dev.sh's flow, this playground still uses a password-session token
// (not an apiKey account token), so no argocd-cm capability patch or
// argocd-server restart is needed — argocd-rbac-cm hot-reloads.
//
// Playground-only: this patches the ArgoCD instance THIS playground run
// just installed via install.yaml, never any chart/values shipped to real
// users.
func grantArgoCDAdminRBAC(kubeconfigPath string) error {
	fmt.Println("    Granting admin role:admin in argocd-rbac-cm...")

	changed, err := ensureRBACGrantLine(kubeconfigPath, adminRoleGrantLine)
	if err != nil {
		return err
	}
	if changed {
		fmt.Println("    argocd-rbac-cm patched — admin granted role:admin")
	} else {
		fmt.Println("    argocd-rbac-cm already grants admin role:admin")
	}
	return nil
}

// adminRoleGrantLine is the policy.csv line grantArgoCDAdminRBAC ensures is
// present — a group-membership binding of the admin account to ArgoCD's
// built-in role:admin. Exported as a constant (not inlined) so
// mergeAdminRoleGrant's test can assert against the exact same string the
// production path writes.
const adminRoleGrantLine = "g, admin, role:admin"

// ensureRBACGrantLine is the shared read-merge-patch cycle behind every
// argocd-rbac-cm grant this playground manages (the admin grant above, and
// the sharko account grant in argocd_sharko_token.go). Each call re-reads
// policy.csv fresh, so two grants applied back-to-back in the same process
// compose correctly instead of clobbering each other: the second call's read
// already sees whatever the first call just wrote.
//
// Returns whether a patch was actually issued, so callers that also need to
// restart argocd-server (only the sharko-account flow does — see
// argocd_sharko_token.go) can skip the restart when nothing changed.
func ensureRBACGrantLine(kubeconfigPath, grantLine string) (changed bool, err error) {
	current, _, err := runCmd(15*time.Second, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"-n", ArgoCDNamespace,
		"get", "configmap", "argocd-rbac-cm",
		"-o", `jsonpath={.data.policy\.csv}`)
	if err != nil {
		// argocd-rbac-cm always ships with install.yaml, so a read failure
		// here means something is genuinely wrong with the install, not an
		// expected "not found yet" state — surface it rather than silently
		// skipping the grant.
		return false, fmt.Errorf("read argocd-rbac-cm: %w", err)
	}

	newPolicy, alreadyGranted := mergeRoleGrantLine(current, grantLine)
	if alreadyGranted {
		return false, nil
	}

	patch := map[string]map[string]string{
		"data": {"policy.csv": newPolicy},
	}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return false, fmt.Errorf("marshal argocd-rbac-cm patch: %w", err)
	}

	if _, stderr, err := runCmd(15*time.Second, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"-n", ArgoCDNamespace,
		"patch", "configmap", "argocd-rbac-cm",
		"--type", "merge",
		"-p", string(patchJSON)); err != nil {
		return false, fmt.Errorf("patch argocd-rbac-cm: %w (stderr=%s)", err, stderr)
	}

	return true, nil
}

// mergeRoleGrantLine is the pure decision logic shared by every
// argocd-rbac-cm grant this playground manages: given the CURRENT contents
// of policy.csv and the exact grant line desired, it reports the policy.csv
// value that should be written, and whether the grant was already present
// (in which case newPolicy equals current unchanged and the caller should
// skip the write entirely — argocd-rbac-cm hot-reloads, but there is no
// reason to issue a no-op patch every run).
//
// Split out from ensureRBACGrantLine (which does the kubectl read/patch) so
// this line-matching/merge logic — the part most likely to have an
// off-by-one bug (trailing newlines, blank lines, an existing rule that
// differs only in whitespace) — has a direct unit test that needs no
// cluster.
func mergeRoleGrantLine(currentPolicyCSV, grantLine string) (newPolicy string, alreadyGranted bool) {
	trimmed := strings.TrimSpace(currentPolicyCSV)

	for _, line := range strings.Split(trimmed, "\n") {
		if strings.TrimSpace(line) == grantLine {
			return currentPolicyCSV, true
		}
	}

	if trimmed == "" {
		return grantLine, false
	}
	return trimmed + "\n" + grantLine, false
}

// mergeAdminRoleGrant is mergeRoleGrantLine specialized to adminRoleGrantLine
// — kept as its own named function because argocd_rbac_test.go asserts
// against it directly.
func mergeAdminRoleGrant(currentPolicyCSV string) (newPolicy string, alreadyGranted bool) {
	return mergeRoleGrantLine(currentPolicyCSV, adminRoleGrantLine)
}

// sharkoImageBuildTimeout is the budget for a cold `docker build` of the
// Sharko image (Go binary + full UI production build). The old flat 5-minute
// timeout was cutting the build off mid-way on machines where the UI build
// step alone takes ~4 minutes — this only matters on the FIRST run per git
// SHA; every later run hits buildImages' skip-if-already-built branch
// instead and never waits on this at all.
const sharkoImageBuildTimeout = 15 * time.Minute

// buildImages builds the Sharko + GitFake Docker images. Deliberately split
// from loadImagesOntoHub (below) and run BEFORE any kind cluster is created
// — a cold Sharko build (Go binary + full UI production build) can take up
// to ~15 minutes, and running it while kind is also standing up clusters and
// ArgoCD is cold-pulling its own images inside the same Docker VM starves
// the build of resources with zero visible progress (walk finding, task
// #147: the build silently hit its full budget and was killed even though
// BuildKit's buffered output showed it had actually reached the final `go
// build` step). Building first — before there is any cluster to compete
// with — and streaming progress via --progress=plain fixes both problems.
func buildImages() error {
	fmt.Println("==> Building Sharko + GitFake images")

	// Build GitFake image via Makefile.
	fmt.Println("    Building GitFake image...")
	if _, stderr, err := runCmd(5*time.Minute, "make", "build-gitfake-image"); err != nil {
		return fmt.Errorf("make build-gitfake-image: %w (stderr=%s)", err, stderr)
	}

	// Build Sharko image (Dockerfile at repo root). Skips the build the same
	// way build-gitfake-image (above) already does when the tag for the
	// current commit is already present locally — a re-run against an
	// already-built playground doesn't need to pay the build cost again.
	gitSHA := mustRunCmd(10*time.Second, "git", "rev-parse", "--short", "HEAD")
	sharkoImage := "sharko:playground-" + gitSHA
	if dockerImageExists(sharkoImage) {
		fmt.Printf("    Sharko image %s already present locally — skipping build\n", sharkoImage)
	} else {
		// This is a cold multi-stage build (Go binary + full UI production
		// build) — on a real machine the UI step alone can take several
		// minutes, so a flat 5-minute budget was cutting the build off
		// mid-way on first run. 15 minutes is an honest budget for a cold
		// build; every SUBSEQUENT run hits the skip branch above instead.
		// --progress=plain streams BuildKit output line-by-line as it
		// happens, instead of buffering it until the build finishes or is
		// killed — a stalled build must be visible in the log, not silent
		// for up to 15 minutes.
		fmt.Println("    Building Sharko image (first run — this is a cold build of the Go binary + full UI, can take up to ~15 minutes)...")
		if _, stderr, err := runCmd(sharkoImageBuildTimeout, "docker", "build", "--progress=plain", "-t", sharkoImage, "."); err != nil {
			return fmt.Errorf("docker build sharko: %w (stderr=%s)", err, stderr)
		}
		fmt.Println("    Sharko image built")
	}

	fmt.Println("    Images built")
	return nil
}

// loadImagesOntoHub kind-loads the already-built Sharko + GitFake images
// onto the hub cluster. Split from buildImages (above) because `kind load`
// needs a running cluster to load into — this can only run once the hub
// cluster exists, whereas the build itself has no such dependency and is
// deliberately run earlier (see buildImages' doc comment).
func loadImagesOntoHub() error {
	fmt.Println("==> Loading images onto hub cluster")

	gitSHA := mustRunCmd(10*time.Second, "git", "rev-parse", "--short", "HEAD")
	sharkoImage := "sharko:playground-" + gitSHA
	gitfakeImage := "sharko-gitfake:e2e-" + gitSHA

	if _, stderr, err := runCmd(2*time.Minute, "kind", "load", "docker-image", gitfakeImage, "--name", ClusterHub); err != nil {
		return fmt.Errorf("kind load gitfake image: %w (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runCmd(2*time.Minute, "kind", "load", "docker-image", sharkoImage, "--name", ClusterHub); err != nil {
		return fmt.Errorf("kind load sharko image: %w (stderr=%s)", err, stderr)
	}

	fmt.Println("    Images loaded")
	return nil
}

// deployGitFake deploys an in-cluster GitFake Pod on the hub, seeded with
// managed-clusters.yaml. Returns the in-cluster service URL.
//
// LEGACY / DORMANT PATH — read before touching this or the GitFake branch in
// registerSpokes below. GitFake (tests/e2e/harness/gitfake) only speaks the
// git smart-HTTP protocol (clone/push); it has no PR-open/PR-merge REST API.
// So this function bakes managed-clusters.yaml directly into the Pod at
// startup (SEED_CONTENT below) and registerSpokes' GitFake branch talks to
// Sharko with the old direct addons-map call — NEITHER goes through Sharko's
// real API doors (POST /api/v1/init, POST /api/v1/clusters, POST
// /api/v1/catalog/addons, ...) the way the default Gitea backend's
// runGiteaRealDoorsFlow (realdoors.go) does. A GitFake playground run does
// NOT exercise the seed-bootstrap PR flow, the catalog approval gate, or any
// PR merge/reconcile path Gitea's flow proves.
//
// Gitea is the supported local playground flow (PLAYGROUND_GIT_BACKEND is
// "gitea" by default; see docs/site/developer-guide/playground.md's "GitFake
// backend note"). This path is kept only because some of GitFake's own
// existing test coverage (tests/e2e/harness) still exercises it in-process,
// and because ripping it out is a bigger, separate call than a walk-finding
// fix — it is not a second maintained way to run the playground. Do NOT
// build GitFake a PR API to "fix" this; that is out of scope here (see
// e2e-testing.md's own note on the same limitation) — either keep pointing
// people at Gitea, or have a real conversation about deleting this path.
func deployGitFake(spokeNames []string) (string, error) {
	fmt.Println("==> Deploying in-cluster GitFake")

	// Generate seed content for managed-clusters.yaml (assigns ~2 addons per spoke).
	seedContent := generateManagedClustersSeed(spokeNames)

	gitSHA := mustRunCmd(10*time.Second, "git", "rev-parse", "--short", "HEAD")
	gitfakeImage := "sharko-gitfake:e2e-" + gitSHA

	// GitFake Deployment YAML.
	deploymentYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: gitfake
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gitfake
  template:
    metadata:
      labels:
        app: gitfake
    spec:
      containers:
      - name: gitfake
        image: %s
        imagePullPolicy: Never
        env:
        - name: LISTEN_ADDR
          value: ":8080"
        - name: REPO_NAME
          value: %s
        - name: SEED_BRANCH
          value: %s
        - name: SEED_FILE
          value: managed-clusters.yaml
        - name: SEED_CONTENT
          value: |
%s
        ports:
        - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: gitfake
  namespace: %s
spec:
  selector:
    app: gitfake
  ports:
  - port: 80
    targetPort: 8080
`, Namespace, gitfakeImage, GitFakeRepoName, GitFakeSeedBranch, indentMultiline(seedContent, 12), Namespace)

	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	// Create sharko namespace (idempotent).
	_, _, _ = runCmd(15*time.Second, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "create", "namespace", Namespace)

	if err := kubectlApply(kubeconfigPath, ContextHub, Namespace, deploymentYAML); err != nil {
		return "", fmt.Errorf("apply gitfake deployment: %w", err)
	}

	// Wait for gitfake deployment to be ready.
	fmt.Println("    Waiting for gitfake deployment to be ready...")
	if err := kubectlWait(kubeconfigPath, ContextHub, Namespace, "deployment", "gitfake", "available", 2*time.Minute); err != nil {
		return "", fmt.Errorf("wait for gitfake deployment: %w", err)
	}

	serviceURL := fmt.Sprintf("http://gitfake.%s.svc.cluster.local/%s.git", Namespace, GitFakeRepoName)
	fmt.Printf("    GitFake deployed at %s\n", serviceURL)
	return serviceURL, nil
}

// deployGitea deploys a real Gitea server in the hub, headlessly bootstraps it
// (admin user + API token + repo), and seeds the two config files Sharko reads.
// Returns the in-cluster Git URL and the API token. This is dev tooling only —
// no product code changes.
//
// This is now the DEFAULT git backend for the playground (as of Story 4b).
// To use GitFake instead, set PLAYGROUND_GIT_BACKEND=gitfake.
func deployGitea(spokeNames []string) (giteaURL, giteaToken string, err error) {
	fmt.Println("==> Deploying Gitea in hub")

	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	// 1. Deploy Gitea Deployment + Service
	giteaDeploymentYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: gitea
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: gitea
  template:
    metadata:
      labels:
        app: gitea
    spec:
      containers:
      - name: gitea
        image: gitea/gitea:1.22.6
        imagePullPolicy: IfNotPresent
        env:
        - name: GITEA__security__INSTALL_LOCK
          value: "true"
        - name: GITEA__database__DB_TYPE
          value: sqlite3
        - name: GITEA__database__PATH
          value: /data/gitea.db
        - name: GITEA__server__ROOT_URL
          value: http://gitea.%s.svc.cluster.local:3000/
        - name: GITEA__server__DISABLE_REGISTRATION
          value: "true"
        - name: GITEA__service__DISABLE_REGISTRATION
          value: "true"
        - name: USER_UID
          value: "1000"
        - name: USER_GID
          value: "1000"
        ports:
        - containerPort: 3000
        - containerPort: 22
        volumeMounts:
        - name: data
          mountPath: /data
      volumes:
      - name: data
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: gitea
  namespace: %s
spec:
  selector:
    app: gitea
  ports:
  - name: http
    port: 3000
    targetPort: 3000
  - name: ssh
    port: 22
    targetPort: 22
`, Namespace, Namespace, Namespace)

	// Create sharko namespace (idempotent).
	_, _, _ = runCmd(15*time.Second, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "create", "namespace", Namespace)

	if err := kubectlApply(kubeconfigPath, ContextHub, Namespace, giteaDeploymentYAML); err != nil {
		return "", "", fmt.Errorf("apply gitea deployment: %w", err)
	}

	// Wait for gitea deployment to be ready.
	fmt.Println("    Waiting for Gitea deployment to be ready (up to 3 minutes)...")
	if err := kubectlWait(kubeconfigPath, ContextHub, Namespace, "deployment", "gitea", "available", 3*time.Minute); err != nil {
		return "", "", fmt.Errorf("wait for gitea deployment: %w", err)
	}

	// 2. Bootstrap Gitea headlessly
	// Initialize the database schema (INSTALL_LOCK=true skips the web installer's auto-migration)
	fmt.Println("    Initializing Gitea database schema (gitea migrate)...")
	if _, migrateErr := execGiteaCmd(kubeconfigPath, Namespace, ContextHub, "gitea migrate"); migrateErr != nil {
		return "", "", fmt.Errorf("gitea migrate (init db schema): %w", migrateErr)
	}

	fmt.Println("    Bootstrapping Gitea (creating admin user)...")

	// Create admin user (idempotent — ignore "user already exists" error).
	// Run as the 'git' user (uid 1000) because Gitea CLI refuses to run as root.
	//
	// Re-running over a half-built playground (a previous run created the
	// user, then got interrupted before finishing bootstrap) hits this on
	// every fresh run — execGiteaCmd itself recognizes gitea's "already
	// exists" outcome and fails fast (no 10-attempt retry loop) rather than
	// treating it like the DB-not-ready race the retry loop is for. We
	// still classify the error here and treat it as success — the user is
	// there, which is what we need.
	createUserCmd := fmt.Sprintf("gitea admin user create --admin --username %s --password %s --email %s --must-change-password=false",
		GiteaAdminUser, GiteaAdminPassword, GiteaAdminEmail)
	_, createErr := execGiteaCmd(kubeconfigPath, Namespace, ContextHub, createUserCmd)
	if createErr != nil {
		// Check if it's the idempotent "already exists" case
		errStr := createErr.Error()
		if !contains(errStr, "user already exists") && !contains(errStr, "already exists") {
			return "", "", fmt.Errorf("create gitea admin user: %w", createErr)
		}
		// else: user already exists, proceed
	}

	// Start port-forward to access Gitea's REST API from the playground
	// process. Moved ahead of token generation (it used to sit right before
	// repo creation) because the token step below now needs the REST API
	// too, to delete a stale token before minting a fresh one. Retry
	// establishing the tunnel to absorb flaky port-forward startup.
	fmt.Println("    Establishing Gitea port-forward (with retry)...")
	pfCmd, err := establishGiteaPortForward(kubeconfigPath)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = killProcessGroup(pfCmd) }()

	// Generate API token
	fmt.Println("    Generating Gitea API token...")
	//
	// Unlike admin user create above, a token-name collision can't be waved
	// off as "already there, proceed" — gitea only ever shows a token's raw
	// value once, at creation time, so a token left over from an earlier
	// half-failed run is unrecoverable, not redundant. That means the create
	// call itself can never be made idempotent; what makes THIS step
	// rerunnable is deleting any stale token under the same name first (via
	// the REST API — the gitea CLI has no delete-token subcommand), so the
	// create call below always starts from a clean slate.
	if err := giteaDeleteAccessTokenIfExists(GiteaAdminUser, GiteaAdminPassword, GiteaAPITokenName); err != nil {
		return "", "", fmt.Errorf("clear stale gitea token before regenerating: %w", err)
	}

	// Run as the 'git' user (uid 1000) because Gitea CLI refuses to run as root.
	generateTokenCmd := fmt.Sprintf("gitea admin user generate-access-token --username %s --token-name %s --scopes 'write:repository,write:user' --raw",
		GiteaAdminUser, GiteaAPITokenName)
	tokenOut, tokenErr := execGiteaCmd(kubeconfigPath, Namespace, ContextHub, generateTokenCmd)
	if tokenErr != nil {
		return "", "", fmt.Errorf("generate gitea token: %w", tokenErr)
	}
	giteaToken = mustTrimSpace(tokenOut)

	// 3. Create an EMPTY repo via the Gitea REST API. No Sharko-format
	// files are written here — ALL Sharko state (seed-bootstrap files,
	// cluster registrations, addon assignments) goes through Sharko's own
	// REST API afterward, exactly like a real user, via realdoors.go
	// (runGiteaRealDoorsFlow). "Empty" here means what any git host gives
	// you on repo creation with auto-init on: a README, nothing else.
	fmt.Println("    Creating Gitea repository (empty — Sharko state comes later, through the real API)...")

	// Create the repository
	if err := giteaCreateRepo(giteaToken, GiteaRepoName); err != nil {
		return "", "", fmt.Errorf("create gitea repo: %w", err)
	}

	// Build the in-cluster Git URL Sharko will use
	giteaURL = fmt.Sprintf("http://gitea.%s.svc.cluster.local:3000/%s/%s.git", Namespace, GiteaAdminUser, GiteaRepoName)

	fmt.Printf("    Gitea deployed at %s\n", giteaURL)
	fmt.Println("")
	fmt.Println("    Gitea admin credentials (local dev only):")
	fmt.Printf("      Username: %s\n", GiteaAdminUser)
	fmt.Printf("      Password: %s\n", GiteaAdminPassword)
	fmt.Printf("      API Token: %s\n", giteaToken)
	fmt.Println("    Access Gitea UI:")
	fmt.Printf("      kubectl --context %s -n %s port-forward svc/gitea 13000:3000\n", ContextHub, Namespace)
	fmt.Println("      Then open http://localhost:13000")

	return giteaURL, giteaToken, nil
}

// sharkoRolloutTimeout is what installSharko hands to kubectl's own
// --timeout flag on the post-install rollout status check. A named constant
// (not a literal duplicated between the flag string and the outer runCmd
// deadline) so the two can never silently drift back to the same value —
// see argoCDRolloutTimeout's doc comment (argocd_sharko_token.go) for why
// that drift matters.
const sharkoRolloutTimeout = 3 * time.Minute

// installSharko installs Sharko on the hub via direct helm upgrade --install.
// The image (sharko:playground-<sha>) was already built + kind-loaded earlier.
// For Gitea backend, the gitea in-cluster host is allowlisted and no bootstrap connection is set.
// For GitFake, the bootstrap connection points at GitFake.
func installSharko(gitBackend, gitfakeURL, giteaURL string) error {
	fmt.Println("==> Installing Sharko on hub")

	gitSHA := mustRunCmd(10*time.Second, "git", "rev-parse", "--short", "HEAD")
	imageTag := "playground-" + gitSHA

	// Base helm args.
	args := []string{
		"upgrade", "--install", Release, "charts/sharko",
		"--kube-context", ContextHub,
		"--namespace", Namespace,
		"--create-namespace",
		"-f", "charts/sharko/values.yaml",
		"--set", "image.repository=sharko",
		"--set", "image.tag=" + imageTag,
		"--set", "image.pullPolicy=Never",
		"--set", "bootstrapAdmin.password=admin",
		"--wait",
		"--timeout", "5m",
	}

	// Backend-specific settings.
	if gitBackend == "gitea" {
		// For Gitea: allowlist the in-cluster Gitea host so createConnection succeeds.
		// No bootstrap connection — we'll create a proper gitea-typed connection via API.
		args = append(args, "--set", "e2e.gitHostsAllowlist=gitea.sharko.svc.cluster.local")
	} else {
		// For GitFake: set a bootstrap connection pointing at GitFake (github-typed).
		args = append(args,
			"--set", "connection.git.provider=github",
			"--set", "connection.git.repoURL="+gitfakeURL,
		)
	}

	if _, stderr, err := runCmd(10*time.Minute, "helm", args...); err != nil {
		return fmt.Errorf("helm upgrade: %w (stderr=%s)", err, stderr)
	}

	// Belt-and-suspenders: explicitly wait for the Sharko deployment rollout to
	// complete before returning. This ensures the pod is on the new image before
	// registerSpokes runs (fixes cold-launch race where createConnection might hit
	// the pre-rollout pod).
	fmt.Println("    Waiting for Sharko deployment rollout...")
	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	_, stderr, err := runCmd(outerCmdTimeout(sharkoRolloutTimeout), "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "-n", Namespace,
		"rollout", "status", "deploy/"+Release, "--timeout="+sharkoRolloutTimeout.String())
	if err != nil {
		return fmt.Errorf("wait for sharko rollout: %w (stderr=%s)", err, stderr)
	}

	fmt.Println("    Sharko installed")
	return nil
}

// mintArgoCDToken is what hands Sharko the credential for its ArgoCD
// connection. It used to just log in as admin and return that SESSION
// token — but ArgoCD session tokens expire after 24h, and when that
// happened on the live playground every ArgoCD read started returning 401
// and the product broke (verified live). The admin account itself CANNOT
// hold apiKey tokens (also verified live), so there is no way to make
// admin's own token non-expiring.
//
// The fix: provision a dedicated local ArgoCD account ("sharko") with
// apiKey capability + role:admin (both idempotent — steps 1-3 below), then
// use an admin session ONLY to mint that account a token with NO expiry
// (steps 4-5). Admin/admin stays exactly as it was for logging into the
// ArgoCD UI by hand; the token this function returns — the one that ends
// up in Sharko's stored connection — is never admin's own session token.
//
// Steps:
//  1. Ensure argocd-cm declares accounts.sharko: apiKey (ensureSharkoAccountCapability).
//  2. Ensure argocd-rbac-cm grants the sharko account role:admin (ensureSharkoRBACGrant)
//     — composes with grantArgoCDAdminRBAC's own admin grant via the shared
//     ensureRBACGrantLine read-merge-patch cycle (see its doc comment).
//  3. Restart argocd-server + wait for ready, but ONLY if step 1 or 2 actually
//     changed a ConfigMap — on a re-run against an already-provisioned
//     playground this is a no-op and no restart happens.
//  4. Log in as admin via POST /api/v1/session (existing retry loop, unchanged
//     from before this fix) — this session is used ONLY to authorize minting
//     the sharko account's token in step 5.
//  5. POST /api/v1/account/sharko/token with an empty body (no "expiresIn"
//     key — ArgoCD's CreateToken treats that as "never expires") to mint the
//     token Sharko actually gets.
//
// Every step returns a plain error naming what failed and why — there is no
// fallback path that silently hands back the old expiring session token.
func mintArgoCDToken(kubeconfigPath string) (string, error) {
	const argoCDPort = 18443
	const maxAttempts = 5
	const retryDelay = 3 * time.Second

	// 1-3. Provision the sharko account (idempotent) and restart
	// argocd-server only if something actually changed.
	fmt.Println("    Provisioning dedicated sharko ArgoCD account for a non-expiring token...")
	cmChanged, err := ensureSharkoAccountCapability(kubeconfigPath)
	if err != nil {
		return "", fmt.Errorf("grant sharko account apiKey capability in argocd-cm: %w", err)
	}
	rbacChanged, err := ensureSharkoRBACGrant(kubeconfigPath)
	if err != nil {
		return "", fmt.Errorf("grant sharko account role:admin in argocd-rbac-cm: %w", err)
	}
	if cmChanged || rbacChanged {
		if err := restartArgoCDServerAndWait(kubeconfigPath); err != nil {
			return "", fmt.Errorf("restart argocd-server to apply sharko account changes: %w", err)
		}
	} else {
		fmt.Println("    sharko account already provisioned — no argocd-server restart needed")
	}

	// Read ArgoCD admin password from the initial admin secret.
	fmt.Println("    Reading ArgoCD admin password...")
	passwordB64, _, err := runCmd(10*time.Second, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"-n", ArgoCDNamespace,
		"get", "secret", "argocd-initial-admin-secret",
		"-o", "jsonpath={.data.password}")
	if err != nil {
		return "", fmt.Errorf("read argocd-initial-admin-secret: %w", err)
	}
	passwordBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(passwordB64))
	if err != nil {
		return "", fmt.Errorf("decode argocd admin password: %w", err)
	}
	password := strings.TrimSpace(string(passwordBytes))

	// Check if the port is available.
	if isLocalPortInUse(argoCDPort) {
		return "", fmt.Errorf("local port %d is already in use — the playground needs it to mint the ArgoCD token. Stop whatever is using it and re-run", argoCDPort)
	}

	// Start background port-forward to argocd-server. Used for both the
	// admin login below and the account-token mint that follows it.
	fmt.Println("    Starting port-forward to argocd-server...")
	pfCmd, err := startBackground("kubectl",
		"--kubeconfig", kubeconfigPath,
		"--context", ContextHub,
		"-n", ArgoCDNamespace,
		"port-forward", "svc/argocd-server",
		fmt.Sprintf("%d:443", argoCDPort))
	if err != nil {
		return "", fmt.Errorf("start argocd port-forward: %w", err)
	}
	defer func() {
		_ = killProcessGroup(pfCmd)
	}()

	// Wait for argocd-server to be reachable and POST /api/v1/session to log
	// in as admin. Use an insecure TLS client since ArgoCD uses a
	// self-signed cert.
	insecureClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	argoCDBaseURL := fmt.Sprintf("https://localhost:%d", argoCDPort)

	sessionURL := argoCDBaseURL + "/api/v1/session"
	sessionBody := map[string]string{
		"username": "admin",
		"password": password,
	}
	bodyBytes, err := json.Marshal(sessionBody)
	if err != nil {
		return "", fmt.Errorf("marshal session request: %w", err)
	}

	fmt.Println("    Waiting for argocd-server and logging in as admin...")
	var adminSessionToken string
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(retryDelay)
		}

		req, err := http.NewRequest("POST", sessionURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("create session request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := insecureClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("POST /api/v1/session (attempt %d/%d): %w", attempt, maxAttempts, err)
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("POST /api/v1/session (attempt %d/%d): status %d: %s", attempt, maxAttempts, resp.StatusCode, string(respBody))
			continue
		}

		// Parse the token from the response.
		var sessionResp struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(respBody, &sessionResp); err != nil {
			return "", fmt.Errorf("parse session response: %w", err)
		}
		adminSessionToken = sessionResp.Token
		break
	}

	if adminSessionToken == "" {
		return "", fmt.Errorf("log in to argocd as admin: %w", lastErr)
	}

	// Mint the actual token Sharko gets: a NO-EXPIRY apiKey token on the
	// dedicated sharko account, authorized by the admin session above.
	fmt.Println("    Minting non-expiring apiKey token for sharko account...")
	sharkoToken, err := createArgoCDAccountToken(insecureClient, argoCDBaseURL, adminSessionToken, sharkoAccountName)
	if err != nil {
		return "", fmt.Errorf("mint sharko account token: %w", err)
	}

	fmt.Println("    ArgoCD sharko account token minted successfully (no expiry)")
	return sharkoToken, nil
}

// registerSpokes registers the N spokes as Sharko-managed clusters via REST API.
// For Gitea backend, also creates a gitea-typed connection and sets it as active.
func registerSpokes(numSpokes int, spokeNames []string, gitBackend, giteaURL, giteaToken string) error {
	fmt.Println("==> Registering spokes with Sharko")

	// Create ServiceAccounts on each spoke.
	for i := 0; i < numSpokes; i++ {
		clusterName := SpokeClusterName(i)
		if err := createServiceAccountOnCluster(clusterName, ServiceAccountName); err != nil {
			return fmt.Errorf("create SA on %s: %w", clusterName, err)
		}
	}

	// Build in-cluster kubeconfigs for each spoke.
	kubeconfigs := make([]string, numSpokes)
	for i := 0; i < numSpokes; i++ {
		clusterName := SpokeClusterName(i)
		kc, err := buildKubeconfigInCluster(clusterName, ServiceAccountName)
		if err != nil {
			return fmt.Errorf("build kubeconfig for %s: %w", clusterName, err)
		}
		kubeconfigs[i] = kc
	}

	// Start background port-forward to Sharko API.
	fmt.Println("    Starting port-forward to Sharko API...")
	if isLocalPortInUse(8080) {
		return fmt.Errorf("local port 8080 is already in use — the playground needs it to reach Sharko. Stop whatever is using it (e.g. a stale 'sharko serve --demo' or an old kubectl port-forward) and re-run. On macOS/Linux find it with: lsof -nP -iTCP:8080 -sTCP:LISTEN")
	}
	pfCmd, err := startBackground("kubectl", "--context", ContextHub,
		"-n", Namespace, "port-forward", "svc/"+Release, "8080:80")
	if err != nil {
		return fmt.Errorf("start port-forward: %w", err)
	}
	// Ensure the port-forward is killed when we're done (even on error).
	defer func() {
		_ = killProcessGroup(pfCmd)
	}()

	// Wait for Sharko API to become ready.
	sharkoURL := "http://localhost:8080"
	fmt.Println("    Waiting for Sharko API to be ready...")
	if err := waitForSharkoReady(sharkoURL, 60*time.Second); err != nil {
		return fmt.Errorf("wait for Sharko API: %w", err)
	}

	client := newAPIClient(sharkoURL)

	// Login with default admin credentials (admin/admin — now deterministic via Fix 0).
	fmt.Println("    Logging in to Sharko API...")
	if err := client.login("admin", "admin"); err != nil {
		return fmt.Errorf("login to Sharko: %w", err)
	}

	// The spokes are registered by pasting their in-cluster kubeconfigs —
	// the legacy inline path, which is OFF by default (product correction
	// 5). Opt in through the real settings door BEFORE any registration,
	// on both backends (the Gitea real-doors flow below registers by
	// pasting too). The server-side default stays off.
	fmt.Println("    Enabling legacy inline credentials for this sandbox (default is off)")
	if err := client.enableLegacyInlineCredentials(); err != nil {
		return fmt.Errorf("enable legacy inline credentials: %w", err)
	}

	// Gitea backend: drive EVERY piece of Sharko state (connection is
	// infra wiring, but the seed-bootstrap, cluster registrations, and
	// addon assignment all go through Sharko's real REST API doors — see
	// realdoors.go). GitFake backend: keep the pre-existing direct path,
	// since GitFake has no PR/merge REST API to drive the same way (see
	// the doc comment at the top of realdoors.go).
	if gitBackend == "gitea" {
		kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")
		if err := runGiteaRealDoorsFlow(client, kubeconfigPath, numSpokes, spokeNames, kubeconfigs, giteaURL, giteaToken); err != nil {
			return err
		}
		return nil
	}

	// GitFake legacy path — GitFake was pre-seeded with managed-clusters.yaml
	// at pod startup (deployGitFake's SEED_CONTENT), so registration here
	// still uses the v3-style direct addons map. GitFake cannot open/merge
	// real PRs (no such REST API), so it can't run runGiteaRealDoorsFlow.
	for i := 0; i < numSpokes; i++ {
		displayName := spokeNames[i]
		kubeconfig := kubeconfigs[i]
		fmt.Printf("    Registering %s...\n", displayName)
		addons := map[string]bool{"metrics-server": true, "external-secrets": true}
		if err := client.registerCluster(displayName, kubeconfig, addons); err != nil {
			return fmt.Errorf("register %s: %w", displayName, err)
		}
	}

	fmt.Println("    All spokes registered")
	return nil
}

// printSuccessMessage prints access instructions and next steps.
func printSuccessMessage() error {
	fmt.Println("")
	fmt.Println("==> Playground is ready!")
	fmt.Println("")

	// Retrieve ArgoCD initial admin password.
	argoCDPassword := ""
	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	out, _, err := runCmd(10*time.Second, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "-n", ArgoCDNamespace,
		"get", "secret", "argocd-initial-admin-secret",
		"-o", "jsonpath={.data.password}")
	if err != nil {
		// Secret not found or other error — provide fallback instruction.
		argoCDPassword = "(secret not found — retrieve with: kubectl --context " + ContextHub +
			" -n " + ArgoCDNamespace + " get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d)"
	} else {
		// Decode the base64-encoded password.
		decoded, err := base64.StdEncoding.DecodeString(out)
		if err != nil {
			argoCDPassword = "(base64 decode failed)"
		} else {
			argoCDPassword = string(decoded)
		}
	}

	fmt.Println("Credentials (local dev only):")
	fmt.Println("")
	fmt.Println("  Sharko:")
	fmt.Println("    Username: admin")
	fmt.Println("    Password: admin")
	fmt.Println("")
	fmt.Println("  ArgoCD:")
	fmt.Println("    Username: admin")
	fmt.Printf("    Password: %s\n", argoCDPassword)
	fmt.Println("")
	fmt.Println("Access Sharko UI:")
	fmt.Printf("  kubectl --context %s port-forward -n %s svc/%s 8080:80\n", ContextHub, Namespace, Release)
	fmt.Println("  Then open http://localhost:8080")
	fmt.Println("")
	fmt.Println("Access ArgoCD UI:")
	fmt.Printf("  kubectl --context %s port-forward -n %s svc/argocd-server 18080:443\n", ContextHub, ArgoCDNamespace)
	fmt.Println("  Then open https://localhost:18080")
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Println("  make playground-status     # Check current state")
	fmt.Println("  make playground-tunnels    # Open browser tunnels")
	fmt.Println("  make playground-down       # Tear down playground")

	return nil
}

// showStatusSnapshot shells out to scripts/playground-status.sh to
// display the current state of the playground (clusters, addon labels, Gitea state).
func showStatusSnapshot() error {
	fmt.Println("")
	fmt.Println("==> Running status snapshot...")
	_, stderr, err := runCmd(30*time.Second, "sh", "./scripts/playground-status.sh")
	if err != nil {
		return fmt.Errorf("playground-status.sh: %w (stderr=%s)", err, stderr)
	}
	return nil
}

// giteaCreateRepo creates a new repository via the Gitea REST API.
// Retries on transport errors or unexpected status codes (preserves 201/409 success semantics).
func giteaCreateRepo(token, repoName string) error {
	return retryHTTP(5, 2*time.Second, func() error {
		client := newHTTPClient()
		reqBody := fmt.Sprintf(`{"name":"%s","private":false,"auto_init":true,"default_branch":"main"}`, repoName)
		req := mustNewRequest("POST", "http://localhost:13000/api/v1/user/repos", reqBody)
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("gitea create repo request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 201 && resp.StatusCode != 409 {
			return fmt.Errorf("gitea create repo: unexpected status %d", resp.StatusCode)
		}
		return nil
	})
}

// giteaDeleteAccessTokenIfExists deletes a Gitea access token by name via
// the REST API (DELETE /users/{username}/tokens/{token} — Gitea resolves
// that path segment by numeric ID first, then falls back to matching it
// against a token's display name, so the plain name deployGitea mints
// tokens under works here even though generate-access-token never handed
// back an ID). Authenticates with the admin username/password rather than a
// bearer token, since the whole point of calling this is that we may not
// have — or trust — an existing token yet.
//
// A 404 (nothing under that name) is treated as success: this is called
// unconditionally before every token generation, not just on a detected
// conflict, so the token-generation step is rerunnable by construction
// rather than by string-matching gitea's error text.
func giteaDeleteAccessTokenIfExists(username, password, tokenName string) error {
	client := newHTTPClient()
	url := fmt.Sprintf("%s/users/%s/tokens/%s", giteaLocalAPIBase, username, tokenName)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("build gitea delete-token request: %w", err)
	}
	req.SetBasicAuth(username, password)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("delete gitea token %q: %w", tokenName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete gitea token %q: unexpected status %d: %s", tokenName, resp.StatusCode, string(body))
	}
	return nil
}

// generateManagedClustersSeed generates a managed-clusters.yaml seed content
// assigning ~2 addons across the given spoke names.
//
// GitFake-only (PLAYGROUND_GIT_BACKEND=gitfake): baked into the GitFake Pod
// at startup via SEED_CONTENT (see deployGitFake), NOT written by this
// process into a live repo. The Gitea path (the default) never seeds
// Sharko-format files directly — see realdoors.go for why: GitFake has no
// PR/merge REST API for the playground to drive the real seed-bootstrap /
// register / addon-enable doors the way it does against Gitea.
//
// The shape mirrors internal/models.SaveManagedClusters exactly (schema
// header line, then apiVersion/kind, then the clusters list at the SAME
// top level — no metadata block, no spec: wrapper, design doc
// 2026-07-31-catalog-approved-model.md §9). This matters beyond style: the
// bare path "configuration/managed-clusters.yaml" is one of
// orchestrator.V3SecondaryMarkerPath's two v3-repo signals
// (internal/orchestrator/v3_markers.go), so seeding this content at that
// path would make Sharko believe a fresh v4 playground repo is an
// already-bootstrapped v3 one. Writing it at the flat v4 path
// ("managed-clusters.yaml", set as SEED_FILE in deployGitFake) avoids
// that trap.
func generateManagedClustersSeed(spokeNames []string) string {
	// Placeholder: assign metrics-server to spoke0, external-secrets to spoke1.
	// For N>2, assign no addons.
	yaml := "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/managed-clusters.v1.json\n"
	yaml += "apiVersion: sharko.dev/v1\n"
	yaml += "kind: ManagedClusters\n"
	yaml += "clusters:\n"
	for i, name := range spokeNames {
		yaml += fmt.Sprintf("  - name: %s\n", name)
		if i == 0 {
			yaml += "    labels:\n"
			yaml += "      metrics-server: enabled\n"
		} else if i == 1 {
			yaml += "    labels:\n"
			yaml += "      external-secrets: enabled\n"
		}
		// No labels for i > 1 (no addons assigned)
	}
	return yaml
}

// indentMultiline indents each line of s by n spaces.
func indentMultiline(s string, n int) string {
	prefix := ""
	for i := 0; i < n; i++ {
		prefix += " "
	}
	lines := ""
	for _, line := range splitLines(s) {
		lines += prefix + line + "\n"
	}
	return lines
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

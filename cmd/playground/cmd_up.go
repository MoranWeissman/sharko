package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// cmdUp implements the `playground up` subcommand — spin up hub + N spokes,
// install ArgoCD + Sharko + GitFake, register spokes.
func cmdUp(ctx context.Context) error {
	fmt.Println("==> Starting operator playground setup")

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

	// 2. Create or reuse hub kind cluster.
	if err := provisionHub(); err != nil {
		return fmt.Errorf("provision hub: %w", err)
	}

	// 3. Create or reuse spoke kind clusters.
	for i := 0; i < numSpokes; i++ {
		if err := provisionSpoke(i); err != nil {
			return fmt.Errorf("provision spoke %d: %w", i, err)
		}
	}

	// 4. Install ArgoCD on the hub.
	if err := installArgoCD(); err != nil {
		return fmt.Errorf("install ArgoCD: %w", err)
	}

	// 5. Build and load Sharko + GitFake images onto the hub.
	if err := buildAndLoadImages(); err != nil {
		return fmt.Errorf("build/load images: %w", err)
	}

	// 6. Determine which git backend to use (Gitea by default, GitFake when PLAYGROUND_GIT_BACKEND=gitfake).
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

	// 7. Install Sharko on the hub via helm with operator.enabled=true,
	//    operator.drivesLabels=false (start inert). For Gitea backend, allowlist
	//    the in-cluster Gitea host.
	if err := installSharko(gitBackend, gitfakeURL, giteaURL); err != nil {
		return fmt.Errorf("install Sharko: %w", err)
	}

	// 8. Register the N spokes as Sharko-managed clusters via REST API.
	//    For Gitea backend, also create a gitea-typed connection.
	if err := registerSpokes(numSpokes, spokeNames, gitBackend, giteaURL, giteaToken); err != nil {
		return fmt.Errorf("register spokes: %w", err)
	}

	// 9. Print access instructions and next steps.
	if err := printSuccessMessage(); err != nil {
		// Non-fatal — just log the error and continue.
		fmt.Printf("    Warning: could not retrieve all credentials: %v\n", err)
	}

	// 10. Show the status snapshot.
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

	fmt.Println("    ArgoCD installed")
	return nil
}

// buildAndLoadImages builds Sharko + GitFake Docker images and loads them onto the hub.
func buildAndLoadImages() error {
	fmt.Println("==> Building and loading images onto hub")

	// Build GitFake image via Makefile.
	fmt.Println("    Building GitFake image...")
	if _, stderr, err := runCmd(5*time.Minute, "make", "build-gitfake-image"); err != nil {
		return fmt.Errorf("make build-gitfake-image: %w (stderr=%s)", err, stderr)
	}

	// Build Sharko image (Dockerfile at repo root).
	fmt.Println("    Building Sharko image...")
	gitSHA := mustRunCmd(10*time.Second, "git", "rev-parse", "--short", "HEAD")
	sharkoImage := "sharko:playground-" + gitSHA
	if _, stderr, err := runCmd(5*time.Minute, "docker", "build", "-t", sharkoImage, "."); err != nil {
		return fmt.Errorf("docker build sharko: %w (stderr=%s)", err, stderr)
	}

	// Load both images onto hub.
	fmt.Println("    Loading images onto hub cluster...")
	gitfakeImage := "sharko-gitfake:e2e-" + gitSHA
	if _, stderr, err := runCmd(2*time.Minute, "kind", "load", "docker-image", gitfakeImage, "--name", ClusterHub); err != nil {
		return fmt.Errorf("kind load gitfake image: %w (stderr=%s)", err, stderr)
	}
	if _, stderr, err := runCmd(2*time.Minute, "kind", "load", "docker-image", sharkoImage, "--name", ClusterHub); err != nil {
		return fmt.Errorf("kind load sharko image: %w (stderr=%s)", err, stderr)
	}

	fmt.Println("    Images built and loaded")
	return nil
}

// deployGitFake deploys an in-cluster GitFake Pod on the hub, seeded with
// managed-clusters.yaml. Returns the in-cluster service URL.
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
          value: configuration/managed-clusters.yaml
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
	fmt.Println("    Bootstrapping Gitea (creating admin user)...")

	// Create admin user (idempotent — ignore "user already exists" error)
	// Run as the 'git' user (uid 1000) because Gitea CLI refuses to run as root.
	createUserCmd := fmt.Sprintf("gitea admin user create --admin --username %s --password %s --email %s --must-change-password=false",
		GiteaAdminUser, GiteaAdminPassword, GiteaAdminEmail)
	_, stderr, err := runCmd(30*time.Second, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "-n", Namespace,
		"exec", "deploy/gitea", "--",
		"su", "git", "-c", createUserCmd)
	if err != nil && !contains(stderr, "user already exists") && !contains(stderr, "already exists") {
		return "", "", fmt.Errorf("create gitea admin user: %w (stderr=%s)", err, stderr)
	}

	// Generate API token
	fmt.Println("    Generating Gitea API token...")
	// Run as the 'git' user (uid 1000) because Gitea CLI refuses to run as root.
	generateTokenCmd := fmt.Sprintf("gitea admin user generate-access-token --username %s --token-name sharko-playground --scopes 'write:repository,write:user' --raw",
		GiteaAdminUser)
	tokenOut, stderr, err := runCmd(30*time.Second, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "-n", Namespace,
		"exec", "deploy/gitea", "--",
		"su", "git", "-c", generateTokenCmd)
	if err != nil {
		return "", "", fmt.Errorf("generate gitea token: %w (stderr=%s)", err, stderr)
	}
	giteaToken = mustTrimSpace(tokenOut)

	// 3. Create repo + seed files via Gitea REST API
	fmt.Println("    Creating Gitea repository and seeding config files...")

	// Start port-forward to access Gitea API from the playground process
	pfCmd, err := startBackground("kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "-n", Namespace,
		"port-forward", "svc/gitea", "13000:3000")
	if err != nil {
		return "", "", fmt.Errorf("start gitea port-forward: %w", err)
	}
	defer func() {
		_ = killProcessGroup(pfCmd)
	}()

	// Wait a bit for port-forward to be ready
	time.Sleep(2 * time.Second)

	// Create the repository
	if err := giteaCreateRepo(giteaToken, GiteaRepoName); err != nil {
		return "", "", fmt.Errorf("create gitea repo: %w", err)
	}

	// Seed managed-clusters.yaml
	managedClustersContent := generateManagedClustersSeed(spokeNames)
	if err := giteaAddFile(giteaToken, GiteaAdminUser, GiteaRepoName, "configuration/managed-clusters.yaml", managedClustersContent); err != nil {
		return "", "", fmt.Errorf("seed managed-clusters.yaml: %w", err)
	}

	// Seed addons-catalog.yaml
	addonsCatalogContent := generateAddonsCatalogSeed()
	if err := giteaAddFile(giteaToken, GiteaAdminUser, GiteaRepoName, "configuration/addons-catalog.yaml", addonsCatalogContent); err != nil {
		return "", "", fmt.Errorf("seed addons-catalog.yaml: %w", err)
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
		"--set", "operator.enabled=true",
		"--set", "operator.drivesLabels=false",
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
	_, stderr, err := runCmd(3*time.Minute, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", ContextHub, "-n", Namespace,
		"rollout", "status", "deploy/"+Release, "--timeout=180s")
	if err != nil {
		return fmt.Errorf("wait for sharko rollout: %w (stderr=%s)", err, stderr)
	}

	fmt.Println("    Sharko installed")
	return nil
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

	// For Gitea backend, create a gitea-typed connection and set it as active.
	// This gives Sharko an active connection pointing at the in-cluster Gitea.
	// For GitFake, skip this step (GitFake is wired via the bootstrap connection or env vars).
	if gitBackend == "gitea" {
		fmt.Println("    Creating Gitea connection...")
		argocdServerURL := fmt.Sprintf("http://argocd-server.%s.svc.cluster.local", ArgoCDNamespace)
		if err := client.createConnection(
			GiteaConnectionName,
			"gitea",
			giteaURL,
			giteaToken,
			argocdServerURL,
			ArgoCDNamespace,
			"", // empty argocd token — in-cluster uses service account
		); err != nil {
			return fmt.Errorf("create gitea connection: %w", err)
		}
		fmt.Printf("    Gitea connection '%s' created and set as active\n", GiteaConnectionName)
	}

	// Register each spoke.
	for i := 0; i < numSpokes; i++ {
		displayName := spokeNames[i]
		kubeconfig := kubeconfigs[i]
		fmt.Printf("    Registering %s...\n", displayName)
		// Assign a couple of addons per spoke (placeholder — the seed content
		// already assigns them, but the REST API expects the addon list).
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
	fmt.Println("  make operator-playground-status     # Check current state")
	fmt.Println("  make operator-playground-drive-on   # Flip operator drive ON")
	fmt.Println("  make operator-playground-drive-off  # Flip operator drive OFF")
	fmt.Println("  make operator-playground-down       # Tear down playground")

	return nil
}

// showStatusSnapshot shells out to scripts/operator-playground-status.sh to
// display the current state of the playground (clusters, ClusterAddons, drive mode).
func showStatusSnapshot() error {
	fmt.Println("")
	fmt.Println("==> Running status snapshot...")
	_, stderr, err := runCmd(30*time.Second, "sh", "./scripts/operator-playground-status.sh")
	if err != nil {
		return fmt.Errorf("operator-playground-status.sh: %w (stderr=%s)", err, stderr)
	}
	return nil
}

// giteaCreateRepo creates a new repository via the Gitea REST API.
func giteaCreateRepo(token, repoName string) error {
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
}

// giteaAddFile adds or updates a file in a Gitea repository via the Contents API.
func giteaAddFile(token, owner, repo, filePath, content string) error {
	client := newHTTPClient()
	encodedContent := base64.StdEncoding.EncodeToString([]byte(content))
	reqBody := fmt.Sprintf(`{"branch":"main","content":"%s","message":"Add %s"}`, encodedContent, filePath)

	url := fmt.Sprintf("http://localhost:13000/api/v1/repos/%s/%s/contents/%s", owner, repo, filePath)
	req := mustNewRequest("POST", url, reqBody)
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gitea add file request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		return fmt.Errorf("gitea add file %s: unexpected status %d", filePath, resp.StatusCode)
	}
	return nil
}

// generateAddonsCatalogSeed generates the addons-catalog.yaml seed content
// with the two addons registerSpokes assigns (metrics-server + external-secrets).
func generateAddonsCatalogSeed() string {
	return `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json
apiVersion: sharko.dev/v1alpha1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: metrics-server
      chart: metrics-server
      repoURL: https://kubernetes-sigs.github.io/metrics-server/
      version: "3.12.1"
      namespace: kube-system

    - name: external-secrets
      chart: external-secrets
      repoURL: https://charts.external-secrets.io
      version: "0.10.0"
      namespace: external-secrets-system
`
}

// generateManagedClustersSeed generates a managed-clusters.yaml seed content
// assigning ~2 addons across the given spoke names.
func generateManagedClustersSeed(spokeNames []string) string {
	// Placeholder: assign metrics-server to spoke-eu, external-secrets to spoke-us.
	// For N>2, alternate addons or assign none.
	yaml := "apiVersion: sharko.dev/v1alpha1\n"
	yaml += "kind: ManagedClusters\n"
	yaml += "metadata:\n"
	yaml += "  name: managed-clusters\n"
	yaml += "clusters:\n"
	for i, name := range spokeNames {
		yaml += fmt.Sprintf("- name: %s\n", name)
		yaml += "  addons:\n"
		if i == 0 {
			yaml += "  - name: metrics-server\n"
			yaml += "    version: \"0.7.0\"\n"
		} else if i == 1 {
			yaml += "  - name: external-secrets\n"
			yaml += "    version: \"0.10.0\"\n"
		} else {
			yaml += "  # No addons assigned\n"
		}
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

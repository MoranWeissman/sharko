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

	// 6. Deploy in-cluster GitFake Pod on the hub, seeded with managed-clusters.yaml.
	gitfakeURL, err := deployGitFake(spokeNames)
	if err != nil {
		return fmt.Errorf("deploy GitFake: %w", err)
	}

	// 7. Install Sharko on the hub via scripts/helm-install.sh with operator.enabled=true,
	//    operator.drivesLabels=false (start inert), pointing at GitFake.
	if err := installSharko(gitfakeURL); err != nil {
		return fmt.Errorf("install Sharko: %w", err)
	}

	// 8. Register the N spokes as Sharko-managed clusters via REST API.
	if err := registerSpokes(numSpokes, spokeNames); err != nil {
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

// installSharko installs Sharko on the hub via scripts/helm-install.sh.
func installSharko(gitfakeURL string) error {
	fmt.Println("==> Installing Sharko on hub")

	gitSHA := mustRunCmd(10*time.Second, "git", "rev-parse", "--short", "HEAD")
	sharkoImage := "sharko:playground-" + gitSHA

	// Call scripts/helm-install.sh with environment overrides.
	// We pass a dummy GITHUB_TOKEN since GitFake needs none.
	// Set bootstrapAdmin.password=admin for deterministic login.
	cmd := fmt.Sprintf(`
KIND_CLUSTER_NAME=%s \
SHARKO_IMAGE=%s \
GITHUB_TOKEN=dummy-token-for-gitfake \
GIT_REPO_URL=%s \
./scripts/helm-install.sh --set operator.enabled=true --set operator.drivesLabels=false --set bootstrapAdmin.password=admin
`, ClusterHub, sharkoImage, gitfakeURL)

	if _, stderr, err := runCmd(5*time.Minute, "sh", "-c", cmd); err != nil {
		return fmt.Errorf("helm-install.sh: %w (stderr=%s)", err, stderr)
	}

	fmt.Println("    Sharko installed")
	return nil
}

// registerSpokes registers the N spokes as Sharko-managed clusters via REST API.
func registerSpokes(numSpokes int, spokeNames []string) error {
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

	// Register each spoke.
	for i := 0; i < numSpokes; i++ {
		displayName := spokeNames[i]
		kubeconfig := kubeconfigs[i]
		fmt.Printf("    Registering %s...\n", displayName)
		// Assign a couple of addons per spoke (placeholder — the seed content
		// already assigns them, but the REST API expects the addon list).
		addons := []string{"metrics-server", "external-secrets"}
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

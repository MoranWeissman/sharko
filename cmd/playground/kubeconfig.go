package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// buildKubeconfigInCluster assembles a kubeconfig YAML pointing at the given
// kind cluster's control-plane via the Docker network IP. The resulting
// kubeconfig is reachable from inside Docker (so in-cluster ArgoCD can use
// it to reach the spoke).
//
// This is the recipe from tests/e2e/harness/fixtures.go::BuildKubeconfig,
// lifted here so the playground command (untagged) can reuse it without
// importing the e2e-tagged harness.
//
// clusterName is the kind cluster name (e.g. "sharko-play-spoke-1").
// saName is the ServiceAccount whose bearer token authenticates the kubeconfig.
// CreateServiceAccountOnCluster must have been called first to create the SA.
func buildKubeconfigInCluster(clusterName, saName string) (string, error) {
	// 1. Look up the control-plane container's IP on its Docker network.
	//    kind names the container "<cluster-name>-control-plane".
	containerName := clusterName + "-control-plane"
	ip, err := dockerContainerIP(containerName)
	if err != nil {
		return "", fmt.Errorf("buildKubeconfigInCluster(%s): %w", clusterName, err)
	}

	// 2. Pull the cluster CA cert (base64) from the existing kubeconfig
	//    so the assembled kubeconfig validates the API server's TLS.
	//    kind writes the kubeconfig to ~/.kube/config by default.
	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	context := "kind-" + clusterName
	caData, err := kubectlConfigView(kubeconfigPath, context, "{.clusters[0].cluster.certificate-authority-data}")
	if err != nil {
		return "", fmt.Errorf("buildKubeconfigInCluster(%s): read CA: %w", clusterName, err)
	}

	// 3. Fetch the SA token (CreateServiceAccountOnCluster must have run).
	token, err := kubectlCreateToken(kubeconfigPath, context, saName, "1h")
	if err != nil {
		return "", fmt.Errorf("buildKubeconfigInCluster(%s): create token: %w", clusterName, err)
	}

	// 4. Assemble the kubeconfig YAML. Hand-written rather than via
	//    yaml.Marshal so the output is stable + readable.
	server := fmt.Sprintf("https://%s:6443", ip)
	yaml := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"- name: " + clusterName + "\n" +
		"  cluster:\n" +
		"    server: " + server + "\n" +
		"    certificate-authority-data: " + caData + "\n" +
		"contexts:\n" +
		"- name: " + context + "\n" +
		"  context:\n" +
		"    cluster: " + clusterName + "\n" +
		"    user: " + saName + "\n" +
		"current-context: " + context + "\n" +
		"users:\n" +
		"- name: " + saName + "\n" +
		"  user:\n" +
		"    token: " + token + "\n"
	return yaml, nil
}

// createServiceAccountOnCluster creates a ServiceAccount + cluster-admin
// ClusterRoleBinding in the cluster's default namespace.
//
// Idempotent: if the SA already exists the create is a no-op.
//
// This is the recipe from tests/e2e/harness/fixtures.go::CreateServiceAccountToken,
// lifted here for playground use.
func createServiceAccountOnCluster(clusterName, saName string) error {
	kubeconfigPath := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	// Create SA (idempotent — ignore "already exists").
	_, stderr, err := runCmd(15, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", "kind-"+clusterName,
		"create", "sa", saName)
	if err != nil && !contains(stderr, "already exists") {
		return fmt.Errorf("create sa %s on %s: %w (stderr=%s)", saName, clusterName, err, stderr)
	}

	// Bind to cluster-admin (idempotent — ignore "already exists").
	_, stderr, err = runCmd(15, "kubectl", "--kubeconfig", kubeconfigPath,
		"--context", "kind-"+clusterName,
		"create", "clusterrolebinding", saName+"-cluster-admin",
		"--clusterrole=cluster-admin",
		"--serviceaccount=default:"+saName)
	if err != nil && !contains(stderr, "already exists") {
		return fmt.Errorf("bind sa %s to cluster-admin on %s: %w (stderr=%s)", saName, clusterName, err, stderr)
	}

	return nil
}

// contains returns true if s contains substr (case-sensitive).
func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && indexOfSubstring(s, substr) >= 0
}

func indexOfSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runCmd executes a command and returns stdout, stderr, and error.
// It times out after the given duration (use 0 for no timeout).
func runCmd(timeout time.Duration, name string, args ...string) (stdout, stderr string, err error) {
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
	} else {
		ctx = context.Background()
	}

	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// mustRunCmd runs a command and panics if it fails. Use for commands that
// must succeed for the playground to function (e.g. kubectl, kind).
func mustRunCmd(timeout time.Duration, name string, args ...string) string {
	stdout, stderr, err := runCmd(timeout, name, args...)
	if err != nil {
		panic(fmt.Sprintf("command failed: %s %v\nstdout=%s\nstderr=%s\nerr=%v",
			name, args, stdout, stderr, err))
	}
	return strings.TrimSpace(stdout)
}

// kindClusterExists returns true if the named kind cluster exists.
func kindClusterExists(name string) bool {
	out, _, err := runCmd(5*time.Second, "kind", "get", "clusters")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// dockerContainerIP returns the IP address of a Docker container on its
// primary network (the kind bridge). This is the IP ArgoCD on the hub
// uses to reach spoke control planes.
func dockerContainerIP(containerName string) (string, error) {
	out, stderr, err := runCmd(10*time.Second, "docker", "inspect", "-f",
		"{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
		containerName)
	if err != nil {
		return "", fmt.Errorf("docker inspect %s: %w (stderr=%s)", containerName, err, stderr)
	}
	ip := strings.TrimSpace(out)
	if ip == "" {
		return "", fmt.Errorf("docker inspect %s: empty IP", containerName)
	}
	return ip, nil
}

// kubectlConfigView extracts a field from a kubeconfig using kubectl config view.
func kubectlConfigView(kubeconfig, context, jsonpath string) (string, error) {
	out, stderr, err := runCmd(15*time.Second, "kubectl", "config", "view",
		"--kubeconfig", kubeconfig,
		"--context", context,
		"--minify",
		"-o", "jsonpath="+jsonpath)
	if err != nil {
		return "", fmt.Errorf("kubectl config view: %w (stderr=%s)", err, stderr)
	}
	return strings.TrimSpace(out), nil
}

// kubectlCreateToken creates a short-lived (1h) bearer token for a ServiceAccount.
func kubectlCreateToken(kubeconfig, saName, duration string) (string, error) {
	out, stderr, err := runCmd(15*time.Second, "kubectl", "--kubeconfig", kubeconfig,
		"create", "token", saName, "--duration", duration)
	if err != nil {
		return "", fmt.Errorf("kubectl create token: %w (stderr=%s)", err, stderr)
	}
	return strings.TrimSpace(out), nil
}

// kubectlApply applies a YAML manifest (from stdin or file).
func kubectlApply(kubeconfig, namespace, yaml string) error {
	cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig, "apply", "-n", namespace, "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply: %w (stderr=%s)", err, stderr.String())
	}
	return nil
}

// kubectlWait waits for a resource to be ready.
func kubectlWait(kubeconfig, namespace, resourceType, resourceName, condition string, timeout time.Duration) error {
	_, stderr, err := runCmd(timeout, "kubectl", "--kubeconfig", kubeconfig,
		"-n", namespace, "wait",
		"--for=condition="+condition,
		"--timeout="+timeout.String(),
		resourceType+"/"+resourceName)
	if err != nil {
		return fmt.Errorf("kubectl wait: %w (stderr=%s)", err, stderr)
	}
	return nil
}

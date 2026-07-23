package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
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
		"--raw",
		"-o", "jsonpath="+jsonpath)
	if err != nil {
		return "", fmt.Errorf("kubectl config view: %w (stderr=%s)", err, stderr)
	}
	return strings.TrimSpace(out), nil
}

// kubectlCreateToken creates a short-lived (1h) bearer token for a ServiceAccount.
func kubectlCreateToken(kubeconfig, kubectlContext, saName, duration string) (string, error) {
	out, stderr, err := runCmd(15*time.Second, "kubectl", "--kubeconfig", kubeconfig,
		"--context", kubectlContext,
		"create", "token", saName, "--duration", duration)
	if err != nil {
		return "", fmt.Errorf("kubectl create token: %w (stderr=%s)", err, stderr)
	}
	return strings.TrimSpace(out), nil
}

// kubectlApply applies a YAML manifest (from stdin or file).
func kubectlApply(kubeconfig, kubectlContext, namespace, yaml string) error {
	cmd := exec.Command("kubectl", "--kubeconfig", kubeconfig, "--context", kubectlContext, "apply", "-n", namespace, "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply: %w (stderr=%s)", err, stderr.String())
	}
	return nil
}

// kubectlWait waits for a resource to be ready.
func kubectlWait(kubeconfig, kubectlContext, namespace, resourceType, resourceName, condition string, timeout time.Duration) error {
	_, stderr, err := runCmd(timeout, "kubectl", "--kubeconfig", kubeconfig,
		"--context", kubectlContext,
		"-n", namespace, "wait",
		"--for=condition="+condition,
		"--timeout="+timeout.String(),
		resourceType+"/"+resourceName)
	if err != nil {
		return fmt.Errorf("kubectl wait: %w (stderr=%s)", err, stderr)
	}
	return nil
}

// startBackground starts a command in the background (does not wait for it to finish).
// The process is placed in its own process group so it can be reliably killed via
// the negative PGID. Returns the *exec.Cmd handle so the caller can kill it later.
func startBackground(name string, args ...string) (*exec.Cmd, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}
	return cmd, nil
}

// killProcessGroup kills the process and its entire process group.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Kill the entire process group (negative PID).
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		// Process already gone or not in a group; fallback to killing just the process.
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// mustTrimSpace trims whitespace from s and panics if s is empty after trimming.
func mustTrimSpace(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		panic("mustTrimSpace: empty string after trimming")
	}
	return trimmed
}

// newHTTPClient creates a simple HTTP client with a reasonable timeout.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// mustNewRequest creates a new HTTP request and panics on error.
func mustNewRequest(method, url, body string) *http.Request {
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		panic(fmt.Sprintf("mustNewRequest: %v", err))
	}
	return req
}

// waitForGiteaAPI polls the Gitea version endpoint through the local port-forward
// until it answers (tunnel established + server ready) or the timeout expires.
func waitForGiteaAPI(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := newHTTPClient()
	url := baseURL + "/api/v1/version"
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("gitea API not ready at %s within %s: %w", url, timeout, lastErr)
}

// retryHTTP retries a function up to 'attempts' times with 'delay' backoff between attempts.
// The function should return nil on success, error on failure (either transport or unexpected status).
func retryHTTP(attempts int, delay time.Duration, fn func() error) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
			if i < attempts-1 {
				fmt.Printf("      Attempt %d/%d failed (%v), retrying in %s...\n", i+1, attempts, err, delay)
				time.Sleep(delay)
			}
		}
	}
	return fmt.Errorf("failed after %d attempts: %w", attempts, lastErr)
}

// isLocalPortInUse returns true if something is already listening on the given TCP port on localhost.
func isLocalPortInUse(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true // couldn't bind → something's using it (or perms) → treat as in-use
	}
	_ = ln.Close()
	return false
}

// execGiteaCmd runs a gitea CLI command inside the gitea pod as the git user,
// retrying to absorb the DB-not-ready startup race (sqlite single-writer vs the
// server's own boot-time init). Returns stdout on success.
func execGiteaCmd(kubeconfigPath, namespace, kubectlContext, giteaCmd string) (string, error) {
	const maxAttempts = 10
	const delay = 6 * time.Second
	var lastStdout, lastStderr string
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stdout, stderr, err := runCmd(60*time.Second, "kubectl", "--kubeconfig", kubeconfigPath,
			"--context", kubectlContext, "-n", namespace,
			"exec", "deploy/gitea", "--", "su", "git", "-c", giteaCmd)
		if err == nil {
			return stdout, nil
		}
		lastStdout, lastStderr, lastErr = stdout, stderr, err
		// stderr from kubectl exec is often empty on a swallowed pod error, so log attempt.
		fmt.Printf("      gitea exec attempt %d/%d failed (retrying in %s): %v %s\n", attempt, maxAttempts, delay, err, stderr)
		time.Sleep(delay)
	}
	return lastStdout, fmt.Errorf("gitea exec failed after %d attempts: %w (stderr=%s)", maxAttempts, lastErr, lastStderr)
}

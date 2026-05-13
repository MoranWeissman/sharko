//go:build e2e

package harness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Eventually polls fn until it returns true or timeout. Logs progress
// every 5 attempts so flaky failures are debuggable.
//
// Standard pattern for waiting on async sharko/argocd state. Calls
// t.Fatalf on timeout with the supplied message.
func Eventually(t *testing.T, timeout time.Duration, fn func() bool, msgAndArgs ...any) {
	t.Helper()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	interval := 100 * time.Millisecond
	attempt := 0
	for time.Now().Before(deadline) {
		attempt++
		if fn() {
			return
		}
		if attempt%5 == 0 {
			t.Logf("Eventually: still waiting after %d attempts (%s elapsed)",
				attempt, time.Since(deadline.Add(-timeout)).Round(100*time.Millisecond))
		}
		time.Sleep(interval)
	}
	msg := "Eventually: condition never became true within " + timeout.String()
	if len(msgAndArgs) > 0 {
		if format, ok := msgAndArgs[0].(string); ok {
			msg += " — " + fmt.Sprintf(format, msgAndArgs[1:]...)
		}
	}
	t.Fatal(msg)
}

// EventuallyNoError polls fn until it returns nil or timeout.
//
// Use when the condition function naturally returns an error (e.g. an
// HTTP probe). The most-recent error is included in the t.Fatalf
// message so flaky failures point at the actual cause.
func EventuallyNoError(t *testing.T, timeout time.Duration, fn func() error) {
	t.Helper()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	interval := 100 * time.Millisecond
	var lastErr error
	for time.Now().Before(deadline) {
		if err := fn(); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(interval)
	}
	t.Fatalf("EventuallyNoError: never returned nil within %s; last err=%v", timeout, lastErr)
}

// BuildKubeconfig assembles a kubeconfig YAML pointing at the given
// kind cluster's control-plane via the Docker network IP. The resulting
// kubeconfig is reachable from inside Docker (so in-cluster sharko +
// argocd can use it); the host-only "127.0.0.1:<port>" kubeconfigs that
// `kind export kubeconfig` writes by default do NOT work in-cluster.
//
// Lifted from smoke.sh Phase 6/7 — the same kubeconfig assembly that
// the maintainer's smoke script uses for cluster registration. Returns
// the kubeconfig as a YAML string ready to paste into the
// register-cluster API.
//
// saName is the ServiceAccount whose bearer token authenticates the
// kubeconfig. CreateServiceAccountToken must have been called first to
// create the SA + cluster-admin binding.
func BuildKubeconfig(t *testing.T, cluster KindCluster, saName string) string {
	t.Helper()

	// 1. Look up the control-plane container's IP on its Docker network.
	//    `kind` names the container "<cluster-name>-control-plane".
	containerName := cluster.Name + "-control-plane"
	ip, err := dockerInspectIP(containerName)
	if err != nil {
		t.Fatalf("BuildKubeconfig(%s): docker inspect IP: %v", cluster.Name, err)
	}

	// 2. Pull the cluster CA cert (base64) from the existing kubeconfig
	//    so the assembled kubeconfig validates the API server's TLS.
	caData, err := kubectlConfigView(cluster.Kubeconfig, cluster.Context, "{.clusters[0].cluster.certificate-authority-data}")
	if err != nil {
		t.Fatalf("BuildKubeconfig(%s): read CA: %v", cluster.Name, err)
	}

	// 3. Fetch the SA token (CreateServiceAccountToken must have run).
	token, err := kubectlCreateToken(cluster.Kubeconfig, saName, "1h")
	if err != nil {
		t.Fatalf("BuildKubeconfig(%s): create token: %v", cluster.Name, err)
	}

	// 4. Assemble the kubeconfig YAML. Hand-written rather than via
	//    yaml.Marshal so the output is stable + readable in test logs.
	server := fmt.Sprintf("https://%s:6443", ip)
	yaml := "apiVersion: v1\n" +
		"kind: Config\n" +
		"clusters:\n" +
		"- name: " + cluster.Name + "\n" +
		"  cluster:\n" +
		"    server: " + server + "\n" +
		"    certificate-authority-data: " + caData + "\n" +
		"contexts:\n" +
		"- name: " + cluster.Context + "\n" +
		"  context:\n" +
		"    cluster: " + cluster.Name + "\n" +
		"    user: " + saName + "\n" +
		"current-context: " + cluster.Context + "\n" +
		"users:\n" +
		"- name: " + saName + "\n" +
		"  user:\n" +
		"    token: " + token + "\n"
	return yaml
}

// CreateServiceAccountToken creates a ServiceAccount + cluster-admin
// ClusterRoleBinding in cluster's default namespace and returns a
// 1-hour bearer token for that SA.
//
// Idempotent: if the SA already exists the create is a no-op; the
// rolebinding is similarly create-or-skip. The token is fresh on every
// call (kubectl create token is by-design ephemeral).
//
// Calls t.Fatalf on any kubectl failure.
func CreateServiceAccountToken(t *testing.T, cluster KindCluster, saName string) string {
	t.Helper()
	kc := cluster.Kubeconfig

	// Create SA (ignore "already exists" for idempotency).
	if _, _, err := runCmd(15*time.Second, "kubectl", "--kubeconfig", kc,
		"create", "sa", saName, "--dry-run=client", "-o", "yaml"); err != nil {
		t.Fatalf("CreateServiceAccountToken: dry-run sa: %v", err)
	}
	if out, _, err := runCmd(15*time.Second, "kubectl", "--kubeconfig", kc,
		"create", "sa", saName); err != nil && !strings.Contains(out, "already exists") {
		t.Fatalf("CreateServiceAccountToken: create sa: %v\n%s", err, out)
	}

	// Bind cluster-admin (idempotent).
	bindingName := saName + "-admin"
	if out, _, err := runCmd(15*time.Second, "kubectl", "--kubeconfig", kc,
		"create", "clusterrolebinding", bindingName,
		"--clusterrole=cluster-admin", "--serviceaccount=default:"+saName,
	); err != nil && !strings.Contains(out, "already exists") {
		t.Fatalf("CreateServiceAccountToken: create binding: %v\n%s", err, out)
	}

	// Mint token.
	token, err := kubectlCreateToken(kc, saName, "1h")
	if err != nil {
		t.Fatalf("CreateServiceAccountToken: token: %v", err)
	}
	return token
}

// RandSuffix returns a short timestamp+random suffix for collision-free
// resource naming across reruns. Format: 6-7 lower-hex chars.
//
// Use to disambiguate cluster names, repo names, etc., across tests
// that may run in the same kind topology over multiple iterations.
func RandSuffix() string {
	buf := make([]byte, 3)
	if _, err := rand.Read(buf); err != nil {
		// Fall back to time only — better than panicking in a test.
		return fmt.Sprintf("%06x", time.Now().UnixNano()&0xffffff)
	}
	return hex.EncodeToString(buf)
}

// MustJSON marshals v to JSON or t.Fatal. Saves typing in test bodies
// that need to compose request payloads.
func MustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("MustJSON: %v", err)
	}
	return out
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

// runCmd executes name+args with the given timeout and returns
// (stdout, stderr, error). Used by the kubeconfig builders below.
func runCmd(timeout time.Duration, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// dockerInspectIP returns the first network IP for the named container.
// Used by BuildKubeconfig to assemble a kubeconfig reachable from
// inside Docker (the Kind container's bridge IP, NOT 127.0.0.1).
func dockerInspectIP(container string) (string, error) {
	out, stderr, err := runCmd(10*time.Second, "docker", "inspect",
		"-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}",
		container,
	)
	if err != nil {
		return "", fmt.Errorf("docker inspect %s: %w (stderr=%s)", container, err, stderr)
	}
	ip := strings.TrimSpace(out)
	if ip == "" {
		return "", fmt.Errorf("docker inspect %s: no IP", container)
	}
	return ip, nil
}

// kubectlConfigView reads a single jsonpath value out of the cluster's
// kubeconfig. Equivalent to `kubectl --kubeconfig X config view --raw
// -o jsonpath=Y`. Used by BuildKubeconfig to extract the CA cert.
func kubectlConfigView(kubeconfig, _ /*context*/, jsonpath string) (string, error) {
	out, stderr, err := runCmd(10*time.Second, "kubectl",
		"--kubeconfig", kubeconfig,
		"config", "view", "--raw", "--minify",
		"-o", "jsonpath="+jsonpath,
	)
	if err != nil {
		return "", fmt.Errorf("kubectl config view: %w (stderr=%s)", err, stderr)
	}
	return strings.TrimSpace(out), nil
}

// kubectlCreateToken mints a service-account bearer token via
// `kubectl create token`. Returns the token string (no leading/trailing
// whitespace).
func kubectlCreateToken(kubeconfig, sa, duration string) (string, error) {
	out, stderr, err := runCmd(15*time.Second, "kubectl",
		"--kubeconfig", kubeconfig,
		"create", "token", sa,
		"--duration="+duration,
	)
	if err != nil {
		return "", fmt.Errorf("kubectl create token %s: %w (stderr=%s)", sa, err, stderr)
	}
	return strings.TrimSpace(out), nil
}

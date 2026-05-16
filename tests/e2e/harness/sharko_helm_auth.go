//go:build e2e

package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// ---------------------------------------------------------------------------
// Story V125-1-13.2 — In-cluster auth bootstrap for Helm-mode Sharko
//
// bootstrapHelmSharkoAuth turns a freshly-helm-installed Sharko (Story 13.1's
// *HelmHandle) into a host-reachable, authenticated *AuthBundle that the
// typed API client (apiclient.go) can consume. It mirrors the pattern the
// argocd lifecycle helpers use (tests/e2e/lifecycle/cluster_helpers.go) but
// reads the Sharko-side bootstrap admin secret instead of ArgoCD's.
//
// The flow:
//   1. Read sharko-initial-admin-secret (V124-6.3 + V124-7.1 format:
//      data.username + data.password, the secret created by the bootstrap
//      auth-store on first start and rewritten by `sharko reset-admin`).
//   2. Spawn `kubectl port-forward svc/sharko -n <ns> :<svcPort>` — the
//      ":<svcPort>" form asks kubectl for a random local port, which
//      avoids conflicts under parallel tests. Parse the local port out of
//      kubectl's "Forwarding from 127.0.0.1:NNNN -> ..." stdout line.
//   3. Poll http://127.0.0.1:<localPort>/api/v1/health for 200 — proves
//      the port-forward + the in-cluster Sharko pod are both healthy.
//   4. POST /api/v1/auth/login with the bootstrap admin/password JSON to
//      mint a JWT. Same shape as Client.Login in apiclient.go.
//   5. Register t.Cleanup to kill the port-forward subprocess (the test's
//      defer would NOT cover it because t.Cleanup runs in LIFO after the
//      test body, including its own t.Cleanup-registered cleanups).
//
// OQ #4 + OQ #5 (resolved 2026-05-15): the secret is read directly via the
// K8s API rather than via `kubectl get secret -o jsonpath` (matches V124-6.3
// + V124-7.1 ArgoCD-style flow + the cmd/sharko/reset_admin.go pattern); the
// port-forward is a kubectl subprocess (operator-friendly + simpler debug
// story than the in-process k8s.io/client-go/tools/portforward path).
//
// Story 13.3 wires SharkoModeHelm to call installSharkoHelm +
// bootstrapHelmSharkoAuth in sequence; Wave D's tests are the first to
// exercise this path end-to-end.
// ---------------------------------------------------------------------------

const (
	// initialAdminSecretName is the bootstrap admin Secret name written by
	// the auth-store on first start (and rewritten by `sharko reset-admin`).
	// Mirrors cmd/sharko/reset_admin.go::initialAdminSecretName + the auth
	// package constant of the same name. Duplicated here rather than
	// imported because the harness package's //go:build e2e tag would force
	// the import out of the default build.
	initialAdminSecretName = "sharko-initial-admin-secret"

	// defaultHelmServicePort is the in-cluster service port the harness's
	// chart values pin (charts/sharko/values.yaml: service.port=80). Story
	// 13.1's HelmHandle.BaseURL bakes in the same number, so we use it
	// here for the port-forward target side rather than re-deriving it
	// from the Service object.
	defaultHelmServicePort = 80

	// defaultPortForwardReadyTimeout caps how long we wait for the
	// kubectl port-forward subprocess to print its "Forwarding from..."
	// line AND for /api/v1/health to start responding 200. Generous enough
	// for cold CI nodes; short enough to fail fast in local dev.
	defaultPortForwardReadyTimeout = 30 * time.Second

	// defaultBootstrapSecretWait caps how long we poll the K8s API for
	// sharko-initial-admin-secret. The auth-store writes it during boot;
	// helm --wait already gates pod-Ready, but the secret write can race a
	// few hundred milliseconds behind the readiness probe in practice.
	defaultBootstrapSecretWait = 60 * time.Second

	// defaultLoginTimeout caps the POST /api/v1/auth/login round trip.
	// Sharko's login handler is rate-limited (5/min/IP) but on a fresh
	// install the limiter has no history; 10s is plenty.
	defaultLoginTimeout = 10 * time.Second
)

// AuthBundle is the host-reachable, authenticated entrypoint to a
// Helm-installed Sharko. Returned by bootstrapHelmSharkoAuth. Story 13.3's
// SharkoModeHelm wires AuthBundle.BaseURL into Sharko.URL and AuthBundle.Token
// into Sharko.AdminToken so the existing typed API client (apiclient.go) can
// consume the helm-mode install identically to the in-process mode.
//
// Public fields are intentionally minimal — internal subprocess + cleanup
// state is hidden so callers cannot accidentally Kill the port-forward and
// race with the t.Cleanup-registered hook.
type AuthBundle struct {
	// BaseURL is the host-reachable URL of the in-cluster Sharko service,
	// fronted by a kubectl port-forward subprocess. Format:
	// "http://127.0.0.1:<localPort>". The local port is random per call
	// to support parallel tests.
	BaseURL string

	// Token is a freshly-minted JWT for the bootstrap admin user. Pass
	// directly to apiclient.Client (via SetToken) or use it in a custom
	// "Authorization: Bearer <token>" header.
	Token string

	// AdminUser is the bootstrap admin username from the Secret (always
	// "admin" today, but read from data.username for future-proofing
	// against secret-format changes).
	AdminUser string

	// AdminPass is the plaintext bootstrap admin password from the
	// Secret. Needed by callers that want to drive Login/Refresh
	// themselves (e.g. tests asserting the rate-limiter behaviour).
	AdminPass string
}

// bootstrapHelmSharkoAuth performs the full secret-read → port-forward →
// health-poll → login sequence and returns a *AuthBundle ready for the typed
// API client.
//
// Calls t.Helper but does NOT call t.Fatalf — returns the error so callers
// (Story 13.3's SharkoModeHelm wrapper) can layer their own t.Fatalf with
// installSharkoHelm context.
//
// Side effects:
//   - Spawns one `kubectl port-forward` subprocess per call. Killed via
//     t.Cleanup (LIFO ordering puts this after Story 13.1's helm uninstall,
//     which is the desired ordering — uninstall closes the svc endpoints
//     anyway, but explicit kill keeps the subprocess from lingering past
//     the test).
//   - Holds no goroutines past return EXCEPT two log-drain goroutines
//     (one per pipe) that exit cleanly when the subprocess closes its
//     stdio at kill time.
func bootstrapHelmSharkoAuth(t *testing.T, helmHandle *HelmHandle) (*AuthBundle, error) {
	t.Helper()

	if helmHandle == nil {
		return nil, fmt.Errorf("bootstrapHelmSharkoAuth: helmHandle is nil")
	}
	if helmHandle.Kubeconfig == "" {
		return nil, fmt.Errorf("bootstrapHelmSharkoAuth: helmHandle.Kubeconfig is empty")
	}
	if helmHandle.Namespace == "" {
		return nil, fmt.Errorf("bootstrapHelmSharkoAuth: helmHandle.Namespace is empty")
	}
	if helmHandle.Service == "" {
		return nil, fmt.Errorf("bootstrapHelmSharkoAuth: helmHandle.Service is empty")
	}

	// 1. Read sharko-initial-admin-secret via the K8s API.
	user, pass, err := readBootstrapAdminSecret(t, helmHandle.Kubeconfig, helmHandle.Namespace)
	if err != nil {
		return nil, fmt.Errorf("bootstrapHelmSharkoAuth: read bootstrap admin secret: %w", err)
	}
	t.Logf("harness: bootstrap admin secret read [user=%s, password-len=%d]", user, len(pass))

	// 2. Spawn kubectl port-forward and capture the random local port.
	pfCtx, pfCancel := context.WithCancel(context.Background())
	pf, err := startSharkoPortForward(pfCtx, t, helmHandle)
	if err != nil {
		pfCancel()
		return nil, fmt.Errorf("bootstrapHelmSharkoAuth: start port-forward: %w", err)
	}
	// Kill on test cleanup — LIFO ordering puts this AFTER Story 13.1's
	// helm uninstall hook, which is fine (uninstall closes svc endpoints
	// independently). Cleanup MUST NOT t.Errorf — log via t.Logf only.
	t.Cleanup(func() {
		pfCancel()
		// Wait for the subprocess to reap so /proc no longer holds it.
		// "process already finished" / "signal: killed" are expected; we
		// log them at info level rather than treating them as errors.
		if err := pf.cmd.Wait(); err != nil {
			t.Logf("harness: port-forward subprocess exited: %v (expected on Kill)", err)
		}
		// Drain wait — both log-relay goroutines exit when their pipes
		// close at subprocess termination.
		pf.drainWG.Wait()
	})

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", pf.localPort)
	t.Logf("harness: sharko port-forward up at %s (svc/%s -n %s)",
		baseURL, helmHandle.Service, helmHandle.Namespace)

	// 3. Poll /api/v1/health until 200 — proves both port-forward and
	// in-cluster pod are responsive.
	if err := waitForSharkoHealth(t, baseURL, defaultPortForwardReadyTimeout); err != nil {
		return nil, fmt.Errorf("bootstrapHelmSharkoAuth: wait for health: %w", err)
	}

	// 4. POST /api/v1/auth/login → JWT.
	token, err := loginToHelmSharko(baseURL, user, pass)
	if err != nil {
		return nil, fmt.Errorf("bootstrapHelmSharkoAuth: login: %w", err)
	}
	t.Logf("harness: bootstrap admin login OK [token-len=%d]", len(token))

	return &AuthBundle{
		BaseURL:   baseURL,
		Token:     token,
		AdminUser: user,
		AdminPass: pass,
	}, nil
}

// ---------------------------------------------------------------------------
// internal helpers — Secret read
// ---------------------------------------------------------------------------

// readBootstrapAdminSecret reads sharko-initial-admin-secret from <namespace>
// via the K8s API and returns (username, password). The Secret data shape
// (V124-6.3 + V124-7.1; see cmd/sharko/reset_admin.go::writeInitialAdminSecretCLI)
// is:
//
//	data:
//	  username: "admin"   (always)
//	  password: <plaintext-bytes>
//
// data.* values are raw bytes when read via client-go (no base64 dance — the
// Go client decodes Secret.Data for you). This is the load-bearing reason to
// use client-go over `kubectl get -o jsonpath` here.
//
// Polls for up to defaultBootstrapSecretWait — the auth-store writes the
// Secret during boot and helm --wait gates pod-Ready, but the Secret write
// can land a few hundred ms behind the readiness probe in practice. Single-
// shot reads catch ~95% of cases; polling makes the harness robust against
// the race rather than blaming a "missing Secret" flake.
func readBootstrapAdminSecret(t *testing.T, kubeconfigPath, namespace string) (string, string, error) {
	t.Helper()
	cs, err := buildHelmK8sClient(kubeconfigPath)
	if err != nil {
		return "", "", fmt.Errorf("build k8s client (kubeconfig=%s): %w", kubeconfigPath, err)
	}

	deadline := time.Now().Add(defaultBootstrapSecretWait)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		secret, err := cs.CoreV1().Secrets(namespace).Get(ctx, initialAdminSecretName, metav1.GetOptions{})
		cancel()
		if err == nil {
			return decodeBootstrapSecret(secret)
		}
		if !errors.IsNotFound(err) {
			// Hard error (auth, network, RBAC) — don't keep polling.
			return "", "", fmt.Errorf("get secret %s/%s: %w", namespace, initialAdminSecretName, err)
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return "", "", fmt.Errorf("secret %s/%s not found within %s: %w",
		namespace, initialAdminSecretName, defaultBootstrapSecretWait, lastErr)
}

// decodeBootstrapSecret extracts (username, password) from a Secret read via
// the K8s API. Split out from readBootstrapAdminSecret so it's unit-testable
// without a live cluster — the K8s API/Get call is the only part that needs
// real infra.
//
// Returns an error if either key is missing OR empty (bytes len == 0). The
// secret could be present in name but blank-data if a buggy installer raced
// the auth-store write — failing fast here gives a much clearer diagnostic
// than a downstream "POST /auth/login: 401" would.
func decodeBootstrapSecret(secret *corev1.Secret) (string, string, error) {
	if secret == nil {
		return "", "", fmt.Errorf("secret is nil")
	}
	if secret.Data == nil {
		return "", "", fmt.Errorf("secret %s/%s has nil .Data", secret.Namespace, secret.Name)
	}
	userBytes, hasUser := secret.Data["username"]
	if !hasUser {
		return "", "", fmt.Errorf("secret %s/%s missing data.username key", secret.Namespace, secret.Name)
	}
	passBytes, hasPass := secret.Data["password"]
	if !hasPass {
		return "", "", fmt.Errorf("secret %s/%s missing data.password key", secret.Namespace, secret.Name)
	}
	user := strings.TrimSpace(string(userBytes))
	pass := string(passBytes) // do NOT TrimSpace the password — leading/trailing whitespace can be intentional
	if user == "" {
		return "", "", fmt.Errorf("secret %s/%s data.username is empty", secret.Namespace, secret.Name)
	}
	if len(passBytes) == 0 {
		return "", "", fmt.Errorf("secret %s/%s data.password is empty", secret.Namespace, secret.Name)
	}
	return user, pass, nil
}

// buildHelmK8sClient constructs a kubernetes.Interface from a kubeconfig
// file path. Uses clientcmd.BuildConfigFromFlags (in-cluster path is
// inappropriate here — the harness runs OUTSIDE any K8s cluster, against a
// kind cluster identified by its kubeconfig).
func buildHelmK8sClient(kubeconfigPath string) (kubernetes.Interface, error) {
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return nil, fmt.Errorf("kubeconfig %s not found: %w", kubeconfigPath, err)
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("build rest.Config from %s: %w", kubeconfigPath, err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes.NewForConfig: %w", err)
	}
	return cs, nil
}

// ---------------------------------------------------------------------------
// internal helpers — port-forward subprocess
// ---------------------------------------------------------------------------

// helmPortForward bundles the live state of one `kubectl port-forward`
// subprocess. The struct is internal — callers receive only an *AuthBundle
// and the subprocess lifecycle is t.Cleanup-managed.
type helmPortForward struct {
	cmd       *exec.Cmd
	localPort int
	drainWG   *sync.WaitGroup
}

// startSharkoPortForward spawns `kubectl port-forward svc/<svc> -n <ns>
// :<svcPort>` and returns once the subprocess has printed its
// "Forwarding from 127.0.0.1:NNNN -> ..." line (proves the listener is up).
// The local port is parsed out of the stdout line; we use the random-local
// form ":<svcPort>" rather than picking a port ourselves to avoid the
// bind-then-close race window with another test running in parallel.
//
// On caller cancel (via the supplied ctx) the subprocess is killed by the
// stdlib's exec.CommandContext machinery; our t.Cleanup hook in
// bootstrapHelmSharkoAuth additionally calls cmd.Wait to reap it.
//
// stderr is piped + line-relayed via a goroutine so failures (e.g. "error
// upgrading connection" mid-test) appear in the test log as
// "[sharko-pf-err] ..." lines without polluting the test process's stderr.
//
// If the subprocess exits BEFORE printing the Forwarding line (e.g. RBAC
// denied, svc not found, port already in use on the bind side), the function
// surfaces the captured stderr in the returned error so the failure is
// debuggable from CI logs without a re-run.
func startSharkoPortForward(ctx context.Context, t *testing.T, h *HelmHandle) (*helmPortForward, error) {
	t.Helper()

	// kubectl bin honours E2E_KUBECTL_BIN — same resolution path as the
	// rest of the harness uses (kind.go::defaultKubectlBinFromEnv).
	kubectlBin := defaultKubectlBinFromEnv()

	args := []string{
		"--kubeconfig", h.Kubeconfig,
		"port-forward",
		"--namespace", h.Namespace,
		"svc/" + h.Service,
		// ":<svcPort>" — kubectl picks a random local port, prints
		// "Forwarding from 127.0.0.1:NNNN -> <svcPort>" on stdout. We
		// parse NNNN out and use it for /api/v1/health + login.
		fmt.Sprintf(":%d", defaultHelmServicePort),
	}
	cmd := exec.CommandContext(ctx, kubectlBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("kubectl port-forward stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("kubectl port-forward stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start kubectl port-forward: %w", err)
	}

	// Capture stderr separately so we can include it in an early-exit
	// error message. The drain goroutine starts AFTER the readiness wait
	// completes — if the subprocess exits before printing the Forwarding
	// line, we want the stderr in the error string, not interleaved into
	// the test log.
	var stderrBuf bytes.Buffer
	stderrTee := io.TeeReader(stderr, &stderrBuf)

	// Read stdout line-by-line until we see "Forwarding from 127.0.0.1:NNNN"
	// or the timeout fires.
	type readResult struct {
		port int
		line string
		err  error
	}
	resCh := make(chan readResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if port, ok := parseForwardingPort(line); ok {
				resCh <- readResult{port: port, line: line}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			resCh <- readResult{err: fmt.Errorf("read stdout: %w", err)}
			return
		}
		// stdout closed without a Forwarding line — subprocess exited.
		resCh <- readResult{err: fmt.Errorf("stdout closed before Forwarding line")}
	}()

	var localPort int
	select {
	case r := <-resCh:
		if r.err != nil {
			// Best-effort drain of stderr we've buffered so far (the
			// goroutine above exited; any unread bytes still in the
			// pipe will appear if we read until close).
			_, _ = io.Copy(&stderrBuf, stderrTee)
			_ = cmd.Wait()
			return nil, fmt.Errorf("%w (stderr: %s)", r.err, strings.TrimSpace(stderrBuf.String()))
		}
		localPort = r.port
	case <-time.After(defaultPortForwardReadyTimeout):
		// Subprocess still alive but never printed Forwarding line —
		// kill it and report timeout.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("port-forward never printed Forwarding line within %s (stderr: %s)",
			defaultPortForwardReadyTimeout, strings.TrimSpace(stderrBuf.String()))
	}

	// Now that the listener is up, start log-relay goroutines on both
	// pipes. They exit cleanly when the subprocess closes stdio at kill
	// time.
	wg := &sync.WaitGroup{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		drainHelmPFLog(t, "sharko-pf-out", stdout)
	}()
	go func() {
		defer wg.Done()
		drainHelmPFLog(t, "sharko-pf-err", stderrTee)
	}()

	return &helmPortForward{
		cmd:       cmd,
		localPort: localPort,
		drainWG:   wg,
	}, nil
}

// parseForwardingPort matches kubectl's "Forwarding from 127.0.0.1:NNNN ->
// <svcPort>" line and returns NNNN. Returns (0, false) when the line is not
// a Forwarding announcement (kubectl also emits informational lines about
// resource lookups before the listener is up).
//
// The IPv6 form ("Forwarding from [::1]:NNNN -> ...") is also matched
// because kubectl >= 1.30 binds both stacks by default; the test client
// only needs ONE port number — the loopback addresses bind to the same
// listener so IPv4 dial works regardless.
//
// Split out as a free function so it's unit-testable without a live kubectl.
func parseForwardingPort(line string) (int, bool) {
	// "Forwarding from 127.0.0.1:38543 -> 80"
	// "Forwarding from [::1]:38543 -> 80"
	re := regexp.MustCompile(`Forwarding from (?:127\.0\.0\.1|\[::1\]):(\d+) ->`)
	m := re.FindStringSubmatch(line)
	if len(m) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 || n > 65535 {
		return 0, false
	}
	return n, true
}

// drainHelmPFLog tails a port-forward stdio pipe line-by-line, mirroring
// each line into t.Logf with a prefix. Stops cleanly when the pipe closes
// (process exit) so it does not leak goroutines past the test.
//
// Mirrors drainArgoLog in tests/e2e/lifecycle/cluster_helpers.go but inside
// the harness package so Story 13.3's SharkoModeHelm wrapper does not need
// to import the lifecycle package.
func drainHelmPFLog(t *testing.T, tag string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		t.Logf("[%s] %s", tag, scanner.Text())
	}
}

// ---------------------------------------------------------------------------
// internal helpers — health probe + login
// ---------------------------------------------------------------------------

// waitForSharkoHealth polls GET <baseURL>/api/v1/health until it returns 200
// or the supplied timeout elapses. Sharko's /api/v1/health endpoint is
// unauthenticated (router.go:1133 explicitly bypasses the bearer-token
// middleware for it), so no Authorization header is needed.
//
// Polls every 200ms — the in-cluster Sharko pod is already Ready (helm --wait
// gated that), so the only delay is the port-forward TCP listener accepting
// + the first request reaching the pod. On a warm install this typically
// resolves in under a second.
func waitForSharkoHealth(t *testing.T, baseURL string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	httpClient := &http.Client{Timeout: 3 * time.Second}
	url := strings.TrimRight(baseURL, "/") + "/api/v1/health"

	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status=%d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("GET %s never returned 200 within %s: %v", url, timeout, lastErr)
}

// loginToHelmSharko POSTs JSON {username, password} to /api/v1/auth/login
// and returns the JWT from the response body. Mirrors apiclient.go's
// Client.Login but is free-standing (no *Client / *Sharko receiver yet —
// AuthBundle is what the typed Client gets built FROM in Story 13.3).
//
// On non-200 the response body (truncated to 256 bytes) is included in the
// error so credential mismatch / rate-limit / server-error are all
// distinguishable from the returned error string.
func loginToHelmSharko(baseURL, user, pass string) (string, error) {
	body, err := json.Marshal(map[string]string{
		"username": user,
		"password": pass,
	})
	if err != nil {
		return "", fmt.Errorf("marshal login body: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/api/v1/auth/login"
	ctx, cancel := context.WithTimeout(context.Background(), defaultLoginTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: defaultLoginTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("POST %s: status=%d body=%s", url, resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode login response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("login response missing or empty .token field")
	}
	return out.Token, nil
}

//go:build e2e

// Package lifecycle holds the V2 Epic 7-1.4+ lifecycle subtests.
//
// Each domain (cluster, addon, catalog, RBAC, ...) owns its own _test.go
// file plus a sibling _helpers.go for subtest-shared utilities. The
// helpers files are deliberately local to the package — concurrent
// agents implementing other lifecycle stories must not depend on them
// (they should grow their own _helpers.go alongside their _test.go).
//
// The cluster lifecycle file (cluster_test.go) is V2 Epic 7-1.4 — it
// covers the 21 cluster + cluster-discovery + cluster-orphan endpoints
// against an in-process sharko backed by a real ArgoCD installed in a
// kind management cluster. Subtests skip-graceful when the kubeconfig
// path lacks a credentials provider (EKS-only handlers).
package lifecycle

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// argocdAccess captures the host-reachable URL + admin bearer token for
// an ArgoCD installation living inside a kind management cluster.
//
// Lifecycle:
//   - StartArgoCDPortForward spawns `kubectl port-forward` on a free
//     local port, waits for the listener to come up, and registers a
//     t.Cleanup that kills the process group.
//   - FetchAdminPassword reads the bootstrap secret created by
//     argocd-installer (argocd-initial-admin-secret).
//   - Login posts /api/v1/session against the port-forwarded URL with
//     the admin password and returns a JWT bearer token. The caller
//     stores it on the active sharko Connection.Argocd.Token so every
//     downstream argocd-touching handler authenticates correctly.
//
// The ArgoCD admin user has full-cluster permissions, which is exactly
// what sharko's per-cluster register/list/diff calls require. Using the
// admin token sidesteps the apiKey-capability dance the production
// service-account flow needs (see scripts/sharko-dev.sh for that
// version).
type argocdAccess struct {
	URL      string // e.g. "https://127.0.0.1:38543" — TLS, self-signed
	Token    string // JWT bearer token from /api/v1/session
	pfCancel context.CancelFunc
}

// startArgoCDAccess wires up host-reachable access to an ArgoCD that has
// already been installed into mgmt via harness.InstallArgoCD. Steps:
//
//  1. start a `kubectl port-forward -n argocd svc/argocd-server :443`
//     on a free local TCP port; wait until the local listener accepts
//     a TCP connection (proves port-forward is up).
//  2. read the argocd-initial-admin-secret to get the admin password.
//  3. POST /api/v1/session with admin/password and capture the token
//     from the JSON response.
//
// Calls t.Fatalf on any step failure and registers t.Cleanup to kill
// the port-forward.
func startArgoCDAccess(t *testing.T, mgmt harness.KindCluster) *argocdAccess {
	t.Helper()

	// 1. allocate a free local TCP port. We let port-forward bind to a
	//    specific port (vs `:0`) so the URL is deterministic before the
	//    process is up.
	localPort, err := pickFreePort()
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}

	pfCtx, pfCancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(pfCtx, "kubectl",
		"--kubeconfig", mgmt.Kubeconfig,
		"port-forward", "-n", "argocd",
		"svc/argocd-server",
		fmt.Sprintf("%d:443", localPort),
	)
	// Stream port-forward output through a pipe so we can spot
	// "error" lines in the test log without polluting stderr at
	// process exit time.
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		pfCancel()
		t.Fatalf("kubectl port-forward start: %v", err)
	}
	t.Cleanup(func() {
		pfCancel()
		_ = cmd.Wait()
	})
	go drainArgoLog(t, "argocd-pf-out", stdout)
	go drainArgoLog(t, "argocd-pf-err", stderr)

	// 2. wait for the listener to accept TCP — port-forward typically
	//    comes up in <1s but we give it 30s (image pull on first run
	//    can drag CI nodes).
	addr := fmt.Sprintf("127.0.0.1:%d", localPort)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if time.Now().After(deadline) {
		t.Fatalf("argocd port-forward never accepted TCP on %s", addr)
	}

	urlStr := fmt.Sprintf("https://%s", addr)
	t.Logf("harness/lifecycle: argocd port-forward up at %s", urlStr)

	// 3. read admin password from argocd-initial-admin-secret.
	pwd := fetchArgoAdminPassword(t, mgmt)
	t.Logf("harness/lifecycle: argocd admin password obtained (len=%d)", len(pwd))

	// 4. login and get a session token.
	token := argoLogin(t, urlStr, "admin", pwd)
	t.Logf("harness/lifecycle: argocd admin session token obtained (len=%d)", len(token))

	return &argocdAccess{
		URL:      urlStr,
		Token:    token,
		pfCancel: pfCancel,
	}
}

// pickFreePort asks the kernel for a free local TCP port by binding to
// :0 and immediately closing the listener. Race window is tiny but
// non-zero; acceptable for a single-shot test setup.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// drainArgoLog tails port-forward stdout/stderr line-by-line, mirroring
// each line into t.Log with a prefix. Stops cleanly when the pipe
// closes (process exit) so it does not leak goroutines.
func drainArgoLog(t *testing.T, tag string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		t.Logf("[%s] %s", tag, scanner.Text())
	}
}

// fetchArgoAdminPassword runs `kubectl get secret argocd-initial-admin-secret`
// against mgmt and returns the decoded admin password. The secret is
// created by ArgoCD's installer and persists for the lifetime of the
// install (it is NOT rotated on argocd-server restart by default).
//
// Retries for up to 90s because the initial-admin-secret is created by
// argocd-server itself on first start, AFTER `kubectl wait
// deployment/argocd-server --for=available` returns. Polling masks the
// race rather than depending on a single command happening to win it.
func fetchArgoAdminPassword(t *testing.T, mgmt harness.KindCluster) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	var lastOut string
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, err := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", mgmt.Kubeconfig,
			"-n", "argocd",
			"get", "secret", "argocd-initial-admin-secret",
			"-o", "jsonpath={.data.password}",
		).CombinedOutput()
		cancel()
		if err == nil {
			b64 := strings.TrimSpace(string(out))
			if b64 != "" {
				dec, decErr := base64.StdEncoding.DecodeString(b64)
				if decErr == nil {
					pwd := strings.TrimSpace(string(dec))
					if pwd != "" {
						return pwd
					}
				}
				lastErr = decErr
			} else {
				lastErr = fmt.Errorf("secret data.password empty")
			}
		} else {
			lastErr = err
			lastOut = string(out)
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("argocd-initial-admin-secret never resolved within 90s: %v\nlast output: %s", lastErr, lastOut)
	return "" // unreachable
}

// argoLogin POSTs /api/v1/session with username/password and returns
// the JWT token from the JSON response. TLS verification is disabled
// (kind-installed argocd uses a self-signed cert).
func argoLogin(t *testing.T, baseURL, user, pwd string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"username": user,
		"password": pwd,
	})
	httpClient := &http.Client{
		Timeout: 15 * time.Second,
		Transport: insecureTransport(),
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/session", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("argoLogin: build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("argoLogin: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("argoLogin: status=%d body=%s", resp.StatusCode, raw)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("argoLogin: decode: %v", err)
	}
	if out.Token == "" {
		t.Fatalf("argoLogin: empty token in response: %s", raw)
	}
	return out.Token
}

// insecureTransport returns an http.Transport with TLS verification
// disabled. The kind-installed argocd serves a self-signed cert that no
// trust store on the host validates by default — every existing sharko
// argocd client also runs with insecure=true (see
// internal/argocd/client.go NewClient call sites), so we mirror it.
func insecureTransport() *http.Transport {
	return &http.Transport{
		//nolint:gosec // intentional: kind-installed argocd uses self-signed certs
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
}

// ---------------------------------------------------------------------------
// connection seeding
// ---------------------------------------------------------------------------

// seedActiveConnection POSTs a connection to the running sharko via the
// public API and marks it active. Uses the bootstrap admin client, so
// the caller does not need an explicit auth dance.
//
// The Git side is irrelevant for read-time GetActiveConnection() because
// SharkoConfig.GitProvider already installed an in-memory MockGitProvider
// override on the connection service — but the connection still needs a
// valid Git config to pass create-time validation. We supply the
// gitfake's repo URL.
func seedActiveConnection(t *testing.T, admin *harness.Client, gitfakeRepoURL, argoURL, argoToken string) {
	t.Helper()

	// Validate the URL parses — surfaces config typos earlier than
	// sharko's create handler would.
	if _, err := url.Parse(argoURL); err != nil {
		t.Fatalf("seedActiveConnection: invalid argoURL %q: %v", argoURL, err)
	}

	body := map[string]any{
		"name": "e2e-cluster-lifecycle",
		"git": models.GitRepoConfig{
			Provider: models.GitProviderGitHub,
			Owner:    "sharko-e2e",
			Repo:     "sharko-addons",
			RepoURL:  gitfakeRepoURL,
			Token:    "ghmock-test-token", // unused (gitprovider override is wired)
		},
		"argocd": models.ArgocdConfig{
			ServerURL: argoURL,
			Token:     argoToken,
			Namespace: "argocd",
			Insecure:  true,
		},
		"set_as_default": true,
	}

	// Use the lower-level Do helper so we can accept either 200 or 201
	// (the server returns 201 on create today, but the contract is "any
	// 2xx").
	resp := admin.Do(t, http.MethodPost, "/api/v1/connections/", body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("seedActiveConnection: create status=%d body=%s", resp.StatusCode, raw)
	}

	// Activate the connection (POST /connections/active).
	resp2 := admin.Do(t, http.MethodPost, "/api/v1/connections/active", map[string]string{
		"connection_name": "e2e-cluster-lifecycle",
	})
	defer resp2.Body.Close()
	raw2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode < 200 || resp2.StatusCode >= 300 {
		t.Fatalf("seedActiveConnection: activate status=%d body=%s", resp2.StatusCode, raw2)
	}

	t.Logf("harness/lifecycle: seeded active connection e2e-cluster-lifecycle [argo=%s]", argoURL)
}

// ---------------------------------------------------------------------------
// kubeconfig fixture for kubeconfig-provider register
// ---------------------------------------------------------------------------

// makeRegisterRequest assembles a RegisterClusterRequest using the
// inline-kubeconfig provider against the supplied target kind cluster.
// Creates the SA + ClusterRoleBinding + bearer-token via
// harness.CreateServiceAccountToken and assembles the kubeconfig with
// the Docker-network-internal IP via harness.BuildKubeconfig.
//
// Bypasses the EKS credentials-provider path entirely — sharko v1.25.x's
// kubeconfig provider accepts the inline kubeconfig YAML in the request
// body, satisfies the cluster register without an external secret store,
// and registers the cluster directly in ArgoCD.
func makeKubeconfigRegisterBody(t *testing.T, target harness.KindCluster, name string) map[string]any {
	t.Helper()
	saName := "sharko-e2e-sa"
	_ = harness.CreateServiceAccountToken(t, target, saName)
	kubeconfig := harness.BuildKubeconfig(t, target, saName)
	return map[string]any{
		"name":       name,
		"provider":   "kubeconfig",
		"kubeconfig": kubeconfig,
		"addons":     map[string]bool{}, // no addons for the register path test
	}
}

// fileExists is a tiny helper for the prereq-skip guards in
// cluster_test.go. exec.LookPath returns an error AND nil binary when
// the lookup fails; we want a single-bool answer.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

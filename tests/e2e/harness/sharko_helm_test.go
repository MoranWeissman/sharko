//go:build e2e

package harness

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// errProbeBroken is the sentinel returned by the probe-failure subtest of
// TestResolveImageTagEnvVarSet. Declared at package scope so the stub
// closure stays a one-liner.
var errProbeBroken = errors.New("synthetic docker probe failure")

// TestHelmInstallConfigResolved exercises HelmInstallConfig.resolved() —
// the pure-defaulting layer underneath installSharkoHelm. We test this in
// isolation because the docker/kind/helm shell-outs are stateful and
// non-trivial to mock; the pure layer is the only thing safe to unit-test
// without a real cluster. The integration coverage lands in Story 13.4
// (Wave D) where a real helm install is exercised end-to-end.
func TestHelmInstallConfigResolved(t *testing.T) {
	// Parent must NOT call t.Parallel — t.Setenv in subtests is
	// incompatible with a parallel parent (Go panics at runtime).
	t.Run("zero-value defaults", func(t *testing.T) {
		got := HelmInstallConfig{}.resolved()
		if got.Namespace != defaultHelmNamespace {
			t.Errorf("Namespace: got %q, want %q", got.Namespace, defaultHelmNamespace)
		}
		if got.Timeout != defaultRolloutWait {
			t.Errorf("Timeout: got %s, want %s", got.Timeout, defaultRolloutWait)
		}
		if got.HelmBin != defaultHelmBin {
			t.Errorf("HelmBin: got %q, want %q", got.HelmBin, defaultHelmBin)
		}
		if got.DockerBin != defaultDockerBin {
			t.Errorf("DockerBin: got %q, want %q", got.DockerBin, defaultDockerBin)
		}
	})

	t.Run("explicit values preserved", func(t *testing.T) {
		in := HelmInstallConfig{
			Namespace: "custom-ns",
			Timeout:   42 * time.Second,
			HelmBin:   "/opt/helm/bin/helm",
			DockerBin: "/opt/docker/bin/docker",
		}
		got := in.resolved()
		if got.Namespace != "custom-ns" {
			t.Errorf("Namespace: got %q, want %q", got.Namespace, "custom-ns")
		}
		if got.Timeout != 42*time.Second {
			t.Errorf("Timeout: got %s, want %s", got.Timeout, 42*time.Second)
		}
		if got.HelmBin != "/opt/helm/bin/helm" {
			t.Errorf("HelmBin: got %q, want %q", got.HelmBin, "/opt/helm/bin/helm")
		}
		if got.DockerBin != "/opt/docker/bin/docker" {
			t.Errorf("DockerBin: got %q, want %q", got.DockerBin, "/opt/docker/bin/docker")
		}
	})

	t.Run("env var overrides take effect when fields empty", func(t *testing.T) {
		t.Setenv("E2E_HELM_BIN", "/from/env/helm")
		t.Setenv("E2E_DOCKER_BIN", "/from/env/docker")
		got := HelmInstallConfig{}.resolved()
		if got.HelmBin != "/from/env/helm" {
			t.Errorf("HelmBin: got %q, want %q", got.HelmBin, "/from/env/helm")
		}
		if got.DockerBin != "/from/env/docker" {
			t.Errorf("DockerBin: got %q, want %q", got.DockerBin, "/from/env/docker")
		}
	})

	t.Run("explicit values win over env", func(t *testing.T) {
		t.Setenv("E2E_HELM_BIN", "/from/env/helm")
		got := HelmInstallConfig{HelmBin: "/explicit/helm"}.resolved()
		if got.HelmBin != "/explicit/helm" {
			t.Errorf("HelmBin: got %q, want explicit value %q", got.HelmBin, "/explicit/helm")
		}
	})

	t.Run("zero / negative timeout falls back to default", func(t *testing.T) {
		got := HelmInstallConfig{Timeout: -5 * time.Second}.resolved()
		if got.Timeout != defaultRolloutWait {
			t.Errorf("Timeout: got %s, want default %s", got.Timeout, defaultRolloutWait)
		}
	})
}

// TestHumanDuration covers the kubectl --timeout flag formatter. kubectl
// rejects fractional seconds + unrecognised units; the helper guarantees a
// whole-second string.
func TestHumanDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"3m", 3 * time.Minute, "180s"},
		{"180s", 180 * time.Second, "180s"},
		{"sub-second rounds up to 1s", 500 * time.Millisecond, "1s"},
		{"zero falls back to default", 0, "180s"},
		{"negative falls back to default", -1 * time.Second, "180s"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := humanDuration(tc.in); got != tc.want {
				t.Errorf("humanDuration(%s) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestHelmTimeoutString verifies the helm --timeout formatter uses the
// stdlib Go duration string (helm accepts "180s", "3m", "1h30m" verbatim).
func TestHelmTimeoutString(t *testing.T) {
	t.Parallel()
	if got := helmTimeoutString(3 * time.Minute); got != "3m0s" {
		t.Errorf("helmTimeoutString(3m) = %q, want %q", got, "3m0s")
	}
	if got := helmTimeoutString(0); got != defaultRolloutWait.String() {
		t.Errorf("helmTimeoutString(0) = %q, want default %q", got, defaultRolloutWait.String())
	}
}

// TestRandHex8 sanity-checks the image-tag suffix generator. Length must
// be 8 (4 random bytes -> 8 hex chars) and consecutive calls must differ.
func TestRandHex8(t *testing.T) {
	t.Parallel()
	a, err := randHex8()
	if err != nil {
		t.Fatalf("randHex8 #1: %v", err)
	}
	if len(a) != 8 {
		t.Fatalf("randHex8 length: got %d, want 8", len(a))
	}
	b, err := randHex8()
	if err != nil {
		t.Fatalf("randHex8 #2: %v", err)
	}
	if a == b {
		t.Fatalf("randHex8 returned identical values twice (%q) — entropy bug", a)
	}
}

// TestWorktreeRootFromCallerFile confirms the runtime.Caller-based root
// resolver finds the worktree containing this test file (must hold both
// charts/sharko/Chart.yaml and a Dockerfile). Load-bearing for parallel
// worktree development — the harness must build from the worktree, not
// the maintainer's main checkout.
func TestWorktreeRootFromCallerFile(t *testing.T) {
	t.Parallel()
	root, err := worktreeRootFromCallerFile()
	if err != nil {
		t.Fatalf("worktreeRootFromCallerFile: %v", err)
	}
	for _, marker := range []string{
		filepath.Join("Dockerfile"),
		filepath.Join("charts", "sharko", "Chart.yaml"),
	} {
		if !statOk(filepath.Join(root, marker)) {
			t.Errorf("worktree root %s missing expected marker %s", root, marker)
		}
	}
	// The resolved root must contain "sharko" somewhere in its path —
	// either the main checkout dir or a worktree dir. This is a weak
	// assertion but catches the failure mode of resolving to "/" or
	// "/Users".
	if !strings.Contains(root, "sharko") {
		t.Errorf("worktree root %q does not contain 'sharko' — likely resolved too far up", root)
	}
}

// TestResolveImageTagFreshGeneration covers the no-env-var path of
// resolveImageTag — must return a fresh "e2e-<8hex>" tag with
// skipBuild=false. The local-Docker probe seam is irrelevant on this
// path (we never look at the env var), so no stub is installed.
func TestResolveImageTagFreshGeneration(t *testing.T) {
	// Note: NOT t.Parallel because we depend on env var state.
	t.Setenv("SHARKO_E2E_IMAGE_TAG", "")
	tag, skipBuild, err := resolveImageTag(t, HelmInstallConfig{}.resolved())
	if err != nil {
		t.Fatalf("resolveImageTag: %v", err)
	}
	if skipBuild {
		t.Errorf("skipBuild: got true, want false on env-unset path")
	}
	if !strings.HasPrefix(tag, "e2e-") {
		t.Errorf("tag prefix: got %q, want e2e- prefix", tag)
	}
	if len(tag) != len("e2e-")+8 {
		t.Errorf("tag length: got %d (%q), want %d", len(tag), tag, len("e2e-")+8)
	}
}

// TestResolveImageTagEnvVarSet covers the SHARKO_E2E_IMAGE_TAG path of
// resolveImageTag by stubbing the local-Docker probe seam. The two
// branches exercised here are the heart of the V125-1-13.1 hotfix —
// previously the probe checked the wrong cluster (kind containerd via
// crictl, which doesn't filter by ref) and the cache always reported a
// hit, producing ImagePullBackOff. The fix routes the probe to the
// local Docker host (which is what the env var actually certifies)
// and only skips the docker build — never the kind load.
func TestResolveImageTagEnvVarSet(t *testing.T) {
	// Restore the production probe after the suite finishes; subtests
	// that need a different stub install their own per-subtest stub.
	prev := imageExistsLocallyFn
	t.Cleanup(func() { imageExistsLocallyFn = prev })

	t.Run("env set + image present in local docker -> skipBuild=true, tag preserved", func(t *testing.T) {
		t.Setenv("SHARKO_E2E_IMAGE_TAG", "debug")
		var gotRef string
		imageExistsLocallyFn = func(_ /*dockerBin*/, ref string) (bool, error) {
			gotRef = ref
			return true, nil
		}
		tag, skipBuild, err := resolveImageTag(t, HelmInstallConfig{}.resolved())
		if err != nil {
			t.Fatalf("resolveImageTag: %v", err)
		}
		if tag != "debug" {
			t.Errorf("tag: got %q, want %q (env var must be preserved verbatim)", tag, "debug")
		}
		if !skipBuild {
			t.Errorf("skipBuild: got false, want true on cache hit")
		}
		if gotRef != "sharko:debug" {
			t.Errorf("probe ref: got %q, want %q (must probe sharko:<env-tag>)", gotRef, "sharko:debug")
		}
	})

	t.Run("env set + image absent from local docker -> skipBuild=false, tag preserved", func(t *testing.T) {
		t.Setenv("SHARKO_E2E_IMAGE_TAG", "missing-tag")
		imageExistsLocallyFn = func(_, _ string) (bool, error) { return false, nil }
		tag, skipBuild, err := resolveImageTag(t, HelmInstallConfig{}.resolved())
		if err != nil {
			t.Fatalf("resolveImageTag: %v", err)
		}
		if tag != "missing-tag" {
			t.Errorf("tag: got %q, want %q (env var preserved even on miss for stable PR builds)",
				tag, "missing-tag")
		}
		if skipBuild {
			t.Errorf("skipBuild: got true, want false on cache miss — build must run")
		}
	})

	t.Run("env set + probe error -> skipBuild=false, fall back to rebuild", func(t *testing.T) {
		t.Setenv("SHARKO_E2E_IMAGE_TAG", "probe-error-tag")
		imageExistsLocallyFn = func(_, _ string) (bool, error) {
			return false, errProbeBroken
		}
		tag, skipBuild, err := resolveImageTag(t, HelmInstallConfig{}.resolved())
		if err != nil {
			t.Fatalf("resolveImageTag should swallow probe errors (got %v) — rebuild is the safe fallback", err)
		}
		if tag != "probe-error-tag" {
			t.Errorf("tag: got %q, want %q", tag, "probe-error-tag")
		}
		if skipBuild {
			t.Errorf("skipBuild: got true, want false — probe-error path must rebuild defensively")
		}
	})
}

// TestHelmHandleZeroValueIsSafe confirms a nil-receiver uninstall is a
// no-op (the cleanup hook must not panic if installSharkoHelm returns nil
// before populating the handle).
func TestHelmHandleZeroValueIsSafe(t *testing.T) {
	t.Parallel()
	uninstallSharkoHelm(t, nil) // must not panic / fail
}

// TestRewriteHostLoopbackForPod covers the V125-1-13 hotfix helper that
// makes host-loopback URLs (gitfake, port-forwarded ArgoCD) reachable from
// inside a kind Pod by rewriting "127.0.0.1" / "localhost" / "::1" to
// "host.docker.internal". Non-loopback hosts, in-cluster DNS, non-http(s)
// schemes, and malformed inputs all pass through unchanged.
func TestRewriteHostLoopbackForPod(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "127.0.0.1 with port and path -> host.docker.internal",
			in:   "http://127.0.0.1:1234/foo",
			want: "http://host.docker.internal:1234/foo",
		},
		{
			name: "localhost https with port -> host.docker.internal",
			in:   "https://localhost:8443",
			want: "https://host.docker.internal:8443",
		},
		{
			name: "IPv6 loopback [::1] with port -> host.docker.internal",
			in:   "https://[::1]:8080/api",
			want: "https://host.docker.internal:8080/api",
		},
		{
			name: "in-cluster service DNS passes through unchanged",
			in:   "https://argocd-server.argocd.svc.cluster.local",
			want: "https://argocd-server.argocd.svc.cluster.local",
		},
		{
			name: "real DNS host passes through unchanged",
			in:   "http://github.com/foo/bar",
			want: "http://github.com/foo/bar",
		},
		{
			name: "non-loopback IP passes through unchanged",
			in:   "http://192.168.1.50:9000/path",
			want: "http://192.168.1.50:9000/path",
		},
		{
			name: "non-http scheme passes through unchanged",
			in:   "git://127.0.0.1:9418/repo.git",
			want: "git://127.0.0.1:9418/repo.git",
		},
		{
			name: "query + fragment preserved through rewrite",
			in:   "http://127.0.0.1:5000/x?y=1&z=2#frag",
			want: "http://host.docker.internal:5000/x?y=1&z=2#frag",
		},
		{
			name: "malformed URL returns input unchanged",
			in:   "http://[not-an-ip:bad",
			want: "http://[not-an-ip:bad",
		},
		{
			name: "empty string returns empty string",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RewriteHostLoopbackForPod(tc.in)
			if got != tc.want {
				t.Errorf("RewriteHostLoopbackForPod(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

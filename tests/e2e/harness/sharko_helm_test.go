//go:build e2e

package harness

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
// resolveImageTag — must return a fresh "e2e-<8hex>" tag with cached=false.
// We do NOT cover the cache-hit path here because it requires a live kind
// cluster + docker; that path is covered indirectly by Story 13.4's e2e
// test which sets SHARKO_E2E_IMAGE_TAG and verifies a re-run skips the
// rebuild.
func TestResolveImageTagFreshGeneration(t *testing.T) {
	// Note: NOT t.Parallel because we depend on os.Unsetenv.
	t.Setenv("SHARKO_E2E_IMAGE_TAG", "")
	tag, cached, err := resolveImageTag(t, "any-cluster-name", HelmInstallConfig{}.resolved())
	if err != nil {
		t.Fatalf("resolveImageTag: %v", err)
	}
	if cached {
		t.Errorf("cached: got true, want false on env-unset path")
	}
	if !strings.HasPrefix(tag, "e2e-") {
		t.Errorf("tag prefix: got %q, want e2e- prefix", tag)
	}
	if len(tag) != len("e2e-")+8 {
		t.Errorf("tag length: got %d (%q), want %d", len(tag), tag, len("e2e-")+8)
	}
}

// TestHelmHandleZeroValueIsSafe confirms a nil-receiver uninstall is a
// no-op (the cleanup hook must not panic if installSharkoHelm returns nil
// before populating the handle).
func TestHelmHandleZeroValueIsSafe(t *testing.T) {
	t.Parallel()
	uninstallSharkoHelm(t, nil) // must not panic / fail
}

//go:build e2e

package harness

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// CheckSharkoPodTUFCacheLogs verifies that the Sharko pod successfully loaded
// the Sigstore trust root. This pins the fix for v4.0.3 where a read-only root
// filesystem without a writable /tmp mount caused "mkdir /tmp/sigstore-tuf:
// read-only file system" and 0 verified signatures.
//
// Assertions:
//  1. Pod log contains "sigstore trust root loaded" (proves the TUF cache was
//     successfully created and the trust root fetched).
//  2. Pod log contains 0 "verification errored" lines (proves no TUF cache
//     failures). In E2E this check can only fail today if the TUF cache
//     creation fails, because the E2E image carries the unsigned catalogue and
//     the verifier is never called.
//  3. If expectedVerified > 0, asserts exactly that many "catalog signature
//     verified" lines appear in the log (optional — pass 0 to skip this check,
//     since the E2E image carries an unsigned catalogue).
//
// Returns detailed error with actual counts if any assertion fails.
func CheckSharkoPodTUFCacheLogs(t *testing.T, h *HelmHandle, expectedVerified int) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fetch pod logs via kubectl. Use --all-containers=true to capture logs
	// from the sharko container even if there are sidecars.
	kubectlBin := defaultKubectlBinFromEnv()
	cmd := exec.CommandContext(ctx, kubectlBin,
		"--kubeconfig", h.Kubeconfig,
		"--context", "kind-"+h.KindClusterName,
		"-n", h.Namespace,
		"logs",
		"-l", "app.kubernetes.io/name=sharko",
		"--tail=500", // enough to capture startup + catalogue loading
		"--all-containers=true",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to fetch sharko pod logs: %w\noutput: %s", err, out)
	}

	logs := string(out)

	// Check assertion 1: trust root loaded.
	if !strings.Contains(logs, "sigstore trust root loaded") {
		// Include the actual "sigstore trust root unavailable" line if present,
		// to help diagnose the root cause.
		unavailableMsg := ""
		for _, line := range strings.Split(logs, "\n") {
			if strings.Contains(line, "sigstore trust root unavailable") {
				unavailableMsg = "\nActual log line: " + line
				break
			}
		}
		return fmt.Errorf("pod log does not contain 'sigstore trust root loaded' — "+
			"the TUF cache was not successfully initialized. Common causes: "+
			"(1) read-only /tmp without a writable mount, or (2) no network access to "+
			"tuf-repo-cdn.sigstore.dev.%s", unavailableMsg)
	}

	// Check assertion 2: 0 "verification errored" lines.
	erroredCount := strings.Count(logs, "verification errored")
	if erroredCount > 0 {
		return fmt.Errorf("pod log contains %d 'verification errored' lines — "+
			"TUF cache failures are still occurring", erroredCount)
	}

	// Check assertion 3: exact count of "catalog signature verified" (optional).
	if expectedVerified > 0 {
		verifiedCount := strings.Count(logs, "catalog signature verified")
		if verifiedCount != expectedVerified {
			return fmt.Errorf("pod log contains %d 'catalog signature verified' lines, "+
				"expected %d", verifiedCount, expectedVerified)
		}
	}

	return nil
}

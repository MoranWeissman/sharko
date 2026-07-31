package orchestrator

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/config"
)

// The v4 repo layout fixes three paths. A few packages cannot import the
// package that declares them — internal/orchestrator imports internal/ai and
// internal/config, so neither of those can import back — and so they carry
// their own copy of the literal instead.
//
// A copy is only safe if something notices when it drifts. These tests are
// that something. They live here because internal/orchestrator is the one
// package that can see every copy at once.
//
// The reason this matters more than a normal duplicated constant: BOTH
// copies feed "is this a v4 repo?" checks that answer NO when they find
// nothing at the path. Drift therefore produces no error, no log line and no
// failing request — the repo just quietly starts being treated as v3, and
// every v4 answer after that is wrong. There is nothing to debug from,
// because nothing looks broken. So the drift has to be caught here, at build
// time, or it will not be caught at all.

// TestLockstep_EnginePinPath pins internal/ai's copy of the engine pin path.
// internal/ai answers questions about the connected repo for the assistant;
// if its copy fell behind, the assistant would give confident v3 answers
// about a v4 repo.
func TestLockstep_EnginePinPath(t *testing.T) {
	t.Parallel()
	if ai.V4EnginePinPath != BootstrapRootAppPath {
		t.Errorf(
			"ai.V4EnginePinPath = %q, want %q (orchestrator.BootstrapRootAppPath) — "+
				"these MUST be the same file, and a mismatch makes every v4 repo look like a v3 repo to the assistant, silently",
			ai.V4EnginePinPath, BootstrapRootAppPath)
	}
	if EnginePinPath != BootstrapRootAppPath {
		t.Errorf(
			"EnginePinPath = %q, want %q (BootstrapRootAppPath) — what bootstrap writes and what the pin-bump machinery edits must be one file",
			EnginePinPath, BootstrapRootAppPath)
	}
}

// TestLockstep_ManagedClustersPath pins internal/config's copy of the
// managed-clusters path. config.ResolveCredentialRouting falls back to it to
// find a cluster's stored credential pointer; if its copy fell behind,
// Sharko would fetch credentials by the raw cluster name against the wrong
// backend — the exact bug class that resolver exists to prevent.
func TestLockstep_ManagedClustersPath(t *testing.T) {
	t.Parallel()
	if config.V4ManagedClustersPath != V4ManagedClustersPath {
		t.Errorf(
			"config.V4ManagedClustersPath = %q, want %q (orchestrator.V4ManagedClustersPath) — "+
				"a mismatch silently loses every v4-registered cluster's stored secretPath, credsSource and roleARN",
			config.V4ManagedClustersPath, V4ManagedClustersPath)
	}
}

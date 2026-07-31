package api

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// internal/prtracker holds the canonical list of pull-request operation
// codes, and the dashboard's filter chips sort every tracked PR by matching
// that string. internal/orchestrator cannot import prtracker (see
// pr_tracker_adapter.go — the arrow points the other way), so the codes it
// stamps on a PR are hand copies of prtracker's.
//
// A copy is only safe if something notices when it drifts, and nothing
// would: a code nobody recognises does not error, it just lands the pull
// request in the dashboard's gray "other" bucket, where it looks like a
// stray. internal/api imports both packages, so this is where the two can
// be compared.
func TestLockstep_CatalogOpCodes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name             string
		orchCode, prCode string
	}{
		{"catalog add", orchestrator.OpCodeCatalogAdd, prtracker.OpCatalogAdd},
		{"catalog add and enable", orchestrator.OpCodeCatalogAddEnable, prtracker.OpCatalogAddEnable},
	} {
		if tc.orchCode != tc.prCode {
			t.Errorf(
				"%s: orchestrator says %q, prtracker says %q — these MUST be the same string, "+
					"or every catalog pull request falls into the dashboard's gray \"other\" bucket",
				tc.name, tc.orchCode, tc.prCode)
		}
	}
}

// TestLockstep_IncompleteEntryCode pins the one error code the orchestrator
// sets itself (on V4SemanticValidationError.Code) against this package's
// copy, which is the one the swagger docs describe and the UI reads.
func TestLockstep_IncompleteEntryCode(t *testing.T) {
	t.Parallel()
	if orchestrator.CodeIncompleteEntry != CodeIncompleteEntry {
		t.Errorf(
			"orchestrator.CodeIncompleteEntry = %q, api.CodeIncompleteEntry = %q — "+
				"a mismatch means the UI never recognises a half-written catalog entry and shows a dead-end error instead",
			orchestrator.CodeIncompleteEntry, CodeIncompleteEntry)
	}
}

package orchestrator

// mixed_layout_message_test.go — the exact words of the mixed-layout
// explanation, pinned character for character.
//
// MixedLayoutMessage's own doc comment says it is "a plain-English sentence
// the UI can show as-is", and it is written into MigrationStatusResult.Message
// and into the refusal every v4 write gives on such a repo. It named the
// cluster reconciler to whoever read it, because internal/orchestrator was
// outside every wording guard in the tree until B19 removed their directory
// lists.
//
// The literal below is TYPED OUT rather than compared against the constant.
// A test that compares a constant with itself passes forever and proves
// nothing; that exact defect was found in this tree.

import (
	"strings"
	"testing"
)

func TestMixedLayoutMessage_ExactText(t *testing.T) {
	const want = "this repo has both the old and the new layout in it — sharko-engine.yaml is there, and so are the old v3 files. Finish the conversion or revert it: while both are present the engine reads one set of files and Sharko reads the other, so they disagree about which clusters and addons are real. Sharko will not change your catalog or your clusters' addons until one of the two is gone."

	if MixedLayoutMessage != want {
		t.Errorf("MixedLayoutMessage reads:\n  %q\nand the product decision is:\n  %q", MixedLayoutMessage, want)
	}

	// The wording it replaced, banned by name so it cannot come back in a
	// new constant beside this one.
	const outOf = "the cluster reconciler prefers the other"
	if strings.Contains(strings.ToLower(MixedLayoutMessage), outOf) {
		t.Errorf("MixedLayoutMessage still carries the wording it was meant to replace: %q", outOf)
	}
	if strings.Contains(strings.ToLower(MixedLayoutMessage), "reconciler") {
		t.Errorf("MixedLayoutMessage names Sharko's own machinery to a person:\n  %q", MixedLayoutMessage)
	}
}

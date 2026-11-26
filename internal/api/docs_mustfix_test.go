package api

// The docs sentences written with the must-fix round of the pre-release fix
// branch. Each "must" entry is a sentence that has to be in the page, word for
// word. Each "banned" entry was true before these fixes and is false now.

import (
	"strings"
	"testing"
)

func TestDocsMustFixPins(t *testing.T) {
	const (
		security  = "docs/site/operator/security.md"
		network   = "docs/site/operator/security-network-and-addresses.md"
		aiConfig  = "docs/site/operator/ai-config.md"
		aiRunbook = "docs/site/operator/ai-provider-misconfigured.md"
		audit     = "docs/site/operator/audit-log.md"
		gitLimit  = "docs/site/operator/git-provider-rate-limited.md"
		endpoints = "docs/site/api/endpoints.md"
		values    = "docs/site/user-guide/values-editing.md"
		drift     = "docs/site/user-guide/drift-and-sync.md"
		upgrades  = "docs/site/user-guide/upgrades.md"
		commands  = "docs/site/cli/commands.md"
		repo      = "docs/site/architecture/repo-structure.md"
	)
	docs := map[string]string{}
	for _, rel := range []string{
		security, network, aiConfig, aiRunbook, audit, gitLimit, endpoints, values, drift, upgrades, commands, repo,
	} {
		docs[rel] = flatDoc(readDocForKindPins(t, rel))
	}

	must := []struct{ doc, text string }{
		// A key follows the role of the user who made it.
		{security, "An API key never has more than the role its creator has now"},
		{security, "every key they created works with the lower role from the next call on"},
		{security, "An open audit live stream is closed as soon as its login or key is no longer good."},
		{security, "A reset does not touch the API keys the user created: they keep working."},
		// The saved AI key stays with its address. The sentence is the one the
		// server answers with (AIKeyNeededForNewAddressSentence).
		{aiConfig, AIKeyNeededForNewAddressSentence},
		{aiRunbook, AIKeyNeededForNewAddressSentence},
		{aiConfig, "Sharko does not send the saved key to the new address"},
		// Names kept for Sharko.
		{endpoints, "`sharko`, `sharko-bot`, `system`, `anonymous`, `unknown`, `ai-agent` and `webhook`"},
		{endpoints, `"The name "system" is kept for Sharko's own work. Pick another name."`},
		// Redirects on the typed chart repository address.
		{network, "every address on the way is checked by the same rules"},
		{network, "The real address is checked once more at the moment Sharko connects"},
		// Screens.
		{values, `"Sharko could not read the values file from Git just now. Nothing was changed."`},
		{values, "also when it has no bytes in it at all"},
		{values, "The link **Open the file in your Git host** is only shown when the file is there."},
		{drift, `"The last check failed, so Sharko does not know whether this secret's addon labels match Git."`},
		{drift, `"The last check did not compare this secret's addon labels with Git."`},
		{gitLimit, `"Sharko could not check the repository just now, because GitHub, or the Git host you use, says Sharko has asked too often. Nothing in Settings needs changing. It clears by itself."`},
		{audit, "A test that was refused because of the caller's role is recorded the same way, with the result `rejected`."},
		{repo, "(a cluster that already runs a newer version is left as it is)"},
	}
	for _, m := range must {
		if !strings.Contains(docs[m.doc], flatDoc(m.text)) {
			t.Errorf("%s must say:\n%s", m.doc, m.text)
		}
	}

	banned := []struct{ doc, text string }{
		// A left-out key is no longer kept whatever the address is.
		{aiRunbook, "that you leave out is kept. A different provider gets nothing carried over, so send its key and model. To remove"},
		{aiConfig, "A different provider gets nothing carried over. To remove a saved key"},
		// The unpin row no longer reads as if every cluster's version is removed.
		{repo, "from `cluster-addons/<cluster>.yaml`, and moves values"},
	}
	for _, b := range banned {
		if strings.Contains(docs[b.doc], flatDoc(b.text)) {
			t.Errorf("%s still says:\n%s", b.doc, b.text)
		}
	}
}

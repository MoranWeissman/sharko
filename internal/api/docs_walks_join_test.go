package api

// The docs sentences written when the six pre-release fix branches were
// joined. Stories A to E changed what Sharko does and says; the docs story
// (F) could not know their final wording, so the join wrote it. Each entry
// pins a sentence that must be in the page, word for word, or bans one that
// was true before those stories and is false after them.

import (
	"strings"
	"testing"
)

func TestDocsWalksJoinPins(t *testing.T) {
	const (
		upgrades   = "docs/site/user-guide/upgrades.md"
		values     = "docs/site/user-guide/values-editing.md"
		addons     = "docs/site/user-guide/addons.md"
		clusters   = "docs/site/user-guide/clusters.md"
		drift      = "docs/site/user-guide/drift-and-sync.md"
		market     = "docs/site/user-guide/marketplace.md"
		commands   = "docs/site/cli/commands.md"
		endpoints  = "docs/site/api/endpoints.md"
		overview   = "docs/site/api/overview.md"
		walk       = "docs/site/api/api-walkthrough.md"
		threeDoors = "docs/site/getting-started/three-doors.md"
		security   = "docs/site/operator/security.md"
		aiConfig   = "docs/site/operator/ai-config.md"
		aiRunbook  = "docs/site/operator/ai-provider-misconfigured.md"
		audit      = "docs/site/operator/audit-log.md"
		gitLimit   = "docs/site/operator/git-provider-rate-limited.md"
	)
	docs := map[string]string{}
	for _, rel := range []string{
		upgrades, values, addons, clusters, drift, market, commands, endpoints, overview,
		walk, threeDoors, security, aiConfig, aiRunbook, audit, gitLimit,
	} {
		docs[rel] = flatDoc(readDocForKindPins(t, rel))
	}

	must := []struct{ doc, text string }{
		// Story A: upgrades.
		{upgrades, "This upgrades the addon on ALL clusters in one pull request. It changes the version in `catalog.yaml`."},
		{upgrades, "A cluster that had its own version for this addon goes back to following the catalog."},
		{upgrades, "This moves only that cluster. The catalog and the other clusters stay."},
		{upgrades, "Version 99.0.0 of podinfo was not found in its chart repository (<address>). Nothing was changed. Pick a version that the repository lists."},
		{upgrades, "Sharko could not reach the chart repository of podinfo (<address>), so it could not check that version 6.8.0 exists. Nothing was changed. Try again in a moment."},
		{upgrades, "podinfo is already on version 6.7.1 on every cluster. Nothing to change."},
		{upgrades, "These clusters already run a newer version of podinfo than 6.8.0 and were left as they are: spoke-2 (6.9.0)."},
		{upgrades, "values/global/podinfo.yaml is shared by every cluster and still sets the old chart's value for: image.tag (6.7.1, new chart default 6.8.0)."},
		{upgrades, "Upgrade podinfo to 6.8.0 on all clusters?"},
		{upgrades, "use the box **Upgrade chosen clusters only**"},
		{upgrades, "**Running (in cluster)** is the version ArgoCD really deployed last, not the version Git asks for."},
		{upgrades, "| All clusters | `upgrade addon podinfo to 6.8.0 on all clusters` |"},
		{commands, "| `--dry-run` | Show the files the pull request would change, without opening it |"},
		{commands, "Upgrade several add-ons on all clusters in one pull request."},
		{endpoints, "| `version_not_found` | 400 |"},
		{endpoints, "| `nothing_to_change` | 400 |"},
		{endpoints, "| `chart_repo_unreadable` | 502 |"},
		{endpoints, "has a list `follows_default`"},
		// Story B: values and add-on pages.
		{values, "Editing values here is not in this version. To change them, edit the file in your Git repo: `<path>`"},
		{values, "There is no file at `<path>` in your Git repo yet. Create it there to set values."},
		{values, "None of the settings in this file take effect. They are all inside a block named"},
		{walk, "Editing values here is not in this version. Change the values file in your Git repo instead:"},
		{addons, "A namespace can only use lowercase letters, digits and hyphens. It must start and end with a letter or digit, and be 63 characters at most."},
		{addons, "Values files are shown here without their comment lines, and values that look like passwords are hidden. The pull request writes the whole file."},
		{addons, "out of all managed clusters"},
		{market, "an **In your catalog** badge appears"},
		// Story C: login, roles, keys, AI settings.
		{security, "Log out ends the login on the server."},
		{security, "Changing a role never turns a disabled account back on."},
		{security, "Sharko does not force the change."},
		{security, "an operator can revoke a key they own, and revoking anyone else's key needs an admin."},
		{endpoints, "| Revoke a key you own | operator |"},
		{endpoints, "An API key has no password to change. Log in as the user to change that user's password."},
		{endpoints, "An API key name can use letters, numbers, dots, dashes and underscores, with no spaces, up to 63 characters."},
		{commands, "you are not logged in to ADDRESS. The saved login belongs to another Sharko server and is never sent anywhere else. Run 'sharko login --server ADDRESS' first."},
		{aiConfig, "Addresses starting with `169.254` or `fe80:` are never called."},
		{aiConfig, "Take the user name and password out of the address. If the provider needs a key, put it in the API key field."},
		{aiConfig, "openai returned 401: the provider refused the API key. The provider said: \"...\""},
		{aiRunbook, "that you leave out is kept"},
		{audit, "show such a row as `NAME (API key)`"},
		// Story D: screens that say true things.
		{overview, "Sharko is getting too many requests from your address right now. Try again in a moment."},
		{overview, "GitHub, or another service Sharko uses, says Sharko has asked too often. Try again in a moment."},
		{gitLimit, "the reason `git_rate_limited`"},
		{audit, "The person or key that asked is not allowed to do this, so nothing was changed."},
		{drift, "the field **Last checked** shows when that check ran"},
		{drift, "No differences found at the last check — this secret's addon labels matched Git."},
		{security, "— register and adopt clusters, turn addons on and off, add addons to the catalog. Cannot remove things, change settings or manage users"},
		// Story E: server and command line.
		{clusters, "If an earlier adopt did not finish, run the adopt again."},
		{commands, "X is already in your catalog, with the same chart, version and namespace. Nothing to do, and no pull request was opened."},
		{commands, "X is not in your catalog and is not turned on for CLUSTER, so there is nothing to turn off. Check the name."},
		{commands, "refresh-secrets does not take a cluster name. It refreshes the add-on secrets of every cluster. Run it with no name."},
		{commands, "The pull request keeps the header comment lines of `cluster-addons/<cluster>.yaml`."},
		{commands, "Sharko only sees what ArgoCD deploys. Anything that runs on the cluster in some other way is not on this list, and taking the cluster out of Sharko does not touch it."},
		{audit, "A connection test is recorded as \"tested cluster connectivity\""},
	}
	for _, m := range must {
		if !strings.Contains(docs[m.doc], flatDoc(m.text)) {
			t.Errorf("%s is missing:\n%s", m.doc, m.text)
		}
	}

	banned := []struct{ doc, text string }{
		// Story A: where the version lives, and screens that do not exist.
		{upgrades, "Sharko updates the version in the relevant `values.yaml` file(s)"},
		{upgrades, "This updates the addon's base `values.yaml` in your repo."},
		{upgrades, "addons/cert-manager/clusters/my-staging-cluster.yaml"},
		{upgrades, "Upgrade → All Clusters"},
		{upgrades, "Clusters → [cluster name] → [addon name] → Upgrade"},
		{upgrades, "Sync to Target Version"},
		{commands, "there is no \"global version\" concept on a v4 repo"},
		{commands, "On a v4 repo there is no batch route"},
		{walk, "Older global-upgrade routes are v3-only"},
		{walk, "On v4 there is no fleet-wide version"},
		// Story B.
		{walk, "this editor doesn't support the v4 layout"},
		{market, "an **In catalog** badge"},
		{threeDoors, "(v4)"},
		{values, "this file keeps the old one until you change it"},
		// Story C.
		{aiRunbook, "If you leave out `api_key`, the saved key is cleared."},
		{aiRunbook, "The list does not include `custom-openai`"},
		{endpoints, "| Revoke a key | admin |"},
		{security, "must change it on first login"},
		{commands, "must change it on first login"},
		// Story D.
		{overview, "Other endpoints are not currently rate-limited."},
		{drift, "(the **Last sync** line)"},
	}
	for _, b := range banned {
		if strings.Contains(docs[b.doc], flatDoc(b.text)) {
			t.Errorf("%s still says:\n%s", b.doc, b.text)
		}
	}
}

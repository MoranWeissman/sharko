package api

// docs_walks_f_test.go — pins for the docs lines the ten test walks found
// wrong (pre-release fixes, story F).
//
// What the walks found, in short:
//   - repo-structure.md showed file shapes Sharko does not write (a
//     metadata/spec wrapper, list-style addons), and said every registered
//     cluster gets its own addon file at once (it gets it at the first
//     addon switched on).
//   - values-editing.md said per-cluster values files stay wrapped under the
//     addon name. A wrapped file is silently ignored by Helm. It also
//     described an editor that cannot save on a v4 repo.
//   - addons.md pointed at `sharko add-addon` (refused on a v4 repo), a
//     `--values` flag that does not exist, and a secrets recipe that
//     delivers nothing because it never names the `push:` block.
//   - drift-and-sync.md said self-heal is off and labels stay changed. On a
//     v4 repo addon labels are always put back.
//
// Two kinds of check live here. The pins say the exact new sentence is
// there and the old one is gone. The shape tests go further: they take
// the YAML examples out of the docs and run them through the same schema
// check the server uses, because a pin on a sentence cannot tell you the
// example under it still parses.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/schema"
)

func flatDoc(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestDocsWalksFPins(t *testing.T) {
	docs := map[string]string{}
	for _, rel := range []string{
		"docs/site/architecture/repo-structure.md",
		"docs/site/user-guide/values-editing.md",
		"docs/site/user-guide/addons.md",
		"docs/site/user-guide/clusters.md",
		"docs/site/user-guide/drift-and-sync.md",
		"docs/site/user-guide/connections.md",
		"docs/site/user-guide/secrets-provider.md",
		"docs/site/user-guide/smart-values.md",
		"docs/site/cli/commands.md",
		"docs/site/api/overview.md",
		"docs/site/api/endpoints.md",
		"docs/site/operator/ai-config.md",
		"docs/site/operator/gitea-provider.md",
		"docs/site/developer-guide/logging.md",
		"docs/site/operator/budget-burn-runbook.md",
		"docs/site/operator/failure-mode-index.md",
		"README.md",
	} {
		docs[rel] = flatDoc(readDocForKindPins(t, rel))
	}

	must := []struct{ doc, text string }{
		// repo-structure.md
		{"docs/site/architecture/repo-structure.md", "There is no `metadata:` or `spec:` block — `clusters:` sits at the top."},
		{"docs/site/architecture/repo-structure.md", "`addons:` is a map: the key is the addon's name."},
		{"docs/site/architecture/repo-structure.md", "A cluster has no file here until the first addon is turned on for it."},
		{"docs/site/architecture/repo-structure.md", "Registering a cluster does **not** create it."},
		{"docs/site/architecture/repo-structure.md", "**Do not wrap them under the addon's name.**"},
		{"docs/site/architecture/repo-structure.md", "### What stays after you turn an addon off"},
		{"docs/site/architecture/repo-structure.md", "The `enabled: false` entry in `cluster-addons/<cluster>.yaml`."},
		{"docs/site/architecture/repo-structure.md", "| Annotation | `sharko.dev/revision` | The Git commit the last write was built from. |"},
		{"docs/site/architecture/repo-structure.md", "`addons.sharko.dev/<addon>=enabled`"},
		// values-editing.md
		{"docs/site/user-guide/values-editing.md", "!!! note \"Editing in Sharko is not in this version\""},
		{"docs/site/user-guide/values-editing.md", "There is no **Submit changes** button."},
		{"docs/site/user-guide/values-editing.md", "| **Per-cluster overrides** | Just one addon on one cluster | `values/clusters/<cluster>/<addon>.yaml` | **Top-level chart values** (no `<addon>:` wrapper) | Only the chosen cluster |"},
		{"docs/site/user-guide/values-editing.md", "!!! warning \"A file wrapped under the addon's name does nothing\""},
		{"docs/site/user-guide/values-editing.md", "Helm looks for a chart value called `podinfo`, finds none, and ignores the whole file."},
		{"docs/site/user-guide/values-editing.md", "ArgoCD still shows the addon as Synced and Healthy, and nothing changes on the cluster."},
		{"docs/site/user-guide/values-editing.md", "(`sharko validate` does not check a v4 repo.)"},
		{"docs/site/user-guide/values-editing.md", "Helm reads a dotted name in quotes as one key with dots in it, not as a path."},
		// addons.md
		{"docs/site/user-guide/addons.md", "On a v4 repo, use `sharko add-to-catalog`."},
		{"docs/site/user-guide/addons.md", "`sharko add-addon` is the command for the older (v3) layout. On a v4 repo it is refused with a `409`. There is no `--values` flag on either command."},
		{"docs/site/user-guide/addons.md", "Both forms have an optional field, **Also enable on a cluster (optional)**."},
		{"docs/site/user-guide/addons.md", "A secret is delivered only when its catalog entry has a **`push:` block**."},
		{"docs/site/user-guide/addons.md", "An entry with only a `name` and a `description` is a note for people. Sharko does not deliver anything for it, and nothing on screen says so."},
		{"docs/site/user-guide/addons.md", "api-key: sharko/addons/datadog/api-key"},
		{"docs/site/user-guide/addons.md", "With **AWS Secrets Manager**, every path must start with the **prefix** set in **Settings → Secrets Provider**."},
		{"docs/site/user-guide/addons.md", "The command always covers every cluster. It takes no cluster name."},
		{"docs/site/user-guide/addons.md", "# Add the SELF-HEAL and SYNC-OPTIONS columns:"},
		{"docs/site/user-guide/addons.md", "You change these files in Git, with a pull request."},
		// clusters.md
		{"docs/site/user-guide/clusters.md", "Its addon file, `cluster-addons/<name>.yaml`, is created the first time you turn an addon on for it"},
		{"docs/site/user-guide/clusters.md", "--add-addon cert-manager,metrics-server"},
		{"docs/site/user-guide/clusters.md", "In the UI, an adopted cluster has an **Un-adopt** button on the **Clusters** page."},
		// Moved at the join: story E made adopt say so and open nothing.
		{"docs/site/user-guide/clusters.md", "If the cluster is already adopted, Sharko says \"X is already adopted. Nothing to do, and no pull request was opened.\""},
		// drift-and-sync.md
		{"docs/site/user-guide/drift-and-sync.md", "!!! danger \"Taking an addon label off by hand uninstalls the addon\""},
		{"docs/site/user-guide/drift-and-sync.md", "**The self-heal setting does not change this**: it is on even when `managed_cluster_self_heal` is off."},
		{"docs/site/user-guide/drift-and-sync.md", "**The pill shows the last check, not this second.**"},
		// connections.md, ai-config.md, gitea
		{"docs/site/user-guide/connections.md", "`gemini`, `claude`, `openai`, `ollama`, or `custom-openai`"},
		{"docs/site/user-guide/connections.md", "The AI assistant shows up as a floating panel on every page **only after a provider is saved**."},
		{"docs/site/user-guide/connections.md", "There is no switch for this in the AI settings."},
		{"docs/site/user-guide/connections.md", "**Settings → Connection** does not have a Provider field, so it cannot be changed there."},
		{"docs/site/operator/gitea-provider.md", "**Settings → Connection** does not have a Provider field, so it cannot switch a connection to Gitea later."},
		{"docs/site/operator/ai-config.md", "| Custom OpenAI-compatible (`custom-openai`) | API key and base URL |"},
		// secrets provider
		{"docs/site/user-guide/secrets-provider.md", "The message lists more names than Sharko supports. Only `aws-sm` and `k8s-secrets` work for addon secret values today."},
		// CLI reference
		{"docs/site/cli/commands.md", "Stores the session token in `~/.sharko/config` (a file with no extension, readable only by you)."},
		// Moved at the join: story E made validate fail when it found neither file.
		{"docs/site/cli/commands.md", "!!! warning \"On a v4 repo this command fails and points you to validate-config\""},
		{"docs/site/cli/commands.md", "nothing was checked: neither configuration/addons-catalog.yaml nor configuration/managed-clusters.yaml was found."},
		{"docs/site/cli/commands.md", "# Server: https://sharko.your-domain.com (connected, 4.0.8, Kubernetes)"},
		// Moved at the join: story C changed the default role of token create to viewer.
		{"docs/site/cli/commands.md", "If you leave it out, the key is a `viewer` key, the same as in the UI and the API."},
		{"docs/site/cli/commands.md", "The command takes no cluster name."},
		{"docs/site/cli/commands.md", "!!! warning \"Not for a v4 repo\""},
		{"docs/site/cli/commands.md", "`sharko pr wait <id>` keeps checking and exits `0` when the pull request is merged, `1` when it is closed without a merge, and `2` when the wait runs out."},
		// API roles
		{"docs/site/api/overview.md", "| `operator` | Day-to-day changes:"},
		{"docs/site/api/endpoints.md", "| `GET` | `/api/v1/tokens` | List API keys (any signed-in user; never shows the key values) |"},
		// chart values that do not exist
		{"docs/site/developer-guide/logging.md", "The Helm chart has no setting for it. Set it through `extraEnv`:"},
		{"docs/site/operator/budget-burn-runbook.md", "The chart has no value for the handler timeout, so it cannot be changed from Helm."},
		// README
		{"README.md", "| `sharko add-to-catalog <name>` | Add an addon to the catalog (opens a pull request)."},
		{"README.md", "| `sharko refresh-secrets` | Trigger immediate secrets reconcile (all clusters) |"},
	}
	for _, m := range must {
		if !strings.Contains(docs[m.doc], flatDoc(m.text)) {
			t.Errorf("%s is missing:\n%s", m.doc, m.text)
		}
	}

	banned := []struct{ doc, text string }{
		// the old, wrong shapes and claims
		{"docs/site/architecture/repo-structure.md", "kind: ManagedClusters\nmetadata:"},
		{"docs/site/architecture/repo-structure.md", "kind: AddonCatalog\nmetadata:"},
		{"docs/site/architecture/repo-structure.md", "kind: ClusterAddons\nmetadata:"},
		{"docs/site/architecture/repo-structure.md", "Each cluster gets its own file."},
		{"docs/site/architecture/repo-structure.md", "Sharko writes these files when you register a cluster"},
		{"docs/site/user-guide/values-editing.md", "Per-cluster files stay wrapped"},
		{"docs/site/user-guide/values-editing.md", "that's correct, because one file holds overrides for many addons"},
		{"docs/site/user-guide/values-editing.md", "Click **Submit changes**."},
		{"docs/site/user-guide/values-editing.md", "update global values for <addon>"},
		{"docs/site/user-guide/addons.md", "Sharko blocks enabling an addon at install time until a matching secret has been registered."},
		{"docs/site/user-guide/addons.md", "Registering the actual value — which provider path fills which key, in which namespace — is a separate step, covered next."},
		{"docs/site/user-guide/addons.md", "| `--values` | No | Path to a values YAML file to use as the base |"},
		{"docs/site/user-guide/addons.md", "Include full catalog config (secrets declarations, values)"},
		{"docs/site/user-guide/addons.md", "use the UI's values editor on the addon page"},
		{"docs/site/user-guide/addons.md", "sharko refresh-secrets prod-eu"},
		{"docs/site/user-guide/addons.md", "sets the addon label on that cluster's values file"},
		{"docs/site/cli/commands.md", "sharko refresh-secrets prod-eu"},
		{"docs/site/cli/commands.md", "~/.sharko/config.yaml"},
		{"docs/site/cli/commands.md", "# CLI: v2.0.1"},
		{"docs/site/cli/commands.md", "# Server: v2.0.1"},
		{"docs/site/cli/commands.md", "| `--role <role>` | `admin`, `operator`, or `viewer` |"},
		// Banned at the join: true before stories C and E, false after them.
		{"docs/site/cli/commands.md", "the CLI makes an `admin` key"},
		{"docs/site/cli/commands.md", "On a v4 repo this command checks nothing"},
		{"docs/site/cli/commands.md", "prints `[SKIP]` for both files and exits `0`"},
		{"docs/site/user-guide/clusters.md", "It does not stop you from adopting a cluster that is already in `managed-clusters.yaml`."},
		{"docs/site/user-guide/clusters.md", "the pull request it opens has no changes. Close it."},
		{"docs/site/cli/commands.md", "(defaults to addon name)"},
		{"docs/site/cli/commands.md", "`--git-token <token>` | PAT or access token (required)"},
		{"docs/site/user-guide/clusters.md", "Edit Addons"},
		{"docs/site/user-guide/clusters.md", "(Apply Changes)"},
		{"docs/site/user-guide/clusters.md", "--addons cert-manager,metrics-server,logging"},
		{"docs/site/user-guide/clusters.md", "and that Sharko does not manage it already."},
		{"docs/site/user-guide/drift-and-sync.md", "The cluster stays OutOfSync until you click **Sync** or enable self-heal."},
		{"docs/site/user-guide/drift-and-sync.md", "reads the cluster entry from `managed-clusters.yaml` in your GitOps repository and compares"},
		{"docs/site/user-guide/connections.md", "`openai`, `claude`, `gemini`, `ollama`, or `custom`"},
		{"docs/site/user-guide/connections.md", "`claude-3-5-sonnet`"},
		{"docs/site/user-guide/connections.md", "require admin role and explicit opt-in in the AI settings"},
		{"docs/site/operator/gitea-provider.md", "Settings → Connection → Git → **Provider: Gitea**"},
		{"docs/site/operator/ai-config.md", "API key and base address"},
		{"docs/site/user-guide/secrets-provider.md", "(aws-sm, k8s-secrets, gcp-sm, azure-kv, or vault)"},
		{"docs/site/api/overview.md", "Write endpoints require the `admin` role."},
		{"docs/site/api/endpoints.md", "List API keys (admin only)"},
		{"docs/site/api/endpoints.md", "List audit log entries (admin only)"},
		{"docs/site/api/endpoints.md", "All write endpoints require the `admin` role."},
		{"docs/site/developer-guide/logging.md", "Helm sets this via `config.logLevel`."},
		{"docs/site/operator/budget-burn-runbook.md", "`catalog.sources` in Helm values"},
		{"docs/site/operator/budget-burn-runbook.md", "Adjust `config.dashboard.timeout`"},
		{"docs/site/operator/failure-mode-index.md", "Helm `secrets.GITHUB_TOKEN`"},
		{"README.md", "| `sharko add-addon <name>` | Add addon to catalog |"},
		{"README.md", "sharko refresh-secrets [cluster]"},
	}
	for _, b := range banned {
		if strings.Contains(docs[b.doc], flatDoc(b.text)) {
			t.Errorf("%s still says %q", b.doc, b.text)
		}
	}
}

// docYAMLBlocks returns the body of every ```yaml fence in a doc that starts
// with the Sharko envelope. Indented fences (inside a list item or an
// admonition) are handled by stripping the common leading spaces.
func docYAMLBlocks(doc string) []string {
	fence := regexp.MustCompile("(?s)([ ]*)```yaml\n(.*?)\n[ ]*```")
	var out []string
	for _, m := range fence.FindAllStringSubmatch(doc, -1) {
		indent := len(m[1])
		lines := strings.Split(m[2], "\n")
		for i, l := range lines {
			if len(l) >= indent && strings.TrimSpace(l[:indent]) == "" {
				lines[i] = l[indent:]
			}
		}
		body := strings.Join(lines, "\n")
		if strings.Contains(body, "apiVersion: sharko.dev/v1") {
			out = append(out, body)
		}
	}
	return out
}

// The examples in the docs are what people copy. If an example is not a
// file Sharko accepts, the docs are wrong, whatever the sentence around it
// says. This is the check the walk 1 finding asked for: the file shapes in
// repo-structure.md (and the secrets recipe in addons.md) have to pass the
// same schema the server reads with.
func TestDocsWalksFExamplesPassTheRealSchema(t *testing.T) {
	v, err := schema.DefaultValidator()
	if err != nil {
		t.Fatalf("schema validator: %v", err)
	}
	for _, rel := range []string{
		"docs/site/architecture/repo-structure.md",
		"docs/site/user-guide/addons.md",
	} {
		blocks := docYAMLBlocks(readDocForKindPins(t, rel))
		if len(blocks) == 0 {
			t.Fatalf("%s: no Sharko YAML examples found — the extractor or the doc changed", rel)
		}
		kinds := map[string]bool{}
		for _, b := range blocks {
			if strings.Contains(b, "kind: DefaultAddons") {
				// Documented separately, and not part of this story.
				continue
			}
			if err := v.ValidateAutoDetect([]byte(b)); err != nil {
				t.Errorf("%s: an example does not pass the schema:\n%s\n%v", rel, b, err)
			}
			for _, k := range []string{"ManagedClusters", "AddonCatalog", "ClusterAddons"} {
				if strings.Contains(b, "kind: "+k) {
					kinds[k] = true
				}
			}
		}
		if rel == "docs/site/architecture/repo-structure.md" {
			for _, k := range []string{"ManagedClusters", "AddonCatalog", "ClusterAddons"} {
				if !kinds[k] {
					t.Errorf("%s has no checked example of kind %s", rel, k)
				}
			}
		}
		if rel == "docs/site/user-guide/addons.md" && !kinds["AddonCatalog"] {
			t.Errorf("%s has no checked AddonCatalog example (the secrets recipe)", rel)
		}
	}
}

// The secrets recipe is only worth anything if the `push:` block in it is
// the shape the schema wants (secretName, namespace, keys). A recipe whose
// push block is missing a key must fail the schema, and this proves the
// check above could catch it.
func TestDocsWalksFSchemaCatchesAHalfPushBlock(t *testing.T) {
	v, err := schema.DefaultValidator()
	if err != nil {
		t.Fatalf("schema validator: %v", err)
	}
	bad := `apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
  datadog:
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: "3.74.0"
    namespace: datadog
    secrets:
      - name: Datadog API key
        description: x
        push:
          secretName: datadog-keys
          namespace: datadog
`
	if err := v.ValidateAutoDetect([]byte(bad)); err == nil {
		t.Fatal("a push block with no keys passed the schema; the docs example check cannot catch a half-written recipe")
	}
}

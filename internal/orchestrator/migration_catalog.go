// Package orchestrator — turning a v3 full-copy catalog into a v4 catalog
// (v4 Wave 2 Story 5.2; re-pointed at the approved-list model in Wave 2.5
// Lane 4, design doc 2026-07-31-catalog-approved-model.md §7).
//
// A v3 repo's configuration/addons-catalog.yaml was already, in practice,
// the org's approved list: every addon the fleet ran, Sharko's own curated
// entries and the user's own, side by side and indistinguishable. The v4
// catalog.yaml (kind AddonCatalog) is now that same thing — the org's
// full, approved list — so the conversion is simple: every named v3 entry
// becomes one full, self-contained v4 entry, straight across.
//
// An earlier version of this converter compared each v3 entry against the
// shipped curated list and dropped it from the delta when every field
// matched. That made sense under the old "catalog.yaml holds only your
// changes" model, but it does not survive the redesign: an addon the org
// still runs, and never touched since bootstrap, would vanish from its own
// approved list the moment it happened to agree with the curated entry —
// which was the exact bug (45 addons nobody chose looking approved, in
// reverse) that started this redesign. There is no shipped list to
// subtract against any more, so there is nothing left to compare.
//
// The v3 secrets: block (internal/models.AddonSecretRef — a real
// secret-push definition: a Kubernetes Secret name, a namespace, and a
// data-key -> provider-path map) moves into the addon's needed-secrets
// list WHOLE. Each v3 ref becomes one config.AddonSecretRequirement: the
// plain-English half (name, description, install/runtime timing) plus a
// `push:` block holding the very same AddonSecretRef the v3 file carried.
//
// Carrying the push block, rather than describing it in prose, is what
// keeps a migrated repo's credentials alive: the secrets reconciler reads
// those blocks and keeps pushing the same Secret to the same namespace
// from the same provider paths, so the first credential rotation after a
// migration lands on the clusters exactly as it did before. See
// secretRequirementsForV3.
package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// effectiveNamespace is the namespace an addon actually installs into: the
// entry's own value, or the addon name when it has none. Both the v3
// ApplicationSet template and the v4 engine chart apply exactly this
// fallback (`namespace | default name`), so comparing EFFECTIVE namespaces
// — rather than the raw fields — is what keeps a migrated addon in the
// namespace it is running in today.
func effectiveNamespace(namespace, addonName string) string {
	if namespace != "" {
		return namespace
	}
	return addonName
}

// catalogEntryForV3 builds the full v4 catalog entry for one v3 catalog
// entry. Every field the v3 entry carries goes straight across — there is
// no shipped list underneath it any more to leave a blank for, so a
// converted entry is exactly as deployable as the ApplicationSet it
// replaces. Namespace keeps the effective-value rule: an entry that never
// set one still installs into a namespace named after the addon, so
// writing the blank through is honest rather than lossy.
func catalogEntryForV3(v3 models.AddonCatalogEntry) config.AddonCatalogEntry {
	out := config.AddonCatalogEntry{
		RepoURL:   v3.RepoURL,
		Chart:     v3.Chart,
		Namespace: v3.Namespace,
		Version:   v3.Version,
	}

	if settings := settingsForV3(v3); settings != nil {
		out.Settings = settings
	}
	if len(v3.AdditionalSources) > 0 {
		out.AdditionalSources = v3.AdditionalSources
	}
	if len(v3.ExtraHelmValues) > 0 {
		out.ExtraHelmValues = v3.ExtraHelmValues
	}
	if secrets := secretRequirementsForV3(v3.Secrets); len(secrets) > 0 {
		out.Secrets = secrets
	}

	return out
}

// settingsForV3 maps the three v3 deployment-behaviour fields onto the v4
// settings block, or returns nil when the entry sets none of them.
//
// The v3 entry has no namespace/createNamespace/prune/preserve fields —
// those either did not exist or were fixed by the ApplicationSet template —
// so only these three can carry over. Nothing is invented: an unset v3
// field stays unset in v4, which means "whatever the engine defaults to",
// exactly as before.
func settingsForV3(v3 models.AddonCatalogEntry) *config.AddonSettings {
	if v3.SelfHeal == nil && len(v3.SyncOptions) == 0 && len(v3.IgnoreDifferences) == 0 {
		return nil
	}
	return &config.AddonSettings{
		SelfHeal:          v3.SelfHeal,
		SyncOptions:       v3.SyncOptions,
		IgnoreDifferences: v3.IgnoreDifferences,
	}
}

// secretRequirementsForV3 converts the v3 secrets: block into the v4
// needed-secrets list, whole.
//
// A v4 requirement has two halves. The plain-English half (name,
// description) says what the credential is for; the `push:` half is the
// machine-readable definition — which Kubernetes Secret Sharko creates, in
// which namespace, and which provider path fills each data key. The v3
// block IS that second half, so it goes across as-is, in the same
// models.AddonSecretRef type with the same yaml keys.
//
// Nothing is left behind in git history. That matters more than it sounds:
// an earlier version of this converter flattened the push definition into a
// sentence, which meant a migrated repo had no machine-readable definition
// left anywhere, and the secrets reconciler had nothing to act on — the
// clusters kept the credentials they already had until the first rotation,
// then quietly went stale (v4 wave 2.5 review, finding F1).
//
// RequiredFor is left unset. Unset defaults to "install" — the stricter
// reading (config.AddonSecretRequirement docs) — which matches how these
// secrets already behaved: EnableAddon has always refused to run without
// them (creds_gate.go), for every v3 secret, with no install/runtime
// distinction to preserve.
func secretRequirementsForV3(v3 []models.AddonSecretRef) []config.AddonSecretRequirement {
	if len(v3) == 0 {
		return nil
	}
	out := make([]config.AddonSecretRequirement, 0, len(v3))
	for _, ref := range v3 {
		name := ref.SecretName
		if name == "" {
			name = "unnamed secret"
		}

		keys := make([]string, 0, len(ref.Keys))
		for k := range ref.Keys {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var desc strings.Builder
		desc.WriteString(fmt.Sprintf("Sharko creates the Kubernetes Secret %q", name))
		if ref.Namespace != "" {
			desc.WriteString(fmt.Sprintf(" in namespace %q", ref.Namespace))
		}
		desc.WriteString(" on every cluster running this addon")
		if len(keys) > 0 {
			desc.WriteString(fmt.Sprintf(", with keys: %s", strings.Join(keys, ", ")))
		}
		desc.WriteString(". The paths it reads those values from are in the push block below, carried over from your old catalog file.")

		// The push block is a COPY — the Keys map especially, so the
		// converted entry can never share a map with the parsed v3
		// document.
		push := models.AddonSecretRef{
			SecretName: ref.SecretName,
			Namespace:  ref.Namespace,
		}
		if len(ref.Keys) > 0 {
			push.Keys = make(map[string]string, len(ref.Keys))
			for k, v := range ref.Keys {
				push.Keys[k] = v
			}
		}

		out = append(out, config.AddonSecretRequirement{
			Name:        name,
			Description: desc.String(),
			Push:        &push,
		})
	}
	return out
}

// buildCatalogFromV3 converts a whole v3 catalog into the v4 catalog spec.
// Every named entry becomes one full v4 entry — nothing is dropped except
// an entry with no name at all, which cannot key a v4 map and is noted
// instead of guessed at.
//
// The returned notes are plain-English sentences for the preview and the
// PR body: they name anything worth a second look before merging, so
// nobody discovers it from a broken cluster instead.
func buildCatalogFromV3(v3Entries []models.AddonCatalogEntry) (config.AddonCatalogSpec, []string) {
	spec := config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{}}
	var notes []string

	// Sorted so the notes (and any error) come out in the same order every
	// run — a preview that reshuffles between two identical calls reads as
	// a bug even when it isn't.
	sorted := append([]models.AddonCatalogEntry(nil), v3Entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	for _, v3 := range sorted {
		if v3.Name == "" {
			notes = append(notes, "one addon in your catalog has no name — it was left out of the new catalog file")
			continue
		}

		spec.Addons[v3.Name] = catalogEntryForV3(v3)

		if len(v3.Secrets) > 0 {
			notes = append(notes, fmt.Sprintf(
				"%s's secrets moved into its own entry in the new catalog file, under \"secrets\" — the secret name, namespace and provider paths came across unchanged, so Sharko keeps pushing them to your clusters exactly as before",
				v3.Name))
		}
	}

	return spec, notes
}

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
// data-key -> provider-path map) has no v4 field of the same shape. It
// converts into the addon's needed-secrets list
// (config.AddonSecretRequirement — the plain-English install/runtime split
// shipped in Wave 2 #634), which is the extension point this data now
// lives in. See secretRequirementsForV3.
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
// needed-secrets list.
//
// The two shapes describe different things: a v3 AddonSecretRef is a
// push definition (the Kubernetes Secret name and namespace Sharko
// creates, and which provider path fills each data key), while a v4
// AddonSecretRequirement is a plain-English "the addon needs this, and
// when" declaration. There is no lossless field mapping between them, so
// the conversion writes a plain-English description naming what the old
// definition said, and leaves RequiredFor unset. Unset defaults to
// "install" — the stricter reading (config.AddonSecretRequirement docs) —
// which matches how these secrets already behaved: EnableAddon has always
// refused to run without them (creds_gate.go), for every v3 secret, with
// no install/runtime distinction to preserve.
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
		desc.WriteString(fmt.Sprintf("Migrated from your old catalog file: the Kubernetes Secret %q", name))
		if ref.Namespace != "" {
			desc.WriteString(fmt.Sprintf(" in namespace %q", ref.Namespace))
		}
		if len(keys) > 0 {
			desc.WriteString(fmt.Sprintf(", with keys: %s", strings.Join(keys, ", ")))
		}
		desc.WriteString(".")

		out = append(out, config.AddonSecretRequirement{
			Name:        name,
			Description: desc.String(),
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
				"%s's secrets from your old catalog file now live inside its own entry in the new catalog file, under \"secrets\" — check the descriptions still match your provider setup",
				v3.Name))
		}
	}

	return spec, notes
}

// Package orchestrator — turning a v3 full-copy catalog into a v4 delta
// (v4 Wave 2 Story 5.2).
//
// A v3 repo's configuration/addons-catalog.yaml is a FULL COPY of every
// addon the fleet runs: Sharko's own curated entries and the user's own,
// side by side, indistinguishable. The v4 file (catalog.yaml, kind
// AddonCatalog) holds ONLY the user's changes — the curated set ships
// inside the engine chart (design doc §2.3).
//
// So the conversion is a subtraction: for every field the shipped catalog
// already supplies with the SAME value, drop it; keep everything else. An
// entry that ends up with nothing left is dropped entirely. The invariant
// that makes this safe is the round trip —
// catalog.MergeDelta(curated, delta) must reproduce the user's effective
// v3 entry for every addon they had — and migration_v3v4_test.go asserts it
// per entry rather than trusting the rules below to be exhaustive.
package orchestrator

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/MoranWeissman/sharko/internal/catalog"
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

// deltaEntryForV3 builds the v4 delta entry for one v3 catalog entry, given
// the shipped (curated) entry for the same addon if there is one. The
// second return is true when the entry carries nothing the shipped catalog
// does not already say — i.e. it can be dropped from the delta entirely.
func deltaEntryForV3(v3 models.AddonCatalogEntry, curated catalog.CatalogEntry, isCurated bool) (config.AddonCatalogEntry, bool) {
	out := config.AddonCatalogEntry{}

	// Deployment coordinates go straight across, always. A catalog entry is
	// self-contained now — there is no shipped list underneath it to fill
	// a blank in — so dropping a field because the curated list happens to
	// say the same thing would produce a file the engine cannot render.
	// Namespace keeps the effective-value rule: an entry that never set one
	// still installs into a namespace named after the addon, so writing the
	// blank through is honest rather than lossy.
	out.RepoURL = v3.RepoURL
	out.Chart = v3.Chart
	out.Namespace = v3.Namespace

	// Version is ALWAYS carried. The shipped catalog deliberately holds no
	// version (design doc D7 — a version baked into a signed artefact goes
	// stale), so the user's pinned version has nowhere else to live and
	// dropping it would silently move every addon onto whatever the engine
	// defaults to.
	out.Version = v3.Version

	// Deployment behaviour. The curated catalog carries no settings block
	// today, so anything the v3 entry set is by definition the user's.
	if settings := deltaSettingsForV3(v3); settings != nil {
		out.Settings = settings
	}
	if len(v3.AdditionalSources) > 0 {
		out.AdditionalSources = v3.AdditionalSources
	}
	if len(v3.ExtraHelmValues) > 0 {
		out.ExtraHelmValues = v3.ExtraHelmValues
	}

	return out, isCurated && reflect.DeepEqual(out, config.AddonCatalogEntry{})
}

// deltaSettingsForV3 maps the three v3 deployment-behaviour fields onto the
// v4 settings block, or returns nil when the entry sets none of them.
//
// The v3 entry has no namespace/createNamespace/prune/preserve fields —
// those either did not exist or were fixed by the ApplicationSet template —
// so only these three can carry over. Nothing is invented: an unset v3
// field stays unset in v4, which means "whatever the engine defaults to",
// exactly as before.
func deltaSettingsForV3(v3 models.AddonCatalogEntry) *config.AddonSettings {
	if v3.SelfHeal == nil && len(v3.SyncOptions) == 0 && len(v3.IgnoreDifferences) == 0 {
		return nil
	}
	return &config.AddonSettings{
		SelfHeal:          v3.SelfHeal,
		SyncOptions:       v3.SyncOptions,
		IgnoreDifferences: v3.IgnoreDifferences,
	}
}

// buildCatalogDelta converts a whole v3 catalog into the v4 delta spec.
// curated may be nil (no shipped catalog wired) — every addon is then
// carried over whole, which is the correct, lossless answer for a caller
// that cannot tell curated from user-added.
//
// The returned notes are plain-English sentences for the preview and the PR
// body: they name what could NOT be carried across, so nobody discovers it
// from a broken cluster instead.
func buildCatalogDelta(v3Entries []models.AddonCatalogEntry, curated *catalog.Catalog) (config.AddonCatalogSpec, []string) {
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
		var curatedEntry catalog.CatalogEntry
		isCurated := false
		if curated != nil {
			curatedEntry, isCurated = curated.Get(v3.Name)
		}

		entry, droppable := deltaEntryForV3(v3, curatedEntry, isCurated)
		if !droppable {
			spec.Addons[v3.Name] = entry
		}

		if len(v3.Secrets) > 0 {
			notes = append(notes, fmt.Sprintf(
				"%s lists secrets in your old catalog file. The new catalog file has no place for them yet, so they are not carried over — the addon still deploys, but check its secrets before you rely on them",
				v3.Name))
		}
	}

	return spec, notes
}

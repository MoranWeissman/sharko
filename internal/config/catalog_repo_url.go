package config

import (
	"fmt"
	"sort"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
)

// catalog_repo_url.go — the check that stops a credential-bearing repository
// address from being written into a catalog file, in the two functions every
// catalog write funnels through.
//
// # Why here and not at each door
//
// A door catches the operator who is typing right now, and it is worth having
// for the clear message it can give them. It cannot catch the next caller
// somebody adds. MarshalAddonCatalog and SaveAddonCatalog are the last thing
// that runs before catalog bytes exist, and every writer in the codebase ends
// in one of them, so a check here cannot be walked around by writing new code.
//
// # Why it refuses the whole file rather than fixing the one entry
//
// Because the alternative is Sharko silently editing an operator's repository.
// If a catalog already carries a token in an address, every ordinary catalog
// write — a version bump on a completely different addon — rewrites the whole
// file. Dropping the entry there would delete an addon nobody asked to delete;
// blanking the address there would replace what the operator wrote with
// something that does not work. Refusing the write leaves the file exactly as
// it is and tells the operator which field to fix.
//
// The reading side is deliberately NOT this strict — see
// internal/catalog.BuildCatalogView. A catalog that already carries such an
// address must still load, so Sharko keeps running and the rest of the addons
// keep working; only the one entry is marked unusable.

// checkV3CatalogRepoURLs returns the first refusal in the v3 catalog entries,
// checked in the order the entries will be written so the same file always
// reports the same field first.
func checkV3CatalogRepoURLs(entries []models.AddonCatalogEntry) error {
	for _, e := range entries {
		field := fmt.Sprintf("spec.applicationsets[%s].repoURL", e.Name)
		if err := credsafe.ValidateSupportedRepoURLAt(AddonCatalogFilename, field, e.RepoURL); err != nil {
			return err
		}
		for i, s := range e.AdditionalSources {
			field := fmt.Sprintf("spec.applicationsets[%s].additionalSources[%d].repoURL", e.Name, i)
			if err := credsafe.ValidateSupportedRepoURLAt(AddonCatalogFilename, field, s.RepoURL); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkV4CatalogRepoURLs is the same for the v4 catalog.yaml. The addons map
// is walked in sorted-name order for the same determinism.
func checkV4CatalogRepoURLs(spec AddonCatalogSpec) error {
	names := make([]string, 0, len(spec.Addons))
	for name := range spec.Addons {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		e := spec.Addons[name]
		field := fmt.Sprintf("addons.%s.repoURL", name)
		if err := credsafe.ValidateSupportedRepoURLAt(AddonCatalogPath, field, e.RepoURL); err != nil {
			return err
		}
		for i, s := range e.AdditionalSources {
			field := fmt.Sprintf("addons.%s.additionalSources[%d].repoURL", name, i)
			if err := credsafe.ValidateSupportedRepoURLAt(AddonCatalogPath, field, s.RepoURL); err != nil {
				return err
			}
		}
	}
	return nil
}

// Package orchestrator — referential-integrity guard (V2-cleanup-22, Part 2).
//
// Before Sharko labels a cluster for an addon or writes per-cluster values
// for it, the addon MUST exist in the addon catalog (addons-catalog.yaml).
// Enabling / valuing an addon that is not in the catalog produces config
// that ArgoCD's ApplicationSet generator can never render — the label points
// at an ApplicationSet entry that does not exist — so the gitops repo ends up
// internally inconsistent and breaks render for the whole cluster set.
//
// This guard is the single shared membership check the three write paths
// (EnableAddon, RegisterCluster, SetClusterAddonValues) call before they
// touch Git. A genuine catalog READ failure is surfaced (returned as a plain
// error → 502 at the API edge) rather than swallowed, because a missing or
// unreadable catalog in production is itself a real problem the operator must
// see — the old `catalog, _ = parseAddonsCatalog(...)` swallow hid it.
// An addon that is simply absent from a successfully-read catalog is a USER
// error and is returned as *AddonNotInCatalogError, which the API layer maps
// to a 4xx (422) telling the caller to add the addon to the catalog first.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
)

// AddonNotInCatalogError is returned by the referential-integrity guard when
// one or more requested addons are not present in the addon catalog. It
// carries the offending addon name(s) so the API layer can render a clear
// 4xx message. The single-addon paths (EnableAddon, SetClusterAddonValues)
// populate exactly one name; RegisterCluster may populate several.
type AddonNotInCatalogError struct {
	Addons []string
}

// Error implements error with a clear, actionable message that names the
// addon(s) and tells the caller the fix (add to the catalog first).
func (e *AddonNotInCatalogError) Error() string {
	if e == nil || len(e.Addons) == 0 {
		return "addon is not in the catalog — add it to the catalog first"
	}
	if len(e.Addons) == 1 {
		return fmt.Sprintf("addon %q is not in the catalog — add it to the catalog first", e.Addons[0])
	}
	quoted := make([]string, len(e.Addons))
	for i, a := range e.Addons {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return fmt.Sprintf("addons %s are not in the catalog — add them to the catalog first", strings.Join(quoted, ", "))
}

// IsAddonNotInCatalog reports whether err is (or wraps) an
// *AddonNotInCatalogError. The API layer uses this to choose a 4xx status.
func IsAddonNotInCatalog(err error) bool {
	var target *AddonNotInCatalogError
	return errors.As(err, &target)
}

// requireAddonsInCatalog rejects any of the supplied addon names that are not
// present in the catalog. Returns:
//   - a plain error when the catalog cannot be read/parsed (→ surface as 502),
//   - *AddonNotInCatalogError listing every missing name (→ 4xx) when the
//     catalog reads fine but one or more names are absent,
//   - nil when every name is present.
//
// The returned entries slice lets callers that ALSO need the parsed catalog
// (EnableAddon generates values from it) avoid a second read.
func (o *Orchestrator) requireAddonsInCatalog(ctx context.Context, addonNames []string) ([]models.AddonCatalogEntry, error) {
	catalogPath := o.paths.Catalog
	if catalogPath == "" {
		catalogPath = "configuration/addons-catalog.yaml"
	}
	data, err := o.git.GetFileContent(ctx, catalogPath, o.gitops.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("reading addon catalog %q: %w", catalogPath, err)
	}
	entries, err := parseAddonsCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("parsing addon catalog %q: %w", catalogPath, err)
	}

	present := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		present[e.Name] = struct{}{}
	}

	var missing []string
	for _, name := range addonNames {
		if name == "" {
			continue
		}
		if _, ok := present[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing) // deterministic message ordering
		return entries, &AddonNotInCatalogError{Addons: missing}
	}
	return entries, nil
}

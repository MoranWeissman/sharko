// Package gitops — catalog-side mutators.
//
// Catalog-side addon-catalog.yaml mutators use parse-mutate-marshal
// via config.NewParser().ParseAddonsCatalog + config.MarshalAddonCatalog
// (the envelope reader/writer). The envelope sits list items under
// `spec.applicationsets:` at indent 4 — line-level scanners would
// produce silently broken output, so parse-mutate-marshal is the only
// safe choice.
//
// Trade-off: these mutators do not preserve inline comments, blank-line
// separators, or original key ordering inside catalog entries — yaml.v3
// emits canonical formatting. The schema header
// (config.AddonCatalogSchemaHeader) is always preserved as line 1
// because MarshalAddonCatalog prepends it on every emit.
//
// Behavioural contract (matches the legacy mutators where callers depend
// on it):
//
//   - AddCatalogEntry returns an error on duplicate name (callers in
//     internal/orchestrator/addon.go surface this as a user-visible
//     "addon already exists" error — no silent-skip retry path exists
//     here, unlike the cluster mutator).
//   - RemoveCatalogEntry returns an error when the addon is not found
//     (caller contract preserved — internal/orchestrator/addon.go wraps
//     it as "removing addon %q from catalog").
//   - UpdateCatalogEntry returns an error when the addon is not found
//     and rejects updates to the "name" key (caller contract preserved).
//     String-shaped updates parse into the typed AddonCatalogEntry
//     fields for the well-known keys (version, chart, repoURL,
//     namespace, syncWave, selfHeal); unknown keys are rejected so the
//     caller cannot smuggle arbitrary YAML through this entry point.
//   - UpdateCatalogVersion returns an error when the addon is not found
//     (caller contract preserved — orchestrator/upgrade.go and
//     ai/tools_write.go both surface errors as user-visible).
//
// Output bytes always carry the envelope (canonical MarshalAddonCatalog
// emission). Legacy bare-YAML inputs are accepted on read (back-compat
// per ParseAddonsCatalog contract) and silently upgraded to the
// envelope on the next emit.
package gitops

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// catalogParser is package-level — config.Parser is stateless so a shared
// instance is fine and avoids a per-call allocation.
var catalogParser = config.NewParser()

// loadCatalogOrBootstrap parses an existing addon-catalog.yaml body or
// returns an empty entries slice when the body is empty / whitespace-only.
// Mirrors loadOrBootstrap in yaml_mutator_cluster.go.
func loadCatalogOrBootstrap(data []byte) ([]models.AddonCatalogEntry, error) {
	if len(trimSpace(data)) == 0 {
		return []models.AddonCatalogEntry{}, nil
	}
	return catalogParser.ParseAddonsCatalog(data)
}

// AddCatalogEntry appends a new entry to the addon-catalog spec. Returns
// an error if an entry with the same name already exists (the caller in
// internal/orchestrator/addon.go surfaces the duplicate as a user-visible
// error). Returns the canonical enveloped document.
func AddCatalogEntry(data []byte, entry CatalogEntryInput) ([]byte, error) {
	entries, err := loadCatalogOrBootstrap(data)
	if err != nil {
		return nil, fmt.Errorf("AddCatalogEntry: %w", err)
	}

	for _, e := range entries {
		if e.Name == entry.Name {
			return nil, fmt.Errorf("addon %q already exists in catalog", entry.Name)
		}
	}

	newEntry := models.AddonCatalogEntry{
		Name:      entry.Name,
		RepoURL:   entry.RepoURL,
		Chart:     entry.Chart,
		Version:   entry.Version,
		Namespace: entry.Namespace,
		SyncWave:  entry.SyncWave,
	}
	if len(entry.DependsOn) > 0 {
		newEntry.DependsOn = append([]string(nil), entry.DependsOn...)
	}

	entries = append(entries, newEntry)
	return config.MarshalAddonCatalog("", entries)
}

// RemoveCatalogEntry removes the entry whose name matches addonName.
// Returns an error when the addon is not found (caller contract
// preserved).
func RemoveCatalogEntry(data []byte, addonName string) ([]byte, error) {
	entries, err := catalogParser.ParseAddonsCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("RemoveCatalogEntry: %w", err)
	}

	filtered := entries[:0]
	found := false
	for _, e := range entries {
		if e.Name == addonName {
			found = true
			continue
		}
		filtered = append(filtered, e)
	}
	if !found {
		return nil, fmt.Errorf("addon %q not found in catalog", addonName)
	}

	return config.MarshalAddonCatalog("", filtered)
}

// UpdateCatalogEntry applies the supplied field updates to the entry
// whose name matches addonName. Rejects updates to the "name" key.
// Returns an error when the addon is not found or when an unknown key
// is supplied (the typed model exposes a fixed field set — silently
// dropping unknown keys would mask caller bugs).
//
// Supported keys: version, chart, repoURL, namespace, syncWave, selfHeal.
// All values are strings (matching the legacy line-level contract); int
// and bool keys are parsed via strconv.
func UpdateCatalogEntry(data []byte, addonName string, updates map[string]string) ([]byte, error) {
	if _, ok := updates["name"]; ok {
		return nil, fmt.Errorf("updating name is not allowed")
	}

	entries, err := catalogParser.ParseAddonsCatalog(data)
	if err != nil {
		return nil, fmt.Errorf("UpdateCatalogEntry: %w", err)
	}

	// Iterate keys in sorted order so error reporting is deterministic
	// when multiple bad keys are supplied (the first sorted bad key wins).
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	found := false
	for i := range entries {
		if entries[i].Name != addonName {
			continue
		}
		found = true
		for _, k := range keys {
			if err := applyCatalogFieldUpdate(&entries[i], k, updates[k]); err != nil {
				return nil, fmt.Errorf("UpdateCatalogEntry: %w", err)
			}
		}
		break
	}
	if !found {
		return nil, fmt.Errorf("addon %q not found in catalog", addonName)
	}

	return config.MarshalAddonCatalog("", entries)
}

// applyCatalogFieldUpdate sets a single field on the entry. Returns an
// error when the key is unknown or when an int/bool parse fails.
func applyCatalogFieldUpdate(entry *models.AddonCatalogEntry, key, value string) error {
	switch key {
	case "version":
		entry.Version = value
	case "chart":
		entry.Chart = value
	case "repoURL":
		entry.RepoURL = value
	case "namespace":
		entry.Namespace = value
	case "syncWave":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("syncWave: %w", err)
		}
		entry.SyncWave = n
	case "selfHeal":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("selfHeal: %w", err)
		}
		entry.SelfHeal = &b
	default:
		return fmt.Errorf("unsupported field %q (supported: version, chart, repoURL, namespace, syncWave, selfHeal)", key)
	}
	return nil
}

// UpdateCatalogVersion updates the version field for the named addon.
// Thin wrapper around UpdateCatalogEntry for the common "version bump"
// case; kept as a separate symbol because callers in
// orchestrator/upgrade.go and ai/tools_write.go reference it by name.
func UpdateCatalogVersion(data []byte, addonName, newVersion string) ([]byte, error) {
	return UpdateCatalogEntry(data, addonName, map[string]string{"version": newVersion})
}

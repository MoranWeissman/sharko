// Package orchestrator — the v4 "sharpened pipeline": semantic validation
// before any git write, plus the v4-format enable/disable ops (v4 Wave 1
// Story 4.3).
//
// docs/design/2026-07-30-v4-data-file-format.md §2.1/§2.2/§2.3 define the
// v4 write targets this file produces:
//
//   - clusters/<cluster>.yaml (kind ClusterAddons) — enabled flag,
//     the per-cluster version pin, per-cluster settings.
//   - values/global/<addon>.yaml and values/clusters/<cluster>/<addon>.yaml
//     — plain Helm values, no envelope.
//
// "Sharpened" is the AC's word for the thing v3 never did: semantic
// validation runs BEFORE any branch or PR exists. A missing required
// value or an undeclared secret is a *V4SemanticValidationError, in plain
// English, naming exactly what's missing — never a half-open PR the user
// has to notice is broken.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/models"
)

// V4SemanticValidationError is returned by EnableAddonV4 (and reused by
// any future v4 write path) when the merged catalog entry's requirements
// are not met. Problems is always non-empty and each entry is a complete,
// plain-English sentence — the API handler renders the list verbatim,
// with no further formatting.
type V4SemanticValidationError struct {
	Cluster  string
	Addon    string
	Problems []string
}

func (e *V4SemanticValidationError) Error() string {
	return fmt.Sprintf("cannot enable %s on %s: %s", e.Addon, e.Cluster, strings.Join(e.Problems, "; "))
}

// ErrV4AddonNotInCatalog marks an EnableAddonV4/DisableAddonV4 request
// naming an addon that is not in the caller's merged catalog (curated +
// catalog/addons.yaml delta). The API layer (internal/api/addon_ops_v4.go)
// maps this to 422 — the request names something that does not exist,
// distinct from the 502 "upstream failed" default (Wave 2 ride-along
// w2-q6 item 2).
var ErrV4AddonNotInCatalog = errors.New("addon not in catalog")

// ErrV4ClusterNotFound marks a v4 write naming a cluster that is not
// registered — absent from both fleet/connections.yaml (the v4 cluster
// registry, design doc §2.4) and clusters/<name>.yaml. The API layer maps
// this to 404 (Wave 2 ride-along w2-q6 items 2 and 6): EnableAddonV4/
// DisableAddonV4 must refuse BEFORE any git write rather than silently
// bootstrapping a brand-new clusters/<name>.yaml for a cluster nobody
// registered.
var ErrV4ClusterNotFound = errors.New("cluster not found")

// v4ClusterExists reports whether clusterName is a known v4 cluster —
// present in fleet/connections.yaml (the registry every RegisterCluster/
// AdoptClusters v4 write populates) or already has a clusters/<name>.yaml
// assignment file. Checking both means a cluster that has never had an
// addon touched (no assignment file yet, connections-only) still passes,
// while a name that is not registered anywhere gets refused before any
// write. Read-only — safe to call before any git mutation.
func (o *Orchestrator) v4ClusterExists(ctx context.Context, clusterName string) bool {
	if data, ok := o.readFileIfExists(ctx, V4ConnectionsPath); ok && len(data) > 0 {
		if spec, err := models.LoadManagedClusters(data); err == nil {
			for _, c := range spec.Clusters {
				if c.Name == clusterName {
					return true
				}
			}
		}
	}
	if clusterPath, err := v4ClusterAddonsPath(clusterName); err == nil {
		if _, exists := o.readFileIfExists(ctx, clusterPath); exists {
			return true
		}
	}
	return false
}

// EnableAddonV4Request is the input for EnableAddonV4.
type EnableAddonV4Request struct {
	Cluster string `json:"cluster"`
	Addon   string `json:"addon"`

	// Version pins the chart version for THIS cluster only (design doc
	// §2.1). nil means "don't change the pin" — the field is a pointer so
	// enabling an already-enabled addon without mentioning a version
	// never accidentally clears an existing pin. An explicit empty
	// string clears the pin (follow the catalog default again).
	Version *string `json:"version,omitempty"`

	// Values is deep-merged onto any EXISTING
	// values/clusters/<cluster>/<addon>.yaml content and written back —
	// never a wholesale replace, so a second enable call that only sets
	// one more key doesn't clobber values a person hand-edited in
	// between. Nil/empty means "don't touch the per-cluster values file
	// at all".
	Values map[string]interface{} `json:"values,omitempty"`

	// Settings, when non-nil, replaces the cluster's per-Application
	// settings block wholesale (design doc §3.3 — list fields replace,
	// never merge). Nil leaves any existing settings block untouched.
	Settings *models.ClusterAddonsAddonSettings `json:"settings,omitempty"`

	DryRun bool `json:"dry_run,omitempty"`
	Yes    bool `json:"yes"`
	// AutoMerge mirrors every other v4/v3 write's per-request override.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// DisableAddonV4Request is the input for DisableAddonV4.
type DisableAddonV4Request struct {
	Cluster string `json:"cluster"`
	Addon   string `json:"addon"`
	// Remove, when true, deletes the addon's entry from clusters/<cluster>.yaml
	// entirely instead of keeping it with enabled=false. Design doc §2.1
	// recommends keeping the entry (a one-word re-enable later); Remove
	// exists for the rare "actually forget this addon on this cluster"
	// case.
	Remove    bool  `json:"remove,omitempty"`
	DryRun    bool  `json:"dry_run,omitempty"`
	Yes       bool  `json:"yes"`
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// mergedAddonForV4 reads the caller's catalog/addons.yaml delta and
// merges it against the wired-in curated catalog (o.curated — nil-safe),
// returning the single merged entry for addonName. Propagates
// *catalog.MissingRequiredFieldError verbatim when an internal addon in
// the delta is missing repoURL/chart/version — that error already names
// the addon and the field in plain English, so no re-wrapping is needed.
func (o *Orchestrator) mergedAddonForV4(ctx context.Context, addonName string) (catalog.MergedAddon, error) {
	delta, err := o.readCatalogDelta(ctx)
	if err != nil {
		return catalog.MergedAddon{}, fmt.Errorf("reading catalog delta: %w", err)
	}
	merged, err := catalog.MergeDelta(o.curated, delta)
	if err != nil {
		return catalog.MergedAddon{}, err
	}
	entry, ok := merged[addonName]
	if !ok {
		return catalog.MergedAddon{}, fmt.Errorf(
			"%w: addon %q is not in the catalog — add it to your catalog/addons.yaml first (via the internal-addon API) or check the spelling",
			ErrV4AddonNotInCatalog, addonName,
		)
	}
	return entry, nil
}

// validateV4AddonInputs is the "sharpened pipeline" semantic-validation
// gate (Story 4.3 AC): every RequiredValue the merged catalog entry
// declares must resolve to a non-empty value somewhere across the merged
// values layers, and every declared secret must be one Sharko has an
// AddonSecretDefinition for (o.secretDefs — the one thing Sharko can
// check without live cluster access; whether the SECRET VALUE actually
// exists in the secrets backend is a runtime concern EnableAddon's
// pre-flight credentials gate already owns, not this static check).
//
// v4 wave 2 w2-q4 ("needed-secrets gate") split what happens on a missing
// secret by SecretRequirement.EffectiveRequiredFor(): a missing
// "install"-classified secret is still a blocking problem (unchanged
// behavior — this is the default for any entry that predates the split),
// but a missing "runtime"-classified secret is downgraded to a
// non-blocking, plain-English warning — the addon installs; the warning
// just flags that the secret will matter later. Returns problems (each a
// complete sentence — blocks the enable when non-empty) and warnings
// (each a complete sentence — never blocks).
func (o *Orchestrator) validateV4AddonInputs(addon catalog.MergedAddon, mergedValues map[string]interface{}) (problems []string, warnings []string) {
	for _, rv := range addon.RequiredValues {
		val, ok := lookupDottedValue(mergedValues, rv.Key)
		if !ok || isEmptyYAMLValue(val) {
			msg := fmt.Sprintf("missing required value %q", rv.Key)
			if rv.Description != "" {
				msg += " — " + rv.Description
			}
			problems = append(problems, msg)
		}
	}

	for _, sr := range addon.Secrets {
		if _, declared := o.secretDefs[addon.Name]; declared {
			continue
		}
		if sr.EffectiveRequiredFor() == catalog.SecretRequiredForRuntime {
			msg := fmt.Sprintf("heads-up: %s will need the secret %q later", addon.Name, sr.Name)
			if sr.Description != "" {
				msg += fmt.Sprintf(" (%s)", sr.Description)
			}
			msg += " — nothing is required to install it now"
			warnings = append(warnings, msg)
			continue
		}
		msg := fmt.Sprintf("needed secret %q is not declared for %s — no secret definition is configured for this addon", sr.Name, addon.Name)
		if sr.Description != "" {
			msg += " (" + sr.Description + ")"
		}
		problems = append(problems, msg)
	}

	return problems, warnings
}

// EnableAddonV4 is the v4-format counterpart to EnableAddon (v4 Wave 1
// Story 4.3). Unlike the v3 path (labels + a combined per-cluster values
// stub), this writes:
//
//   - clusters/<cluster>.yaml  — the addon's enabled flag, version pin,
//     and settings (gitops.SetClusterAddonsAddon).
//   - values/clusters/<cluster>/<addon>.yaml — ONLY when req.Values is
//     non-empty, deep-merged onto whatever is already there.
//
// Semantic validation (required values, declared secrets) runs BEFORE any
// git write — including before the dry-run preview, so a preview never
// promises a PR that the real call would then reject. All-or-nothing: a
// validation failure or a mutator error means NOTHING is written, not
// even a branch.
func (o *Orchestrator) EnableAddonV4(ctx context.Context, req EnableAddonV4Request) (*GitResult, error) {
	if req.Cluster == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if req.Addon == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	// Resolve every commit path BEFORE the catalog read (and long before
	// any branch exists) so a name that could escape the v4 data folders
	// fails with a plain-English error and zero side effects.
	clusterPath, err := v4ClusterAddonsPath(req.Cluster)
	if err != nil {
		return nil, err
	}
	globalValuesPath, err := v4GlobalValuesPath(req.Addon)
	if err != nil {
		return nil, err
	}
	clusterValuesPath, err := v4ClusterValuesPath(req.Cluster, req.Addon)
	if err != nil {
		return nil, err
	}

	// Refuse a cluster nobody registered BEFORE any git write — without
	// this, SetClusterAddonsAddon happily bootstraps a brand-new
	// clusters/<name>.yaml for a typo'd or never-registered cluster name
	// (Wave 2 ride-along w2-q6 item 6). Runs AFTER the path-safety checks
	// above (so a traversal name still fails with "invalid cluster name",
	// not a generic not-found) and is read-only, so it costs nothing on
	// the (overwhelmingly common) already-registered path.
	if !o.v4ClusterExists(ctx, req.Cluster) {
		return nil, fmt.Errorf("%w: %q", ErrV4ClusterNotFound, req.Cluster)
	}

	merged, err := o.mergedAddonForV4(ctx, req.Addon)
	if err != nil {
		return nil, err
	}

	// Both of these are rewrite bases — the addon assignment file and the
	// per-cluster values file are edited and committed back whole — so a
	// swallowed read error would open a pull request that wipes every OTHER
	// addon assignment (or every other value) on this cluster. Only a
	// genuinely absent file starts from nothing. globalValuesPath below is
	// read for validation only and never written back, so it stays lenient.
	existingClusterAddons, _, readErr := o.readFileForRewrite(ctx, clusterPath)
	if readErr != nil {
		return nil, readErr
	}
	existingGlobalValuesRaw, _ := o.readFileIfExists(ctx, globalValuesPath)
	existingClusterValuesRaw, _, readErr := o.readFileForRewrite(ctx, clusterValuesPath)
	if readErr != nil {
		return nil, readErr
	}

	existingGlobalValues, err := parseYAMLMap(existingGlobalValuesRaw)
	if err != nil {
		return nil, fmt.Errorf("parsing existing %s: %w", globalValuesPath, err)
	}
	existingClusterValues, err := parseYAMLMap(existingClusterValuesRaw)
	if err != nil {
		return nil, fmt.Errorf("parsing existing %s: %w", clusterValuesPath, err)
	}

	// The effective per-cluster values AFTER this write: existing file,
	// deep-merged with whatever the caller is submitting now (never a
	// wholesale replace — see EnableAddonV4Request.Values doc comment).
	finalClusterValues := deepCopyYAMLMap(existingClusterValues)
	if len(req.Values) > 0 {
		deepMergeYAMLMaps(finalClusterValues, req.Values)
	}

	// What validation checks: global ∪ (existing-or-submitted per-cluster)
	// — the same layering order the engine itself applies at render time
	// (design doc §4.3: global, then per-cluster, last wins).
	mergedValues := deepCopyYAMLMap(existingGlobalValues)
	deepMergeYAMLMaps(mergedValues, finalClusterValues)

	problems, warnings := o.validateV4AddonInputs(merged, mergedValues)
	if len(problems) > 0 {
		return nil, &V4SemanticValidationError{Cluster: req.Cluster, Addon: req.Addon, Problems: problems}
	}

	updatedClusterAddons, err := gitops.SetClusterAddonsAddon(existingClusterAddons, req.Cluster, req.Addon, true, req.Version, req.Settings)
	if err != nil {
		return nil, fmt.Errorf("updating %s: %w", clusterPath, err)
	}

	writeClusterValues := len(req.Values) > 0
	var updatedClusterValuesRaw []byte
	if writeClusterValues {
		updatedClusterValuesRaw, err = marshalYAMLMap(finalClusterValues)
		if err != nil {
			return nil, fmt.Errorf("rendering %s: %w", clusterValuesPath, err)
		}
	}

	title := fmt.Sprintf("Enable %s on cluster %s", req.Addon, req.Cluster)

	if req.DryRun {
		previews := []FilePreview{{
			Path:   clusterPath,
			Action: fileActionFromExists(existingClusterAddons),
			Diff:   o.buildFileDiff(clusterPath, existingClusterAddons, updatedClusterAddons, fileActionFromExists(existingClusterAddons)),
		}}
		if writeClusterValues {
			previews = append(previews, FilePreview{
				Path:   clusterValuesPath,
				Action: fileActionFromExists(existingClusterValuesRaw),
				Diff:   o.buildFileDiff(clusterValuesPath, existingClusterValuesRaw, updatedClusterValuesRaw, fileActionFromExists(existingClusterValuesRaw)),
			})
		}
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{req.Addon},
				FilesToWrite:    previews,
				PRTitle:         fmt.Sprintf("%s enable addon %s on cluster %s", o.gitops.CommitPrefix, req.Addon, req.Cluster),
				SecretsToCreate: []string{},
			},
			Warnings: warnings,
		}, nil
	}

	if !req.Yes {
		return nil, fmt.Errorf("confirmation required: set yes: true in request body")
	}

	files := map[string][]byte{clusterPath: updatedClusterAddons}
	if writeClusterValues {
		files[clusterValuesPath] = updatedClusterValuesRaw
	}

	// OperationCode reuses the v3 literal ("addon-enable", matching
	// prtracker.OpAddonEnable's value — orchestrator cannot import
	// prtracker itself, see pr_tracker_adapter.go) rather than a
	// "-v4"-suffixed code, so this PR lands in the dashboard's existing
	// "Addons" bucket instead of the default/uncategorized one. Cluster +
	// Addon on the tracked PR already disambiguate which cluster/addon
	// this is about; the wire FORMAT (v3 labels vs v4 ClusterAddons)
	// is an implementation detail the tracker/dashboard don't need to
	// know.
	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("enable addon %s on cluster %s (v4)", req.Addon, req.Cluster),
		o.prMeta(req.AutoMerge, "addon-enable", title, req.Cluster, req.Addon))
	if err != nil {
		return nil, fmt.Errorf("committing addon enable: %w", err)
	}
	if len(warnings) > 0 {
		gitResult.Warnings = append(gitResult.Warnings, warnings...)
	}
	return gitResult, nil
}

// DisableAddonV4 is the v4-format counterpart to DisableAddon. No
// semantic validation runs — disabling never needs required values or
// secrets, it only stops deployment.
func (o *Orchestrator) DisableAddonV4(ctx context.Context, req DisableAddonV4Request) (*GitResult, error) {
	if req.Cluster == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if req.Addon == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	// Same edge guard as EnableAddonV4 — the addon name is not part of a
	// path here, but it IS written into the assignment file as a key, so
	// it goes through the identical check.
	if err := checkV4PathSegment("addon", req.Addon); err != nil {
		return nil, err
	}
	clusterPath, err := v4ClusterAddonsPath(req.Cluster)
	if err != nil {
		return nil, err
	}

	// Same refusal as EnableAddonV4 — a cluster nobody registered gets a
	// clean 404, not a confusing "nothing to disable" (Wave 2 ride-along
	// w2-q6 items 2 and 6). Runs AFTER the path-safety checks above, so a
	// traversal name still fails with "invalid cluster/addon name".
	if !o.v4ClusterExists(ctx, req.Cluster) {
		return nil, fmt.Errorf("%w: %q", ErrV4ClusterNotFound, req.Cluster)
	}

	existing, ok := o.readFileIfExists(ctx, clusterPath)
	if !ok {
		return nil, fmt.Errorf("%w: cluster %q has no assignment file at %s — nothing to disable", ErrV4ClusterNotFound, req.Cluster, clusterPath)
	}

	var updated []byte
	if req.Remove {
		updated, err = gitops.RemoveClusterAddonsAddon(existing, req.Cluster, req.Addon)
	} else {
		// version=nil: disabling must NOT clear an existing pin (design
		// doc §2.1 — "false keeps the entry — and its settings").
		updated, err = gitops.SetClusterAddonsAddon(existing, req.Cluster, req.Addon, false, nil, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("updating %s: %w", clusterPath, err)
	}

	action := "disable"
	if req.Remove {
		action = "remove"
	}
	title := fmt.Sprintf("Disable %s on cluster %s", req.Addon, req.Cluster)

	if req.DryRun {
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{},
				FilesToWrite: []FilePreview{{
					Path:   clusterPath,
					Action: "update",
					Diff:   o.buildFileDiff(clusterPath, existing, updated, "update"),
				}},
				PRTitle:         fmt.Sprintf("%s %s addon %s on cluster %s", o.gitops.CommitPrefix, action, req.Addon, req.Cluster),
				SecretsToCreate: []string{},
			},
		}, nil
	}

	if !req.Yes {
		return nil, fmt.Errorf("confirmation required: set yes: true in request body")
	}

	// Same reasoning as EnableAddonV4: reuse the v3 "addon-disable" literal
	// (prtracker.OpAddonDisable's value) so this lands in the dashboard's
	// existing "Addons" bucket.
	gitResult, err := o.commitChangesWithMeta(ctx, map[string][]byte{clusterPath: updated}, nil,
		fmt.Sprintf("%s addon %s on cluster %s (v4)", action, req.Addon, req.Cluster),
		o.prMeta(req.AutoMerge, "addon-disable", title, req.Cluster, req.Addon))
	if err != nil {
		return nil, fmt.Errorf("committing addon %s: %w", action, err)
	}
	return gitResult, nil
}

// fileActionFromExists returns "update" when existing is non-empty
// (meaning readFileIfExists found something), "create" otherwise.
func fileActionFromExists(existing []byte) string {
	if len(existing) > 0 {
		return "update"
	}
	return "create"
}

// ---- values-layer YAML helpers -------------------------------------------

// parseYAMLMap parses data as a YAML mapping. Empty/whitespace-only data
// (the ordinary "no values file yet" case) returns an empty, non-nil map.
func parseYAMLMap(data []byte) (map[string]interface{}, error) {
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]interface{}{}, nil
	}
	var m map[string]interface{}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	return m, nil
}

// marshalYAMLMap renders m back to YAML bytes, deterministically —
// yaml.v3 sorts map keys on Marshal.
func marshalYAMLMap(m map[string]interface{}) ([]byte, error) {
	return yaml.Marshal(m)
}

// deepCopyYAMLMap returns a deep copy of m so mutating the result can
// never alias the caller's map — every merge helper below assumes this.
func deepCopyYAMLMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = deepCopyYAMLValue(v)
	}
	return out
}

func deepCopyYAMLValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return deepCopyYAMLMap(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, item := range t {
			out[i] = deepCopyYAMLValue(item)
		}
		return out
	default:
		return t
	}
}

// deepMergeYAMLMaps merges src into dst in place: src wins field by
// field; when both sides have a nested map for the same key, the merge
// recurses; anything else (scalars, lists) is a straight overwrite — the
// same "list fields replace whole" rule the rest of the v4 format uses
// (design doc §3.3 decision D12), applied here to values files too, since
// Helm values commonly nest maps but rarely expect list concatenation.
func deepMergeYAMLMaps(dst, src map[string]interface{}) {
	for k, sv := range src {
		if dstMap, dstIsMap := dst[k].(map[string]interface{}); dstIsMap {
			if srcMap, srcIsMap := sv.(map[string]interface{}); srcIsMap {
				deepMergeYAMLMaps(dstMap, srcMap)
				continue
			}
		}
		dst[k] = deepCopyYAMLValue(sv)
	}
}

// lookupDottedValue resolves a dot-separated key path (e.g.
// "auth.token") against a parsed values map. Returns (nil, false) if any
// segment is missing or the path walks through a non-map value.
func lookupDottedValue(m map[string]interface{}, dotted string) (interface{}, bool) {
	segments := strings.Split(dotted, ".")
	var cur interface{} = m
	for _, seg := range segments {
		curMap, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		val, ok := curMap[seg]
		if !ok {
			return nil, false
		}
		cur = val
	}
	return cur, true
}

// isEmptyYAMLValue reports whether a resolved value should count as "not
// really provided" — nil, an empty string, or an empty map/slice. A
// present-but-zero number or explicit `false` boolean is NOT empty: those
// are meaningful values a user set on purpose.
func isEmptyYAMLValue(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	case map[string]interface{}:
		return len(t) == 0
	case []interface{}:
		return len(t) == 0
	default:
		return false
	}
}

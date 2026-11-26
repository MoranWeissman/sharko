// Package orchestrator — "upgrade this addon for all clusters" on a v4 repo.
//
// WHY THIS FILE EXISTS (pre-release walk 4). The main Upgrade button, the
// `sharko upgrade-addon` command and POST /addons/{name}/upgrade all ended in
// "operation not supported on a v4 repo". The only upgrade that worked wrote
// a version pin into each chosen cluster's file and left catalog.yaml on the
// old version — so after a fleet-wide upgrade every screen compared the
// clusters with a catalog that never moved, called them "behind", and offered
// "Downgrade".
//
// WHAT AN ALL-CLUSTERS UPGRADE IS, on a v4 repo. One pull request that:
//
//  1. sets the addon's version in catalog.yaml — the version every cluster
//     without its own pin runs;
//  2. removes the addon's own version pin from every cluster that has it
//     switched on, so "all clusters" really means all of them, and a cluster
//     that was moved ahead earlier does not stay behind on its old pin. One
//     exception: a cluster whose own pin is NEWER than the version asked
//     for keeps it, and is named in a warning — an upgrade never moves a
//     cluster down;
//  3. moves the settings in values/global/<addon>.yaml that are still the old
//     chart's defaults to the new chart's defaults (values_follow_defaults.go)
//     — only in a file Sharko generated, and never a setting a person changed.
//
// A cluster where the addon is switched off is not touched.
//
// The one-cluster and some-clusters upgrade stays UpgradeAddonClustersV4
// (upgrade_v4.go): it writes a pin into those clusters' files and leaves the
// catalog alone, which is what "move these clusters ahead of the rest" means.
package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// ErrUpgradeNothingToDo marks an upgrade that would change nothing: the
// catalog is already on that version and no cluster has its own pin. A
// 400-class answer — there is no pull request to open.
var ErrUpgradeNothingToDo = errors.New("nothing to upgrade")

// UpgradeNothingToDoMessage is the sentence for ErrUpgradeNothingToDo.
func UpgradeNothingToDoMessage(addon, version string) string {
	return fmt.Sprintf("%s is already on version %s on every cluster. Nothing to change.", addon, version)
}

// ErrUpgradeRequestInvalid marks a request Sharko cannot act on as written
// (no version, no cluster, an addon that is not switched on there). The
// caller's mistake — a 400-class answer, never "bad gateway".
var ErrUpgradeRequestInvalid = errors.New("upgrade request cannot be done as written")

// UpgradeAddonV4Request is the input to UpgradeAddonsV4: one addon, one
// version, for all clusters.
type UpgradeAddonV4Request struct {
	Addon   string
	Version string
}

// upgradeV4State is the set of files an all-clusters upgrade works on. It is
// read once and then edited in memory, so several addons upgraded in one
// request (the batch) build on each other's edits and still end in ONE pull
// request.
type upgradeV4State struct {
	catalogOriginal []byte
	catalogBody     []byte
	spec            config.AddonCatalogSpec
	reformatted     bool

	// clusterFiles holds every cluster-addons/<cluster>.yaml, original and
	// current bytes, keyed by path.
	clusterOriginal map[string][]byte
	clusterCurrent  map[string][]byte
	clusterNames    map[string]string // path -> cluster name

	valuesOriginal map[string][]byte
	valuesCurrent  map[string][]byte

	warnings []string
	// perAddon is filled as each addon is planned; used for titles.
	planned []plannedUpgrade
}

type plannedUpgrade struct {
	addon      string
	oldVersion string
	newVersion string
	unpinned   []string // clusters whose own pin was removed
}

// UpgradeAddonsV4 upgrades one or more addons for ALL clusters in a single
// pull request. dryRun returns the exact files and diffs the pull request
// would carry and writes nothing.
func (o *Orchestrator) UpgradeAddonsV4(ctx context.Context, reqs []UpgradeAddonV4Request, dryRun bool, autoMerge *bool) (*GitResult, error) {
	if len(reqs) == 0 {
		return nil, newUserFacingError(ErrUpgradeRequestInvalid, "name at least one addon to upgrade")
	}
	sort.Slice(reqs, func(i, j int) bool { return reqs[i].Addon < reqs[j].Addon })
	seen := map[string]bool{}
	for _, r := range reqs {
		if r.Addon == "" {
			return nil, newUserFacingError(ErrUpgradeRequestInvalid, "addon name is required")
		}
		if err := checkV4PathSegment("addon", r.Addon); err != nil {
			return nil, newUserFacingError(ErrUpgradeRequestInvalid, err.Error())
		}
		if strings.TrimSpace(r.Version) == "" {
			return nil, newUserFacingError(ErrUpgradeRequestInvalid, fmt.Sprintf("a version is required for %s", r.Addon))
		}
		if seen[r.Addon] {
			return nil, newUserFacingError(ErrUpgradeRequestInvalid, fmt.Sprintf("%s is listed twice", r.Addon))
		}
		seen[r.Addon] = true
	}

	if err := o.refuseOnMixedLayout(ctx); err != nil {
		return nil, err
	}

	state, err := o.loadUpgradeV4State(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range reqs {
		if err := o.planUpgradeAddonV4(ctx, state, r.Addon, strings.TrimSpace(r.Version)); err != nil {
			return nil, err
		}
	}

	if state.reformatted {
		state.warnings = append(state.warnings, CatalogRewriteNote)
	}

	title := upgradeAllTitle(state.planned)

	// Every changed file, in a fixed order: catalog, cluster files, values.
	type change struct {
		path     string
		old, new []byte
	}
	var changes []change
	if !bytes.Equal(state.catalogOriginal, state.catalogBody) {
		changes = append(changes, change{config.AddonCatalogPath, state.catalogOriginal, state.catalogBody})
	}
	for _, p := range sortedByteMapKeys(state.clusterCurrent) {
		if !bytes.Equal(state.clusterOriginal[p], state.clusterCurrent[p]) {
			changes = append(changes, change{p, state.clusterOriginal[p], state.clusterCurrent[p]})
		}
	}
	for _, p := range sortedByteMapKeys(state.valuesCurrent) {
		if !bytes.Equal(state.valuesOriginal[p], state.valuesCurrent[p]) {
			changes = append(changes, change{p, state.valuesOriginal[p], state.valuesCurrent[p]})
		}
	}

	if dryRun {
		previews := make([]FilePreview, 0, len(changes))
		effective := make([]string, 0, len(state.planned))
		for _, c := range changes {
			previews = append(previews, FilePreview{Path: c.path, Action: "update", Diff: o.buildFileDiff(c.path, c.old, c.new, "update")})
		}
		for _, p := range state.planned {
			effective = append(effective, p.addon)
		}
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: effective,
				FilesToWrite:    previews,
				PRTitle:         o.gitops.CommitPrefix + " " + upgradeAllOperation(state.planned),
				SecretsToCreate: []string{},
			},
			Warnings: state.warnings,
		}, nil
	}

	files := make(map[string][]byte, len(changes))
	for _, c := range changes {
		files[c.path] = c.new
	}
	addonForMeta := ""
	if len(state.planned) == 1 {
		addonForMeta = state.planned[0].addon
	}
	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, upgradeAllOperation(state.planned),
		o.prMeta(autoMerge, "addon-upgrade", title, "", addonForMeta))
	if err != nil {
		return nil, fmt.Errorf("committing the upgrade to Git: %w", err)
	}
	gitResult.Warnings = append(gitResult.Warnings, state.warnings...)
	return gitResult, nil
}

// loadUpgradeV4State reads catalog.yaml and every cluster-addons file. Fail
// closed: these files are rewritten, so a read that fails stops the upgrade
// rather than rewriting a file from nothing.
func (o *Orchestrator) loadUpgradeV4State(ctx context.Context) (*upgradeV4State, error) {
	existingCatalog, catalogExists, err := o.readFileForRewrite(ctx, config.AddonCatalogPath)
	if err != nil {
		return nil, err
	}
	if catalogExists && len(bytes.TrimSpace(existingCatalog)) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrCatalogFileEmpty, CatalogFileEmptyMessage)
	}
	spec, err := parseCatalogBody(existingCatalog)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
	}
	if spec.Addons == nil {
		spec.Addons = make(map[string]config.AddonCatalogEntry)
	}
	state := &upgradeV4State{
		catalogOriginal: existingCatalog,
		catalogBody:     existingCatalog,
		spec:            spec,
		clusterOriginal: map[string][]byte{},
		clusterCurrent:  map[string][]byte{},
		clusterNames:    map[string]string{},
		valuesOriginal:  map[string][]byte{},
		valuesCurrent:   map[string][]byte{},
	}

	entries, err := o.git.ListDirectory(ctx, V4ClustersDir, o.gitops.BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return state, nil
		}
		return nil, fmt.Errorf("could not list %s, so Sharko stopped before changing any version: %w", V4ClustersDir, err)
	}
	for _, name := range entries {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		p := path.Join(V4ClustersDir, name)
		data, exists, readErr := o.readFileForRewrite(ctx, p)
		if readErr != nil {
			return nil, readErr
		}
		if !exists {
			continue
		}
		state.clusterOriginal[p] = data
		state.clusterCurrent[p] = data
		state.clusterNames[p] = strings.TrimSuffix(name, ".yaml")
	}
	return state, nil
}

// planUpgradeAddonV4 applies one addon's all-clusters upgrade to state.
func (o *Orchestrator) planUpgradeAddonV4(ctx context.Context, state *upgradeV4State, addon, version string) error {
	entry, ok := state.spec.Addons[addon]
	if !ok {
		return fmt.Errorf("%w: %s is not in your catalog — add it first", ErrV4AddonNotInCatalog, addon)
	}
	oldVersion := entry.Version

	// Which clusters carry their own pin for this addon while it is on?
	//
	// A cluster whose own pin is NEWER than the version asked for keeps it.
	// "Upgrade all clusters to 6.8.0" must not quietly move a cluster that
	// somebody already put on 6.9.0 back down; that cluster is named in a
	// warning instead. Going down on purpose is still possible: the
	// some-clusters upgrade does it behind a typed confirmation.
	type pinned struct{ path, cluster string }
	var pins []pinned
	var keptAhead []string
	for _, p := range sortedByteMapKeys(state.clusterCurrent) {
		spec, err := models.LoadClusterAddons(state.clusterCurrent[p])
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		a, has := spec.Addons[addon]
		if !has || !a.Enabled || a.Version == "" {
			continue
		}
		if cmp, ok := compareChartVersions(a.Version, version); ok && cmp > 0 {
			keptAhead = append(keptAhead, fmt.Sprintf("%s (%s)", state.clusterNames[p], a.Version))
			continue
		}
		pins = append(pins, pinned{path: p, cluster: state.clusterNames[p]})
	}

	if oldVersion == version && len(pins) == 0 {
		return newUserFacingError(ErrUpgradeNothingToDo, UpgradeNothingToDoMessage(addon, version))
	}

	// The version has to exist before anything is written.
	if err := o.checkChartVersion(ctx, addon, entry.RepoURL, entry.Chart, version); err != nil {
		return err
	}

	// 1. catalog.yaml
	if oldVersion != version {
		entry.Version = version
		if err := catalog.ValidateCatalogEntry(addon, entry); err != nil {
			return err
		}
		state.spec.Addons[addon] = entry
		body, reformatted, err := writeCatalogFile(state.catalogBody, state.spec,
			map[string]config.AddonCatalogEntry{addon: entry}, []string{addon})
		if err != nil {
			return fmt.Errorf("rendering %s: %w", config.AddonCatalogPath, err)
		}
		state.catalogBody = body
		state.reformatted = state.reformatted || reformatted
	}

	// 2. every cluster's own pin goes, so the cluster follows the catalog.
	var unpinned []string
	for _, pin := range pins {
		updated, err := gitops.ClearClusterAddonsAddonVersion(state.clusterCurrent[pin.path], addon)
		if err != nil {
			return fmt.Errorf("updating %s: %w", pin.path, err)
		}
		state.clusterCurrent[pin.path] = updated
		unpinned = append(unpinned, pin.cluster)
	}

	// 3. the seeded global values file follows the chart's defaults.
	if oldVersion != version {
		o.followDefaultsInGlobalValues(ctx, state, addon, entry, oldVersion, version)
	}

	if len(keptAhead) > 0 {
		state.warnings = append(state.warnings, ClustersKeptAheadWarning(addon, version, keptAhead))
	}

	state.planned = append(state.planned, plannedUpgrade{addon: addon, oldVersion: oldVersion, newVersion: version, unpinned: unpinned})
	return nil
}

// followDefaultsInGlobalValues is step 3 of an all-clusters upgrade. It never
// fails the upgrade: when it cannot compare the two chart versions it leaves
// the file alone and says so.
func (o *Orchestrator) followDefaultsInGlobalValues(ctx context.Context, state *upgradeV4State, addon string, entry config.AddonCatalogEntry, oldVersion, newVersion string) {
	gPath, err := v4GlobalValuesPath(addon)
	if err != nil {
		return
	}
	current, exists := state.valuesCurrent[gPath]
	if !exists {
		data, found, readErr := o.readFileForRewrite(ctx, gPath)
		if readErr != nil || !found {
			return
		}
		state.valuesOriginal[gPath] = data
		state.valuesCurrent[gPath] = data
		current = data
	}
	if yamlDocRoot(current) == nil {
		// Comments only, or empty: the chart's own defaults already apply.
		return
	}
	if o.chartDefaultsFn == nil {
		return
	}

	header := ParseSmartValuesHeader(current)
	baseVersion := oldVersion
	if header.Managed && header.Version != "" {
		// The version the file was really generated from.
		baseVersion = header.Version
	}
	oldDefaults, oldErr := o.chartDefaultsFn(ctx, entry.RepoURL, entry.Chart, baseVersion)
	newDefaults, newErr := o.chartDefaultsFn(ctx, entry.RepoURL, entry.Chart, newVersion)
	if oldErr != nil || newErr != nil {
		state.warnings = append(state.warnings, ValuesNotComparedWarning(addon, gPath))
		return
	}

	if !header.Managed {
		// Not a file Sharko generated. A person wrote it, so nothing in it
		// is changed — but a setting that pins the old chart's default is
		// still worth one plain sentence.
		if kept := staleDefaultsReport(current, oldDefaults, newDefaults); len(kept) > 0 {
			state.warnings = append(state.warnings, ValuesKeptOldDefaultsWarning(gPath, kept))
		}
		return
	}

	res := followChartDefaults(current, oldDefaults, newDefaults)
	// The header line says which chart version the file's defaults match.
	updated := restampSmartValuesHeaderVersion(res.File, header, newVersion)
	if !bytes.Equal(updated, current) {
		state.valuesCurrent[gPath] = updated
	}
	if len(res.Kept) > 0 {
		state.warnings = append(state.warnings, ValuesKeptOldDefaultsWarning(gPath, res.Kept))
	}
}

// ClustersKeptAheadWarning names the clusters an all-clusters upgrade did not
// touch because they already run something newer.
func ClustersKeptAheadWarning(addon, version string, clusters []string) string {
	return fmt.Sprintf(
		"These clusters already run a newer version of %s than %s and were left as they are: %s.",
		addon, version, strings.Join(clusters, ", "))
}

// ValuesNotComparedWarning is said when the old and new chart could not be
// downloaded to compare their default settings.
func ValuesNotComparedWarning(addon, valuesPath string) string {
	return fmt.Sprintf(
		"Sharko could not compare the old and new chart of %s, so it left %s as it is. If that file still sets the old chart's image tag, the old image keeps running after this upgrade. Check the file.",
		addon, valuesPath)
}

// ValuesKeptOldDefaultsWarning names the settings that still hold the old
// chart's default after the upgrade.
func ValuesKeptOldDefaultsWarning(valuesPath string, kept []KeptDefault) string {
	const maxListed = 5
	parts := make([]string, 0, maxListed)
	for i, k := range kept {
		if i == maxListed {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s, new chart default %s)", k.Path, k.Old, k.New))
	}
	more := ""
	if len(kept) > maxListed {
		more = fmt.Sprintf(" and %d more", len(kept)-maxListed)
	}
	return fmt.Sprintf(
		"%s still sets the old chart's value for: %s%s. Sharko did not change these. They stay as they are after the upgrade until you edit that file.",
		valuesPath, strings.Join(parts, ", "), more)
}

// restampSmartValuesHeaderVersion rewrites the "Generated by Sharko from
// <chart>@<version>" line to the new version, so the file says which chart
// version its defaults now match. Only that one line changes.
func restampSmartValuesHeaderVersion(file []byte, header ParsedHeader, newVersion string) []byte {
	if header.Chart == "" || header.Version == "" {
		return file
	}
	oldRef := "# Generated by Sharko from " + header.Chart + "@" + header.Version + " on "
	newRef := "# Generated by Sharko from " + header.Chart + "@" + newVersion + " on "
	lines := strings.SplitN(string(file), "\n", 12)
	for i, l := range lines {
		if i == len(lines)-1 && len(lines) == 12 {
			break
		}
		if strings.HasPrefix(l, oldRef) {
			lines[i] = newRef + strings.TrimPrefix(l, oldRef)
			return []byte(strings.Join(lines, "\n"))
		}
	}
	return file
}

// upgradeAllTitle is the pull request title. It says "Downgrade" when that
// is what the change is, so a reviewer is not told an upgrade is coming when
// the version goes down.
func upgradeAllTitle(planned []plannedUpgrade) string {
	if len(planned) == 1 {
		p := planned[0]
		return fmt.Sprintf("%s %s to %s on all clusters", versionChangeVerb(p.oldVersion, p.newVersion), p.addon, p.newVersion)
	}
	parts := make([]string, 0, len(planned))
	for _, p := range planned {
		parts = append(parts, p.addon+" to "+p.newVersion)
	}
	return "Upgrade addons on all clusters: " + strings.Join(parts, ", ")
}

// upgradeAllOperation is the commit message and pull request title text.
// The preview and the real pull request both use it, so they always match.
func upgradeAllOperation(planned []plannedUpgrade) string {
	if len(planned) == 1 {
		p := planned[0]
		return fmt.Sprintf("%s addon %s to %s on all clusters", strings.ToLower(versionChangeVerb(p.oldVersion, p.newVersion)), p.addon, p.newVersion)
	}
	parts := make([]string, 0, len(planned))
	for _, p := range planned {
		parts = append(parts, p.addon+" to "+p.newVersion)
	}
	return "upgrade addons on all clusters: " + strings.Join(parts, ", ")
}

// versionChangeVerb is "Upgrade" unless the new version is clearly lower
// than the old one.
func versionChangeVerb(oldVersion, newVersion string) string {
	if cmp, ok := compareChartVersions(newVersion, oldVersion); ok && cmp < 0 {
		return "Downgrade"
	}
	return "Upgrade"
}

// compareChartVersions compares two chart versions by their numbers
// (major.minor.patch, a leading "v" ignored). A version with a suffix
// ("-beta.1") is lower than the same numbers without one. ok is false when
// either is not a version Sharko can read.
func compareChartVersions(a, b string) (cmp int, ok bool) {
	an, asuf, aok := splitChartVersion(a)
	bn, bsuf, bok := splitChartVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			if an[i] < bn[i] {
				return -1, true
			}
			return 1, true
		}
	}
	switch {
	case asuf == bsuf:
		return 0, true
	case asuf == "":
		return 1, true
	case bsuf == "":
		return -1, true
	case asuf < bsuf:
		return -1, true
	default:
		return 1, true
	}
}

func splitChartVersion(v string) (nums [3]int, suffix string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		if v[i] == '-' {
			suffix = v[i+1:]
			if j := strings.Index(suffix, "+"); j >= 0 {
				suffix = suffix[:j]
			}
		}
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 || parts[0] == "" {
		return nums, "", false
	}
	for i, p := range parts {
		n := 0
		if p == "" {
			return nums, "", false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return nums, "", false
			}
			n = n*10 + int(r-'0')
		}
		nums[i] = n
	}
	return nums, suffix, true
}

func sortedByteMapKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

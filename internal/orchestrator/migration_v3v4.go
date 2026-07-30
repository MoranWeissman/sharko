// Package orchestrator — the one-PR v3 → v4 migration (v4 Wave 2,
// Stories 5.1 and 5.2).
//
// One pull request converts the whole repository. Not a sequence of small
// ones, not a hand migration with a checklist: the scaffolded template
// forest comes out, the engine pin goes in, the cluster registry and every
// values file are rewritten into the v4 layout, and the full-copy catalog
// becomes a delta of just the user's own changes. Merge it and everything
// keeps running; revert that one merge and you are exactly back where you
// started.
//
// All-or-nothing is structural, not aspirational: the ENTIRE file map is
// built and validated through the real readers (schema + semantic) before
// a branch exists. Any failure returns before the first git write. A
// failure AFTER the branch exists deletes the branch again, so a retry is
// never fighting a half-finished attempt, and the base branch is never
// touched by anything but the final merge.
//
// Layout reference: docs/design/2026-07-30-v4-data-file-format.md.
package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// RepoFormatEmpty is the third answer alongside RepoFormatV3 / RepoFormatV4:
// a connected repo with neither an engine pin nor a v3 marker. Nothing to
// migrate — the first-run bootstrap is what that repo needs.
const RepoFormatEmpty = "empty"

// Migration file actions, as reported in a plan.
const (
	MigrationActionAdd     = "add"
	MigrationActionConvert = "convert"
	MigrationActionRemove  = "remove"
)

// v3ClusterGlobalValuesKey is the one top-level key in a v3 per-cluster
// values file that is NOT an addon: a scratch block for YAML anchors. The
// v3 ApplicationSet never passed it to Helm (it injects only the
// `<addon>:` sub-map), so it has no v4 equivalent and carrying it across
// would invent values nothing ever applied.
const v3ClusterGlobalValuesKey = "clusterGlobalValues"

// MigrationStatusResult is what GET /api/v1/migration/status answers.
type MigrationStatusResult struct {
	// Format is "v3", "v4", or "empty".
	Format string `json:"format"`
	// MigrationAvailable is true only for "v3" — the one state where
	// there is something to convert.
	MigrationAvailable bool `json:"migration_available"`
	// Message is a plain-English sentence the UI can show as-is.
	Message string `json:"message"`
}

// MigrationFileChange is one file the migration PR would add, convert, or
// remove. Content is the rendered body for adds and conversions, already
// through the same redaction the dry-run previews use — a values file
// never shows a secret in a preview.
type MigrationFileChange struct {
	Path     string `json:"path"`
	FromPath string `json:"from_path,omitempty"`
	Action   string `json:"action"`
	Content  string `json:"content,omitempty"`
}

// MigrationPlan is the full dry-run preview: every file the migration PR
// will touch, and plain-words notes about anything that could not be
// carried across.
type MigrationPlan struct {
	Format  string                `json:"format"`
	Add     []MigrationFileChange `json:"add"`
	Convert []MigrationFileChange `json:"convert"`
	Remove  []MigrationFileChange `json:"remove"`
	Notes   []string              `json:"notes"`
	PRTitle string                `json:"pr_title"`
}

// MigrateRequest is the input for MigrateV3ToV4. DryRun/Yes follow the
// same confirmation convention every other v4 write uses (see
// EnableAddonV4Request): dry_run returns the plan with zero side effects,
// and a real run needs yes: true.
type MigrateRequest struct {
	DryRun    bool  `json:"dry_run,omitempty"`
	Yes       bool  `json:"yes"`
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// MigrateResult is the output of MigrateV3ToV4.
type MigrateResult struct {
	// Status is "migrated", "preview", or "already_migrated".
	Status string `json:"status"`
	// Plan is always set for a dry run, and set on a real run too so the
	// caller can render exactly what the PR contains.
	Plan *MigrationPlan `json:"plan,omitempty"`
	Git  *GitResult     `json:"git,omitempty"`
}

// migrationBuild is the assembled, validated migration: the complete file
// map, the delete list, and the notes — everything the PR needs, computed
// before any git write.
type migrationBuild struct {
	files   map[string][]byte
	deletes []string
	// convertedFrom maps a NEW path to the OLD path it came from, so the
	// preview can say "configuration/addons-catalog.yaml → catalog/addons.yaml"
	// instead of showing an unexplained new file.
	convertedFrom map[string]string
	notes         []string
}

// MigrationStatus reports which data-file format the connected repo uses
// and whether the one-PR migration is available. Read-only; never errors
// on a missing file — an unreachable repo is the caller's own connection
// error, surfaced by whatever they used to reach us.
func (o *Orchestrator) MigrationStatus(ctx context.Context) *MigrationStatusResult {
	switch {
	case o.isV4Repo(ctx):
		return &MigrationStatusResult{
			Format:             RepoFormatV4,
			MigrationAvailable: false,
			Message:            "this repo already uses the current format — nothing to migrate",
		}
	case o.hasV3Markers(ctx):
		return &MigrationStatusResult{
			Format:             RepoFormatV3,
			MigrationAvailable: true,
			Message:            "v3 format — migration available: one pull request moves this repo across, and everything keeps running",
		}
	default:
		return &MigrationStatusResult{
			Format:             RepoFormatEmpty,
			MigrationAvailable: false,
			Message:            "this repo has not been set up yet — initialize it instead of migrating",
		}
	}
}

// PreviewMigration returns the full dry-run plan for a v3 repo: every file
// the migration PR would add, convert, or remove, with the rendered
// content of everything it writes. Zero side effects.
func (o *Orchestrator) PreviewMigration(ctx context.Context) (*MigrationPlan, error) {
	status := o.MigrationStatus(ctx)
	if !status.MigrationAvailable {
		return &MigrationPlan{Format: status.Format, Notes: []string{status.Message}}, nil
	}
	build, err := o.buildMigration(ctx)
	if err != nil {
		return nil, err
	}
	return o.planFromBuild(build), nil
}

// MigrateV3ToV4 converts the whole repo in ONE pull request.
//
// dry_run returns the plan and nothing else. A real run requires
// yes: true, matching every other v4 write. On an already-v4 repo it is a
// clean no-op ("already_migrated"), never an error — re-running a
// migration must be safe, because a person who is not sure whether it
// worked will run it again.
func (o *Orchestrator) MigrateV3ToV4(ctx context.Context, req MigrateRequest) (*MigrateResult, error) {
	status := o.MigrationStatus(ctx)
	if status.Format == RepoFormatV4 {
		return &MigrateResult{
			Status: "already_migrated",
			Plan:   &MigrationPlan{Format: RepoFormatV4, Notes: []string{status.Message}},
		}, nil
	}
	if status.Format == RepoFormatEmpty {
		return nil, errors.New(status.Message)
	}

	// Build and validate EVERYTHING first. Every error path below this
	// point and above commitMigrationPR leaves the repo untouched.
	build, err := o.buildMigration(ctx)
	if err != nil {
		return nil, err
	}
	plan := o.planFromBuild(build)

	if req.DryRun {
		return &MigrateResult{Status: "preview", Plan: plan}, nil
	}
	if !req.Yes {
		return nil, fmt.Errorf("confirmation required: set yes: true in request body")
	}

	gitResult, err := o.commitMigrationPR(ctx, build, req.AutoMerge)
	if err != nil {
		return nil, err
	}
	return &MigrateResult{Status: "migrated", Plan: plan, Git: gitResult}, nil
}

// ─── building the migration ──────────────────────────────────────────────

// buildMigration assembles the complete, validated file map. It performs
// only READS against git; nothing here can leave a mark on the repo.
func (o *Orchestrator) buildMigration(ctx context.Context) (*migrationBuild, error) {
	paths := newV3DataPathSet(o.paths)

	b := &migrationBuild{
		files:         map[string][]byte{},
		convertedFrom: map[string]string{},
	}

	// 1 — the cluster registry. A v3 repo recognised by bootstrap/Chart.yaml
	// alone may have no clusters yet; that is an empty fleet, not an error.
	clusters, err := o.readV3Clusters(ctx, paths.managedClusters)
	if err != nil {
		return nil, err
	}

	// 2 — the catalog. Same tolerance: a bootstrapped repo with no addons
	// added yet has an empty (or absent) catalog.
	v3Catalog, err := o.readV3Catalog(ctx, paths.catalog)
	if err != nil {
		return nil, err
	}
	catalogNames := make(map[string]bool, len(v3Catalog))
	for _, e := range v3Catalog {
		catalogNames[e.Name] = true
	}

	// 3 — catalog/addons.yaml (the delta).
	deltaSpec, catalogNotes := buildCatalogDelta(v3Catalog, o.curated)
	b.notes = append(b.notes, catalogNotes...)
	deltaBody, err := config.SaveAddonCatalogDelta(deltaSpec)
	if err != nil {
		return nil, fmt.Errorf("building %s: %w", config.AddonCatalogDeltaPath, err)
	}
	b.files[config.AddonCatalogDeltaPath] = deltaBody
	b.convertedFrom[config.AddonCatalogDeltaPath] = paths.catalog

	// Semantic gate: the delta must merge cleanly against the shipped
	// catalog. A user-added addon missing repoURL/chart/version fails HERE,
	// in plain English, rather than after the PR merges and the engine
	// cannot render it.
	if _, mergeErr := catalog.MergeDelta(o.curated, deltaSpec); mergeErr != nil {
		return nil, fmt.Errorf("the new catalog file would not be usable: %w", mergeErr)
	}

	// 4 — fleet/connections.yaml + one clusters/<name>.yaml per cluster.
	if err := o.buildV4ClusterFiles(b, clusters, catalogNames); err != nil {
		return nil, err
	}

	// 5 — values. Per-cluster first (driven by the cluster list, never by a
	// directory listing — an example file left in that folder is NOT a
	// cluster), then the fleet-wide ones.
	if err := o.buildV4ClusterValues(ctx, b, paths, clusters, catalogNames); err != nil {
		return nil, err
	}
	if err := o.buildV4GlobalValues(ctx, b, paths, catalogNames); err != nil {
		return nil, err
	}

	// 6 — the engine pin and README, from the SAME generator the first-run
	// bootstrap uses, so a migrated repo and a freshly seeded one pin the
	// identical engine version.
	o.addSeedFiles(b)

	// 7 — everything that comes out.
	if err := o.collectMigrationDeletes(ctx, b, paths, clusters); err != nil {
		return nil, err
	}

	// 8 — validate every generated file through the real readers.
	if err := validateMigrationFiles(b.files); err != nil {
		return nil, err
	}

	return b, nil
}

// readV3Clusters reads and parses the v3 cluster registry. A missing file
// is an empty fleet, not an error.
func (o *Orchestrator) readV3Clusters(ctx context.Context, registryPath string) ([]models.ManagedClusterEntry, error) {
	body, ok := o.readFileIfExists(ctx, registryPath)
	if !ok || len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	spec, err := models.LoadManagedClusters(body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", registryPath, err)
	}
	return spec.Clusters, nil
}

// readV3Catalog reads and parses the v3 full-copy catalog. A missing file
// is an empty catalog, not an error.
func (o *Orchestrator) readV3Catalog(ctx context.Context, catalogPath string) ([]models.AddonCatalogEntry, error) {
	body, ok := o.readFileIfExists(ctx, catalogPath)
	if !ok || len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	entries, err := parseAddonsCatalog(body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", catalogPath, err)
	}
	return entries, nil
}

// buildV4ClusterFiles writes fleet/connections.yaml and one
// clusters/<name>.yaml per cluster.
//
// The split is the heart of the format change (design doc §2.4): the
// connection record keeps HOW Sharko reaches a cluster, and the assignment
// file takes over WHICH addons run there. So every addon key (and its
// `<addon>-version` companion) is lifted out of the connection record's
// labels and rewritten as a ClusterAddons entry; any other label a person
// put there — env, region, team — is carried across untouched.
func (o *Orchestrator) buildV4ClusterFiles(b *migrationBuild, clusters []models.ManagedClusterEntry, catalogNames map[string]bool) error {
	converted := make([]models.ManagedClusterEntry, 0, len(clusters))

	for _, entry := range clusters {
		if err := checkV4PathSegment("cluster", entry.Name); err != nil {
			return fmt.Errorf("cannot migrate this repo: %w", err)
		}

		assignment := models.ClusterAddonsSpec{
			Cluster: entry.Name,
			Addons:  map[string]models.ClusterAddonsAddon{},
		}
		keptLabels := models.ClusterLabels{}

		for key, value := range entry.Labels {
			addon, isVersionKey := strings.CutSuffix(key, "-version")
			if isVersionKey && catalogNames[addon] {
				continue // folded into the addon's entry below
			}
			if !catalogNames[key] {
				keptLabels[key] = value
				continue
			}
			assignment.Addons[key] = models.ClusterAddonsAddon{
				// Only the literal "enabled" ever meant on — the same
				// predicate the v3 ApplicationSet selector applied — so
				// "disabled", a legacy "true", and anything else all
				// migrate to enabled:false. The entry is KEPT either way
				// (design doc §2.1): turning it back on later is a
				// one-word change.
				Enabled: models.AddonLabelEnabled(value),
				Version: entry.Labels[key+"-version"],
			}
		}

		body, err := models.SaveClusterAddons(assignment)
		if err != nil {
			return fmt.Errorf("building the assignment file for cluster %q: %w", entry.Name, err)
		}
		clusterPath, err := v4ClusterAddonsPath(entry.Name)
		if err != nil {
			return err
		}
		b.files[clusterPath] = body

		// The invariant this whole story turns on: the set of addons
		// running on this cluster must be IDENTICAL either side of the
		// migration. Checked here, at build time, so a mismatch is a
		// refusal with nothing written rather than a surprise after merge.
		if err := assertAddonSetUnchanged(entry, assignment, catalogNames); err != nil {
			return err
		}

		clean := entry
		clean.Labels = keptLabels
		converted = append(converted, clean)
	}

	connectionsBody, err := models.SaveManagedClusters(models.ManagedClustersSpec{Clusters: converted})
	if err != nil {
		return fmt.Errorf("building %s: %w", V4ConnectionsPath, err)
	}
	b.files[V4ConnectionsPath] = connectionsBody
	b.convertedFrom[V4ConnectionsPath] = o.v3RegistryPath()
	return nil
}

// v3RegistryPath is the configured (or default) v3 cluster-registry path.
func (o *Orchestrator) v3RegistryPath() string {
	return newV3DataPathSet(o.paths).managedClusters
}

// assertAddonSetUnchanged compares the addons a cluster runs BEFORE the
// migration (v3 bare `<addon>: enabled` labels, filtered to the catalog —
// the v3 ApplicationSet only ever existed for catalog entries) against the
// addons it runs AFTER (the v4 `addons.sharko.dev/<addon>: enabled` labels
// the reconciler derives from the assignment file). Any difference is a
// hard error: an addon quietly appearing or disappearing across a
// migration is the one outcome this story cannot ship.
func assertAddonSetUnchanged(v3 models.ManagedClusterEntry, v4 models.ClusterAddonsSpec, catalogNames map[string]bool) error {
	before := map[string]bool{}
	for key, value := range v3.Labels {
		if catalogNames[key] && models.AddonLabelEnabled(value) {
			before[key] = true
		}
	}
	after := map[string]bool{}
	for addon, entry := range v4.Addons {
		if entry.Enabled && models.IsValidResourceName(addon) {
			after[addon] = true
		}
	}

	var added, removed []string
	for addon := range after {
		if !before[addon] {
			added = append(added, addon)
		}
	}
	for addon := range before {
		if !after[addon] {
			removed = append(removed, addon)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	sort.Strings(added)
	sort.Strings(removed)
	return fmt.Errorf(
		"refusing to migrate: cluster %q would end up running a different set of addons (would start: %s; would stop: %s). Nothing was written",
		v3.Name, listOrNone(added), listOrNone(removed))
}

func listOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

// buildV4ClusterValues splits each v3 per-cluster values file into one
// plain Helm values file per addon.
//
// The v3 file held every addon's per-cluster values in one document, keyed
// by addon name, and the ApplicationSet injected just the `<addon>:`
// sub-map into Helm. So the sub-map IS the values, and carrying it across
// verbatim is what makes "everything keeps running" true.
func (o *Orchestrator) buildV4ClusterValues(ctx context.Context, b *migrationBuild, paths v3DataPathSet, clusters []models.ManagedClusterEntry, catalogNames map[string]bool) error {
	for _, cluster := range clusters {
		oldPath := path.Join(paths.clusterValues, cluster.Name+".yaml")
		body, ok := o.readFileIfExists(ctx, oldPath)
		if !ok || len(strings.TrimSpace(string(body))) == 0 {
			continue
		}
		doc, err := parseYAMLMap(body)
		if err != nil {
			return fmt.Errorf("reading %s: %w", oldPath, err)
		}

		for _, addon := range sortedKeys(doc) {
			if addon == v3ClusterGlobalValuesKey {
				if !isEmptyYAMLValue(doc[addon]) {
					b.notes = append(b.notes, fmt.Sprintf(
						"%s had a %q block. Nothing ever read it (it was a scratch area for YAML shortcuts), so it is not carried over",
						oldPath, v3ClusterGlobalValuesKey))
				}
				continue
			}
			if !catalogNames[addon] {
				b.notes = append(b.notes, fmt.Sprintf(
					"%s had values for %q, which is not in your addon list — those values were not deploying anything, so they are not carried over",
					oldPath, addon))
				continue
			}
			values, isMap := doc[addon].(map[string]interface{})
			if !isMap {
				b.notes = append(b.notes, fmt.Sprintf(
					"%s had a %q entry that is not a block of settings — it is not carried over", oldPath, addon))
				continue
			}
			if len(values) == 0 {
				continue
			}
			newPath, err := v4ClusterValuesPath(cluster.Name, addon)
			if err != nil {
				return fmt.Errorf("cannot migrate this repo: %w", err)
			}
			rendered, err := marshalYAMLMap(values)
			if err != nil {
				return fmt.Errorf("rewriting the values for %s on %s: %w", addon, cluster.Name, err)
			}
			b.files[newPath] = rendered
			b.convertedFrom[newPath] = oldPath
		}
	}
	return nil
}

// buildV4GlobalValues moves each fleet-wide values file to its v4 home.
//
// EVERY file in the folder moves, including ones whose addon is not in the
// catalog — a person may have written values for an addon they are about to
// add back, and losing hand-written values is not a tidy-up, it is data
// loss. Those get a note instead.
//
// The content is carried across as-is apart from one repair: files written
// by pre-Bundle-5 Sharko wrapped the whole document under an `<addon>:`
// root key, which Helm silently ignored. UnwrapGlobalValuesFile is the
// existing, comment-preserving fixer for exactly that; running it here
// means the migration lands values that actually apply instead of copying
// a file nobody was reading.
func (o *Orchestrator) buildV4GlobalValues(ctx context.Context, b *migrationBuild, paths v3DataPathSet, catalogNames map[string]bool) error {
	names, err := o.git.ListDirectory(ctx, paths.globalValues, o.gitops.BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return nil
		}
		return fmt.Errorf("listing %s: %w", paths.globalValues, err)
	}

	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		addon := strings.TrimSuffix(name, ".yaml")
		if err := checkV4PathSegment("addon", addon); err != nil {
			b.notes = append(b.notes, fmt.Sprintf(
				"%s could not be moved: %v", path.Join(paths.globalValues, name), err))
			continue
		}
		oldPath := path.Join(paths.globalValues, name)
		body, ok := o.readFileIfExists(ctx, oldPath)
		if !ok {
			continue
		}
		unwrapped, _, unwrapErr := UnwrapGlobalValuesFile(body, addon, "")
		if unwrapErr == nil && len(unwrapped) > 0 {
			body = unwrapped
		}
		newPath, err := v4GlobalValuesPath(addon)
		if err != nil {
			return fmt.Errorf("cannot migrate this repo: %w", err)
		}
		b.files[newPath] = body
		b.convertedFrom[newPath] = oldPath

		if !catalogNames[addon] {
			b.notes = append(b.notes, fmt.Sprintf(
				"%s holds values for %q, which is not in your addon list. The file is moved to %s so nothing is lost, but it deploys nothing until you add the addon",
				oldPath, addon, newPath))
		}
	}
	return nil
}

// addSeedFiles adds the engine pin and the README from BuildV4SeedFiles —
// the same generator the first-run bootstrap uses, so the pinned engine
// version can never drift between a fresh install and a migrated one.
//
// The seed's .gitkeep placeholders are added only for folders the
// migration leaves genuinely empty. Git cannot track an empty directory;
// it also does not need a placeholder next to real files.
func (o *Orchestrator) addSeedFiles(b *migrationBuild) {
	for seedPath, content := range BuildV4SeedFiles(o.gitops, o.paths) {
		if !strings.HasSuffix(seedPath, "/.gitkeep") {
			b.files[seedPath] = content
			continue
		}
		folder := strings.TrimSuffix(seedPath, "/.gitkeep")
		if !folderHasFile(b.files, folder) {
			b.files[seedPath] = content
		}
	}
	b.convertedFrom["README.md"] = "README.md"
}

// folderHasFile reports whether any file in the map lives under folder.
func folderHasFile(files map[string][]byte, folder string) bool {
	for p := range files {
		if underDir(p, folder) {
			return true
		}
	}
	return false
}

// collectMigrationDeletes enumerates everything that comes OUT of the repo:
// the scaffolded template forest, the converted data files at their old
// paths, and anything left over inside the two v3 values folders (the
// example files the bootstrap shipped) so the folders disappear entirely.
//
// Only paths that actually exist on the base branch are listed — a repo
// Sharko adopted rather than bootstrapped never had most of the scaffold,
// and asking git to delete a file that was never there fails the whole PR.
func (o *Orchestrator) collectMigrationDeletes(ctx context.Context, b *migrationBuild, paths v3DataPathSet, clusters []models.ManagedClusterEntry) error {
	candidates := map[string]bool{}

	keep := make(map[string]bool, len(b.files))
	for p := range b.files {
		keep[p] = true
	}
	for _, p := range v3ScaffoldFilesToRemove(o.paths, keep) {
		candidates[p] = true
	}

	// The converted data files, at their old paths.
	candidates[paths.managedClusters] = true
	candidates[paths.catalog] = true
	for _, cluster := range clusters {
		candidates[path.Join(paths.clusterValues, cluster.Name+".yaml")] = true
	}

	// Whatever else is sitting in the two v3 values folders.
	for _, dir := range []string{paths.clusterValues, paths.globalValues} {
		names, err := o.git.ListDirectory(ctx, dir, o.gitops.BaseBranch)
		if err != nil {
			if errors.Is(err, gitprovider.ErrFileNotFound) {
				continue
			}
			return fmt.Errorf("listing %s: %w", dir, err)
		}
		for _, name := range names {
			candidates[path.Join(dir, name)] = true
		}
	}

	for p := range candidates {
		if keep[p] {
			continue // the migration writes this path; a delete would undo it
		}
		if _, exists := o.readFileIfExists(ctx, p); !exists {
			continue
		}
		b.deletes = append(b.deletes, p)
	}
	sort.Strings(b.deletes)
	return nil
}

// ─── validation ──────────────────────────────────────────────────────────

// validateMigrationFiles runs every generated file back through the REAL
// reader for its kind — the same functions the server and the
// validate-config CLI use, JSON Schema and all. Nothing is trusted because
// this package wrote it: a file that would fail validation after the merge
// fails here instead, before a branch exists.
func validateMigrationFiles(files map[string][]byte) error {
	for _, p := range sortedFileKeys(files) {
		body := files[p]
		var err error
		switch {
		case p == V4ConnectionsPath:
			_, err = models.LoadManagedClusters(body)
		case p == config.AddonCatalogDeltaPath:
			_, err = config.LoadAddonCatalogDelta(body)
		case underDir(p, V4ClustersDir) && strings.HasSuffix(p, ".yaml"):
			var spec models.ClusterAddonsSpec
			spec, err = models.LoadClusterAddons(body)
			if err == nil {
				want := strings.TrimSuffix(path.Base(p), ".yaml")
				if spec.Cluster != want {
					err = fmt.Errorf("names cluster %q but lives at %s", spec.Cluster, p)
				}
			}
		case underDir(p, V4GlobalValuesDir), underDir(p, V4ClusterValuesDir):
			var node yaml.Node
			err = yaml.Unmarshal(body, &node)
		}
		if err != nil {
			return fmt.Errorf("refusing to migrate: the new %s would not be valid: %w. Nothing was written", p, err)
		}
	}
	return nil
}

// ─── plan rendering ──────────────────────────────────────────────────────

// planFromBuild renders the assembled migration as the preview a person
// reads before pressing Migrate. Values-file bodies go through the same
// redaction every other dry-run preview uses, so a preview can never be
// the thing that leaks a secret.
func (o *Orchestrator) planFromBuild(b *migrationBuild) *MigrationPlan {
	plan := &MigrationPlan{
		Format:  RepoFormatV3,
		Notes:   b.notes,
		PRTitle: migrationPRTitle(o.gitops.CommitPrefix),
	}
	if plan.Notes == nil {
		plan.Notes = []string{}
	}

	for _, p := range sortedFileKeys(b.files) {
		change := MigrationFileChange{
			Path:     p,
			Action:   MigrationActionAdd,
			FromPath: b.convertedFrom[p],
			Content:  string(o.redactValuesContent(p, b.files[p])),
		}
		if change.FromPath != "" {
			change.Action = MigrationActionConvert
			plan.Convert = append(plan.Convert, change)
			continue
		}
		plan.Add = append(plan.Add, change)
	}
	for _, p := range b.deletes {
		plan.Remove = append(plan.Remove, MigrationFileChange{Path: p, Action: MigrationActionRemove})
	}

	if plan.Add == nil {
		plan.Add = []MigrationFileChange{}
	}
	if plan.Convert == nil {
		plan.Convert = []MigrationFileChange{}
	}
	if plan.Remove == nil {
		plan.Remove = []MigrationFileChange{}
	}
	return plan
}

func migrationPRTitle(commitPrefix string) string {
	return strings.TrimSpace(commitPrefix + " move this repo to the current format")
}

// migrationPRBody is the plain-words explanation on the pull request:
// what changed, and the promise that undoing it is one revert.
func migrationPRBody(b *migrationBuild) string {
	var sb strings.Builder
	sb.WriteString("This pull request moves the whole repository to Sharko's current file format.\n\n")
	sb.WriteString("What changes:\n\n")
	sb.WriteString("- The generated template files Sharko used to keep here are removed. The deploy logic they held now lives in Sharko's engine chart, which this repository points at with one file.\n")
	sb.WriteString("- `engine/application.yaml` is added: that pointer, and the only moving part left.\n")
	sb.WriteString("- Which addons run on which cluster moves into one file per cluster, under `clusters/`.\n")
	sb.WriteString("- How Sharko reaches each cluster moves to `fleet/connections.yaml`.\n")
	sb.WriteString("- Helm values move under `values/` — one file per addon, per cluster where it differs.\n")
	sb.WriteString("- The addon list becomes `catalog/addons.yaml`, holding only YOUR additions and changes. The addons Sharko ships now come with the engine, so they no longer need copying into your repository.\n\n")
	sb.WriteString("Nothing about what is running changes. Every addon stays on the same cluster, at the same version, with the same values.\n\n")
	if len(b.notes) > 0 {
		sb.WriteString("Worth reading before you merge:\n\n")
		for _, note := range b.notes {
			sb.WriteString("- " + note + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("If anything looks wrong after merging, revert this one pull request and the repository is exactly as it was.\n")
	return sb.String()
}

// ─── the single, all-or-nothing PR ───────────────────────────────────────

// commitMigrationPR creates one branch, writes every file and every delete
// onto it, and opens one pull request.
//
// Unlike commitChangesWithMeta, a failure at ANY step deletes the branch
// again. The migration touches the whole repository, so a half-written
// branch left behind is not untidiness — it is a thing somebody could
// merge. The base branch is never written to; only the merge does that.
func (o *Orchestrator) commitMigrationPR(ctx context.Context, b *migrationBuild, autoMerge *bool) (*GitResult, error) {
	if o.gitMu != nil {
		o.gitMu.Lock()
		defer o.gitMu.Unlock()
	}

	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	branch := fmt.Sprintf("%smigrate-to-v4-%s", o.gitops.BranchPrefix, hex.EncodeToString(suffix))

	if err := o.git.CreateBranch(ctx, branch, o.gitops.BaseBranch); err != nil {
		return nil, fmt.Errorf("creating branch %q: %w", branch, err)
	}

	commitMsg := migrationPRTitle(o.gitops.CommitPrefix)
	abandon := func(step string, err error) (*GitResult, error) {
		// Best-effort cleanup: the branch is the only thing that exists,
		// and removing it is what makes a retry a clean start.
		_ = o.git.DeleteBranch(ctx, branch)
		return nil, fmt.Errorf("migration stopped while %s — nothing was changed on %s: %w",
			step, o.gitops.BaseBranch, err)
	}

	if err := o.git.BatchCreateFiles(ctx, b.files, branch, commitMsg); err != nil {
		return abandon("writing the new files", err)
	}
	for _, p := range b.deletes {
		if err := o.git.DeleteFile(ctx, p, branch, commitMsg); err != nil {
			return abandon(fmt.Sprintf("removing %s", p), err)
		}
	}

	pr, err := o.git.CreatePullRequest(ctx, commitMsg, migrationPRBody(b), branch, o.gitops.BaseBranch)
	if err != nil {
		return abandon("opening the pull request", err)
	}

	result := &GitResult{PRUrl: pr.URL, PRID: pr.ID, Branch: branch}

	if o.prTracker != nil {
		_ = o.prTracker.TrackPR(ctx, TrackedPR{
			PRID:       pr.ID,
			PRUrl:      pr.URL,
			PRBranch:   branch,
			PRTitle:    "Move this repo to the current format",
			PRBase:     o.gitops.BaseBranch,
			Operation:  "migrate-v3-v4",
			User:       "system",
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	if resolveAutoMerge(autoMerge, o.gitops.PRAutoMerge) {
		if mergeErr := o.git.MergePullRequest(ctx, pr.ID); mergeErr != nil {
			// The PR exists and is correct — only the merge failed. The
			// branch STAYS: deleting it here would throw away a valid,
			// reviewable migration the person can merge by hand.
			return result, fmt.Errorf("migration pull request opened but the merge failed: %w", mergeErr)
		}
		result.Merged = true
		_ = o.git.DeleteBranch(ctx, branch)
	}

	return result, nil
}

// ─── small shared helpers ────────────────────────────────────────────────

func sortedKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedFileKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

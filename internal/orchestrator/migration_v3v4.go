// Package orchestrator — the one-PR v3 → v4 migration (v4 Wave 2,
// Stories 5.1 and 5.2).
//
// One pull request converts the whole repository. Not a sequence of small
// ones, not a hand migration with a checklist: the scaffolded template
// forest comes out, the engine pin goes in, the cluster registry and every
// values file are rewritten into the v4 layout, and the full-copy catalog
// converts straight across into the v4 catalog — every v3 entry becomes
// one full, self-contained catalog.yaml entry (migration_catalog.go). Merge
// it and everything keeps running; revert that one merge and you are
// exactly back where you started.
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

	"github.com/MoranWeissman/sharko/internal/appsets"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// RepoFormatEmpty is the third answer alongside RepoFormatV3 / RepoFormatV4:
// a connected repo with neither an engine pin nor a v3 marker. Nothing to
// migrate — the first-run bootstrap is what that repo needs.
const RepoFormatEmpty = "empty"

// RepoFormatMixed is the fourth answer: the repo carries the new engine pin
// AND the old v3 files at the same time. Half-converted, half-reverted, or
// hand-edited into that state — either way it is a repo nobody should write
// to until one of the two layouts is gone, because the two halves are read
// by different parts of Sharko and they will disagree.
const RepoFormatMixed = "mixed"

// MixedLayoutMessage is the plain-words explanation for RepoFormatMixed,
// shared by the status surfaces and by the refusal every v4 write gives on
// such a repo, so a person hears one story.
const MixedLayoutMessage = "this repo has both the old and the new layout in it — engine.yaml is there, and so are the old v3 files. Finish the conversion or revert it: while both are present the engine reads one set of files and the cluster reconciler prefers the other, so they disagree about which clusters and addons are real. Sharko will not change your catalog or your clusters' addons until one of the two is gone."

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
	// Format is "v3", "v4", "empty", or "mixed" (both layouts present at
	// once — see RepoFormatMixed).
	Format string `json:"format"`
	// MigrationAvailable is true only for "v3" — the one state where
	// there is something to convert.
	MigrationAvailable bool `json:"migration_available"`
	// Message is a plain-English sentence the UI can show as-is.
	Message string `json:"message"`
	// MigrationPRURL / MigrationPRNumber are set (format "v3" only) when a
	// previous migrate call already opened a pull request that is still
	// open. Their presence is server truth that "Open migration PR" should
	// not be offered again — a UI component that remounts (and so loses
	// whatever in-memory "I already opened one" flag it kept) still learns
	// the right thing from the next status poll instead of minting a
	// second PR for the same repo.
	MigrationPRURL    string `json:"migration_pr_url,omitempty"`
	MigrationPRNumber int    `json:"migration_pr_number,omitempty"`
	// Handoff reports where the RUNTIME half of the migration has got to —
	// the ApplicationSets that keep the fleet's addons running, which live
	// in ArgoCD rather than in the repo (see migration_handoff.go). It is
	// set on v4 repos, where the only remaining question is whether the
	// second half has finished.
	Handoff *RuntimeHandoffReport `json:"handoff,omitempty"`
}

// ErrMigrationPROpen is returned by MigrateV3ToV4 when a previous attempt
// already opened a migration pull request that is still open. The
// migration branch name carries a random suffix and no cluster/addon name
// to key a retry off of (unlike findOpenPRForCluster's title-pattern
// match), so a blind retry would strand the first PR and open a second one
// that touches the same files. Refusing and pointing back at the existing
// PR is the honest answer.
type ErrMigrationPROpen struct {
	PRURL    string
	PRNumber int
}

func (e *ErrMigrationPROpen) Error() string {
	return fmt.Sprintf("a migration pull request is already open: %s", e.PRURL)
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
	// RuntimeHandoff controls the ArgoCD half of the migration. Empty (the
	// default) prepares it whenever the repo has clusters registered;
	// "skip" migrates the files only and is the escape hatch for a repo
	// with nothing running. See migration_handoff.go.
	RuntimeHandoff string `json:"runtime_handoff,omitempty"`
}

// MigrateResult is the output of MigrateV3ToV4.
type MigrateResult struct {
	// Status is "migrated", "preview", or "already_migrated".
	Status string `json:"status"`
	// Plan is always set for a dry run, and set on a real run too so the
	// caller can render exactly what the PR contains.
	Plan *MigrationPlan `json:"plan,omitempty"`
	Git  *GitResult     `json:"git,omitempty"`
	// Handoff reports what the ArgoCD half of the migration did before the
	// pull request was opened (migration_handoff.go).
	Handoff *RuntimeHandoffReport `json:"handoff,omitempty"`
	// Warnings holds plain-English advisories that do NOT mean the
	// migration failed — chiefly "the pull request is open and correct, but
	// auto-merge could not merge it for you". Same partial-success shape as
	// RegisterClusterResult/AdoptClusterResult.
	Warnings []string `json:"warnings,omitempty"`
}

// migrationBuild is the assembled, validated migration: the complete file
// map, the delete list, and the notes — everything the PR needs, computed
// before any git write.
type migrationBuild struct {
	files   map[string][]byte
	deletes []string
	// convertedFrom maps a NEW path to the OLD path it came from, so the
	// preview can say "configuration/addons-catalog.yaml → catalog.yaml"
	// instead of showing an unexplained new file.
	convertedFrom map[string]string
	notes         []string

	// clusterCount and addonNames are what the runtime handoff decides
	// against: how many clusters this repo registers (zero means there is
	// nothing running to strand) and which addon names the old
	// ApplicationSets select clusters by. Both come from the repo contents
	// the file map was computed from, so the handoff and the pull request
	// can never be reasoning about different states of the repo.
	clusterCount int
	addonNames   []string

	// handoff is the pre-merge runtime handoff's report, set on a real run
	// once prepareRuntimeHandoff has succeeded. It shapes the PR body — a
	// person reading the pull request should be able to see, before they
	// merge, that the running fleet was made safe first.
	handoff *RuntimeHandoffReport

	// keepPaths are files inside the v3 values folders that the migration
	// deliberately does NOT delete even though it is not writing them
	// either — a per-cluster values file whose name cannot become a v4
	// folder. Deleting one would be exactly the silent data loss the design
	// promises never happens.
	keepPaths map[string]bool
}

// MigrationStatus reports which data-file format the connected repo uses
// and whether the one-PR migration is available. Read-only.
//
// It returns an error when the git host would not answer (review finding
// L-9). The earlier form swallowed every read failure, and a swallowed
// failure here does not produce a vague answer — it produces a CONFIDENT
// WRONG one: a live v3 repo whose probe read failed came back as "empty",
// which is the state whose message tells the person to initialize the
// repo. Following that advice on a running v3 repo seeds the whole v4
// folder tree on top of it. A missing file is still the ordinary answer;
// only a real transport failure is an error, and callers render it as
// "can't reach your git host right now".
func (o *Orchestrator) MigrationStatus(ctx context.Context) (*MigrationStatusResult, error) {
	v4, err := o.isV4Repo(ctx)
	if err != nil {
		return nil, err
	}
	if v4 {
		// Both layouts in one repo (review finding F4). This is not a
		// finished migration and it is not a v3 repo either: the engine
		// reads the new files while the cluster reconciler still prefers
		// the old registry, so the fleet runs off one half and the Catalog
		// page shows the other. Saying "already the current format" here
		// sends somebody away from the only thing that would fix it.
		//
		// Best-effort: a probe failure must not turn a plain status read
		// into an error, and "we could not check" is not a reason to
		// invent an ambiguity.
		if mixed, mixedErr := o.hasV3MarkersChecked(ctx); mixedErr == nil && mixed {
			return &MigrationStatusResult{
				Format:             RepoFormatMixed,
				MigrationAvailable: false,
				Message:            MixedLayoutMessage,
			}, nil
		}
		result := &MigrationStatusResult{
			Format:             RepoFormatV4,
			MigrationAvailable: false,
			Message:            "this repo already uses the current format — nothing to migrate",
		}
		// The files are across; the only open question on a v4 repo is
		// whether the RUNTIME half finished. Best-effort — a cluster Sharko
		// cannot reach right now must not turn a plain status read into an
		// error.
		if handoff, handoffErr := o.inspectRuntimeHandoff(ctx); handoffErr == nil {
			result.Handoff = handoff
			if handoff.State == HandoffStatePending {
				result.Message = "this repo already uses the current format, but the ArgoCD side of the migration has not finished — " + handoff.Message
			}
		}
		return result, nil
	}

	markers, err := o.hasV3MarkersChecked(ctx)
	if err != nil {
		return nil, err
	}
	if markers {
		result := &MigrationStatusResult{
			Format:             RepoFormatV3,
			MigrationAvailable: true,
			Message:            "v3 format — migration available: one pull request moves this repo across, and everything keeps running",
		}
		// Best-effort: a listing failure here must not hide the v3 status
		// itself — the "is there a PR already" question is a bonus fact on
		// top of it, not a precondition for answering it.
		if pr, prErr := o.findOpenMigrationPR(ctx); prErr == nil && pr != nil {
			result.MigrationPRURL = pr.URL
			result.MigrationPRNumber = pr.ID
		}
		return result, nil
	}

	return &MigrationStatusResult{
		Format:             RepoFormatEmpty,
		MigrationAvailable: false,
		Message:            "this repo has not been set up yet — initialize it instead of migrating",
	}, nil
}

// inspectRuntimeHandoff answers "has the ArgoCD half finished?" without
// changing anything. Used by status only.
func (o *Orchestrator) inspectRuntimeHandoff(ctx context.Context) (*RuntimeHandoffReport, error) {
	if o.appSets == nil {
		return &RuntimeHandoffReport{
			State:   HandoffStateComplete,
			Message: "Sharko has no ArgoCD connection here, so it has nothing to report about the cluster side.",
		}, nil
	}
	addonNames, err := o.mergedAddonNames(ctx)
	if err != nil {
		return nil, err
	}
	all, err := o.appSets.List(ctx)
	if err != nil {
		return nil, err
	}
	leftovers := legacyAddonAppSets(all, addonNames)
	if len(leftovers) == 0 {
		return &RuntimeHandoffReport{
			State:   HandoffStateComplete,
			Message: "Nothing from the old setup is left in ArgoCD.",
		}, nil
	}
	names := appsets.Names(leftovers)
	sort.Strings(names)
	return &RuntimeHandoffReport{
		State:           HandoffStatePending,
		ApplicationSets: names,
		Message: fmt.Sprintf(
			"%s from the old setup %s still in ArgoCD (%s). Finish the migration to retire %s and start the new engine.",
			plainCount(len(names), "ApplicationSet", "ApplicationSets"),
			wasWereCount(len(names)), strings.Join(names, ", "), pluralIt(len(names))),
	}, nil
}

// PreviewMigration returns the full dry-run plan for a v3 repo: every file
// the migration PR would add, convert, or remove, with the rendered
// content of everything it writes. Zero side effects.
func (o *Orchestrator) PreviewMigration(ctx context.Context) (*MigrationPlan, error) {
	status, err := o.MigrationStatus(ctx)
	if err != nil {
		return nil, err
	}
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
	status, err := o.MigrationStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status.Format == RepoFormatV4 {
		return &MigrateResult{
			Status:  "already_migrated",
			Plan:    &MigrationPlan{Format: RepoFormatV4, Notes: []string{status.Message}},
			Handoff: status.Handoff,
		}, nil
	}
	if status.Format == RepoFormatEmpty {
		return nil, errors.New(status.Message)
	}

	// A real run refuses to open a second migration PR when one from a
	// previous attempt is already open — see ErrMigrationPROpen. A dry
	// run has zero side effects regardless, so it stays available (it is
	// how the UI would show "here is what's still pending" if it wanted
	// to), and status already carried this fact for free.
	if !req.DryRun && status.MigrationPRURL != "" {
		return nil, &ErrMigrationPROpen{PRURL: status.MigrationPRURL, PRNumber: status.MigrationPRNumber}
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

	// The RUNTIME half, and it runs BEFORE the branch exists (review
	// finding B-1). The ApplicationSets that keep this fleet's addons
	// running live in ArgoCD, not in this repo, and this pull request takes
	// away everything they read. Make them harmless first, or refuse —
	// there is no safe third option, and a refusal here has written
	// nothing. See migration_handoff.go for the whole sequence.
	handoff, err := o.prepareRuntimeHandoff(ctx, req.RuntimeHandoff, build.clusterCount, build.addonNames)
	if err != nil {
		return nil, err
	}
	build.handoff = handoff
	plan = o.planFromBuild(build) // re-render: the handoff adds notes and PR-body wording

	gitResult, warnings, err := o.commitMigrationPR(ctx, build, req.AutoMerge)
	if err != nil {
		return nil, err
	}
	return &MigrateResult{
		Status:   "migrated",
		Plan:     plan,
		Git:      gitResult,
		Handoff:  handoff,
		Warnings: warnings,
	}, nil
}

// ─── building the migration ──────────────────────────────────────────────

// buildMigration assembles the complete, validated file map. It performs
// only READS against git; nothing here can leave a mark on the repo.
func (o *Orchestrator) buildMigration(ctx context.Context) (*migrationBuild, error) {
	paths := newV3DataPathSet(o.paths)

	b := &migrationBuild{
		files:         map[string][]byte{},
		convertedFrom: map[string]string{},
		keepPaths:     map[string]bool{},
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

	// What the runtime handoff decides against — recorded here so it comes
	// from the same read of the repo the file map does.
	b.clusterCount = len(clusters)
	b.addonNames = sortedBoolKeys(catalogNames)

	// 3 — catalog.yaml: the org's full approved list, straight across from
	// the v3 full-copy catalog (migration_catalog.go).
	catalogSpec, catalogNotes := buildCatalogFromV3(v3Catalog)
	b.notes = append(b.notes, catalogNotes...)
	catalogBody, err := config.SaveAddonCatalog(catalogSpec)
	if err != nil {
		return nil, fmt.Errorf("building %s: %w", config.AddonCatalogPath, err)
	}
	b.files[config.AddonCatalogPath] = catalogBody
	b.convertedFrom[config.AddonCatalogPath] = paths.catalog

	// Semantic gate: every converted entry must be complete on its own —
	// repoURL/chart/version, straight from the v3 entry. An entry missing
	// one fails HERE, in plain English, rather than after the PR merges
	// and the engine cannot render it.
	if err := catalog.ValidateCatalogSpec(catalogSpec); err != nil {
		return nil, fmt.Errorf("the new catalog file would not be usable: %w", err)
	}

	// 4 — managed-clusters.yaml + one clusters/<name>.yaml per cluster.
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
//
// Story 8.6 gap fix (v4 Wave 2 all-or-nothing audit): parseAddonsCatalog's
// legacy bare-YAML path (unlike internal/catalog.LoadBytes, which rejects
// this outright) does not reject a catalog that names the same addon
// twice — buildCatalogFromV3 folds entries into a map keyed by name, so a
// duplicate silently collapses into whichever entry sorts last, with no
// note telling the operator a whole entry's fields were dropped. That is
// exactly the silent-data-loss failure class the migration's own design
// doc promises never happens ("nothing is lost silently"), so it is
// refused here, before any git write, with the same actionable shape
// internal/catalog.LoadBytes already uses for the identical mistake in
// the curated catalog.
func (o *Orchestrator) readV3Catalog(ctx context.Context, catalogPath string) ([]models.AddonCatalogEntry, error) {
	body, ok := o.readFileIfExists(ctx, catalogPath)
	if !ok || len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}
	entries, err := parseAddonsCatalog(body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", catalogPath, err)
	}
	if err := rejectDuplicateCatalogNames(entries); err != nil {
		return nil, fmt.Errorf("reading %s: %w", catalogPath, err)
	}
	return entries, nil
}

// rejectDuplicateCatalogNames returns an error naming the first addon name
// that appears more than once in entries, or nil when every name is
// unique. Empty names are not this check's concern — buildCatalogFromV3
// already skips those with its own note.
func rejectDuplicateCatalogNames(entries []models.AddonCatalogEntry) error {
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		if seen[e.Name] {
			return fmt.Errorf(
				"addon %q is listed more than once — refusing to migrate: the new catalog file can only hold one entry per addon, and silently keeping just one would drop the other's settings without telling you. Remove the duplicate and try again",
				e.Name)
		}
		seen[e.Name] = true
	}
	return nil
}

// buildV4ClusterFiles writes managed-clusters.yaml and one
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
				if _, assigned := entry.Labels[addon]; assigned {
					continue // folded into the addon's entry below
				}
				// A DANGLING version pin (review finding M-7): this cluster
				// pins a version for an addon it does not actually run.
				// Dropping it would be silent data loss, and carrying it as
				// a plain label would leave a v3-shaped key on a v4
				// connection record that nothing reads. So it becomes what
				// it always meant — a pin — on a KEPT but switched-off
				// entry, which is the same shape the migration already uses
				// for an addon whose label said anything other than
				// "enabled" (design doc §2.1: turning it back on later is a
				// one-word change).
				assignment.Addons[addon] = models.ClusterAddonsAddon{
					Enabled: false,
					Version: value,
				}
				b.notes = append(b.notes, fmt.Sprintf(
					"Cluster %q pinned %s to version %s but was not running it. The pin is kept in clusters/%s.yaml with the addon switched off, so turning it on later gives you that same version",
					entry.Name, addon, value, entry.Name))
				continue
			}
			if isVersionKey && !catalogNames[addon] && addon != "" {
				// A version pin for something that is not in the addon list
				// at all. It stays as an ordinary label (below), which loses
				// nothing — but say so, because a person who put it there
				// meant it to do something.
				b.notes = append(b.notes, fmt.Sprintf(
					"Cluster %q has a %q label, but %q is not in your addon list. The label is carried across as an ordinary label; it does not pin anything",
					entry.Name, key, addon))
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
		return fmt.Errorf("building %s: %w", V4ManagedClustersPath, err)
	}
	b.files[V4ManagedClustersPath] = connectionsBody
	b.convertedFrom[V4ManagedClustersPath] = o.v3RegistryPath()
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
	registered := map[string]bool{}
	for _, cluster := range clusters {
		registered[cluster.Name] = true
		oldPath := path.Join(paths.clusterValues, cluster.Name+".yaml")
		if err := o.splitV3ClusterValuesFile(ctx, b, oldPath, cluster.Name, catalogNames); err != nil {
			return err
		}
	}
	return o.rescueOrphanClusterValues(ctx, b, paths, registered, catalogNames)
}

// rescueOrphanClusterValues handles the per-cluster values files whose
// cluster is NOT in the registry (review finding M-7b).
//
// Before this, those files were swept into the delete list by
// collectMigrationDeletes' "whatever else is sitting in the two v3 values
// folders" pass — removed with no note and no replacement. That is exactly
// the silent loss the fleet-wide side already refuses to commit
// (buildV4GlobalValues moves every file it finds, catalog entry or not,
// because "losing hand-written values is not a tidy-up, it is data loss").
// The two sides now behave the same way.
//
// The one file still deleted without ceremony is the example the v3
// bootstrap itself shipped (cluster-example.yaml). That is scaffold, not
// somebody's work, and it is identified the way the rest of the scaffold
// is: by asking the embedded template tree what a v3 bootstrap wrote.
func (o *Orchestrator) rescueOrphanClusterValues(ctx context.Context, b *migrationBuild, paths v3DataPathSet, registered, catalogNames map[string]bool) error {
	names, err := o.git.ListDirectory(ctx, paths.clusterValues, o.gitops.BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return nil
		}
		return fmt.Errorf("listing %s: %w", paths.clusterValues, err)
	}

	scaffold := map[string]bool{}
	for _, p := range v3ScaffoldRepoPaths() {
		scaffold[p] = true
	}

	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		cluster := strings.TrimSuffix(name, ".yaml")
		if registered[cluster] {
			continue
		}
		oldPath := path.Join(paths.clusterValues, name)
		if scaffold[oldPath] {
			continue // the bootstrap's own example file — scaffold, not data
		}
		if segErr := checkV4PathSegment("cluster", cluster); segErr != nil {
			// It cannot become a v4 folder, so it cannot be moved. Leaving
			// it where it is beats deleting it.
			b.keepPaths[oldPath] = true
			b.notes = append(b.notes, fmt.Sprintf(
				"%s is left exactly where it is: %v. Nothing reads it there, but nothing throws it away either", oldPath, segErr))
			continue
		}
		before := len(b.files)
		if err := o.splitV3ClusterValuesFile(ctx, b, oldPath, cluster, catalogNames); err != nil {
			return err
		}
		if len(b.files) == before {
			// Nothing came out of it — an empty file, or every block was
			// unusable and the split already left its own note. Keep the
			// original rather than delete a file we produced no replacement
			// for.
			b.keepPaths[oldPath] = true
			continue
		}
		b.notes = append(b.notes, fmt.Sprintf(
			"%s holds values for %q, which is not one of your registered clusters. They are moved under values/clusters/%s/ so nothing is lost, but they deploy nothing until you register that cluster",
			oldPath, cluster, cluster))
	}
	return nil
}

// splitV3ClusterValuesFile turns one v3 per-cluster values document into
// one plain Helm values file per addon under values/clusters/<cluster>/.
// Shared by the registry-driven pass and the orphan rescue above, so both
// carry values across by exactly the same rules.
func (o *Orchestrator) splitV3ClusterValuesFile(ctx context.Context, b *migrationBuild, oldPath, clusterName string, catalogNames map[string]bool) error {
	body, ok := o.readFileIfExists(ctx, oldPath)
	if !ok || len(strings.TrimSpace(string(body))) == 0 {
		return nil
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
		newPath, err := v4ClusterValuesPath(clusterName, addon)
		if err != nil {
			return fmt.Errorf("cannot migrate this repo: %w", err)
		}
		rendered, err := marshalYAMLMap(values)
		if err != nil {
			return fmt.Errorf("rewriting the values for %s on %s: %w", addon, clusterName, err)
		}
		b.files[newPath] = rendered
		b.convertedFrom[newPath] = oldPath
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
		if b.keepPaths[p] {
			continue // deliberately left in place — see rescueOrphanClusterValues
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
		case p == V4ManagedClustersPath:
			_, err = models.LoadManagedClusters(body)
		case p == config.AddonCatalogPath:
			_, err = config.LoadAddonCatalog(body)
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
	if b.handoff != nil && b.handoff.Message != "" {
		plan.Notes = append(plan.Notes, b.handoff.Message)
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
	sb.WriteString("- `engine.yaml` is added: that pointer, and the only moving part left.\n")
	sb.WriteString("- Which addons run on which cluster moves into one file per cluster, under `clusters/`.\n")
	sb.WriteString("- How Sharko reaches each cluster moves to `managed-clusters.yaml`.\n")
	sb.WriteString("- Helm values move under `values/` — one file per addon, per cluster where it differs.\n")
	sb.WriteString("- The addon list becomes `catalog.yaml`: your full list of addons approved for this org, moved across whole — every chart, repo, version and setting your old catalog file had, still there, still yours to review here.\n")
	sb.WriteString("- Each addon's `secrets:` block, if it had one, moves into that same addon's entry in `catalog.yaml` — nothing is left behind in this description alone.\n\n")
	sb.WriteString("Nothing about what is running changes. Every addon stays on the same cluster, at the same version, with the same values.\n\n")

	// The runtime half. A person about to merge this deserves to see, on
	// the pull request itself, what was done to the running fleet BEFORE
	// the request was opened — that is the whole reason merging it is safe.
	if b.handoff != nil {
		sb.WriteString("What was already done in ArgoCD, before this pull request was opened:\n\n")
		sb.WriteString("- " + b.handoff.Message + "\n")
		if len(b.handoff.ApplicationSets) > 0 {
			sb.WriteString("- ApplicationSets made safe: " + strings.Join(b.handoff.ApplicationSets, ", ") + "\n")
		}
		if len(b.handoff.ReleasedApplications) > 0 {
			sb.WriteString(fmt.Sprintf(
				"- %s had their delete-everything marker removed, so what they installed stays running no matter what happens to them: %s\n",
				plainCount(len(b.handoff.ReleasedApplications), "Application", "Applications"),
				strings.Join(b.handoff.ReleasedApplications, ", ")))
		}
		sb.WriteString("\n")
	}

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

// findOpenMigrationPR searches for an existing open PR from a previous
// migration attempt. Same idempotent-retry shape as findOpenPRForCluster in
// git_helpers.go, but keyed on the branch prefix rather than the PR title:
// a migration branch (see commitMigrationPR) carries a random suffix and
// no cluster/addon name, so title-pattern matching has nothing to match
// on — the branch prefix is the one stable thing every migration branch
// shares.
func (o *Orchestrator) findOpenMigrationPR(ctx context.Context) (*gitprovider.PullRequest, error) {
	prs, err := o.git.ListPullRequests(ctx, "open")
	if err != nil {
		return nil, fmt.Errorf("listing open PRs: %w", err)
	}
	prefix := o.gitops.BranchPrefix + "migrate-to-v4-"
	for i := range prs {
		if strings.HasPrefix(prs[i].SourceBranch, prefix) {
			return &prs[i], nil
		}
	}
	return nil, nil
}

// ─── the single, all-or-nothing PR ───────────────────────────────────────

// commitMigrationPR creates one branch, writes every file and every delete
// onto it, and opens one pull request.
//
// Unlike commitChangesWithMeta, a failure at ANY step deletes the branch
// again. The migration touches the whole repository, so a half-written
// branch left behind is not untidiness — it is a thing somebody could
// merge. The base branch is never written to; only the merge does that.
//
// Returns (result, warnings, error). The warnings channel exists for one
// specific outcome (review finding H-4): auto-merge failing. That is NOT
// a failed migration — the pull request is open, complete and correct, and
// somebody can merge it by hand. Reporting it as an error threw the
// GitResult away with it, so the caller lost the PR link for a PR that
// definitely exists, and the person was told the migration failed when it
// had not.
func (o *Orchestrator) commitMigrationPR(ctx context.Context, b *migrationBuild, autoMerge *bool) (*GitResult, []string, error) {
	if o.gitMu != nil {
		o.gitMu.Lock()
		defer o.gitMu.Unlock()
	}

	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	branch := fmt.Sprintf("%smigrate-to-v4-%s", o.gitops.BranchPrefix, hex.EncodeToString(suffix))

	if err := o.git.CreateBranch(ctx, branch, o.gitops.BaseBranch); err != nil {
		return nil, nil, fmt.Errorf("creating branch %q: %w", branch, err)
	}

	commitMsg := migrationPRTitle(o.gitops.CommitPrefix)
	abandon := func(step string, err error) (*GitResult, []string, error) {
		// Best-effort cleanup: the branch is the only thing that exists,
		// and removing it is what makes a retry a clean start.
		_ = o.git.DeleteBranch(ctx, branch)
		return nil, nil, fmt.Errorf("migration stopped while %s — nothing was changed on %s: %w",
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

	var warnings []string
	if resolveAutoMerge(autoMerge, o.gitops.PRAutoMerge) {
		if mergeErr := o.git.MergePullRequest(ctx, pr.ID); mergeErr != nil {
			// The PR exists and is correct — only the merge failed. The
			// branch STAYS: deleting it here would throw away a valid,
			// reviewable migration the person can merge by hand. And this
			// comes back as a WARNING on a successful result, not an error
			// that discards the PR link (review finding H-4).
			warnings = append(warnings, fmt.Sprintf(
				"The migration pull request is open and ready, but Sharko could not merge it for you (%v). "+
					"Merge it yourself at %s — everything in it is correct.", mergeErr, pr.URL))
			return result, warnings, nil
		}
		result.Merged = true
		_ = o.git.DeleteBranch(ctx, branch)
	}

	return result, warnings, nil
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

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
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

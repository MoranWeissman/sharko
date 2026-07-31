package orchestrator

// catalog_ops.go — the one way an addon gets into the org's catalog.
//
// Design doc .bmad/output/architecture/2026-07-31-catalog-approved-model.md
// §3 and §4. There are three doors into catalog.yaml and they all end up
// here, producing the same pull request with the same full entry:
//
//  1. Pick one from the Marketplace (the entry is pre-filled from the
//     curated list — set from_marketplace).
//  2. Add your own chart (chart location, version, namespace typed in).
//  3. Hand-edit the file in git (no code involved).
//
// One call can carry many addons, and it still makes ONE pull request —
// that is what the first-run wizard needs when somebody ticks five things.
// One call can also enable the addons on a cluster at the same time, which
// makes one pull request touching both catalog.yaml and
// clusters/<name>.yaml: the reviewer sees the whole change in one diff, and
// one merge makes both true.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitops"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// CatalogAddonInput describes ONE addon being added to the org's catalog.
//
// The entry written to catalog.yaml is self-contained, so RepoURL, Chart
// and Version must all end up set. FromMarketplace fills the ones the
// curated list can supply (chart location, default namespace, the
// needed-secrets list) so the caller only has to choose a version.
type CatalogAddonInput struct {
	Name string `json:"name"`

	// FromMarketplace copies the chart location, default namespace and
	// needed-secrets list out of the Marketplace's curated entry for this
	// name. Anything set explicitly below still wins. An unknown name is
	// an error rather than a silent empty entry — a pre-filled shortcut
	// that quietly filled in nothing would produce a broken catalog.
	FromMarketplace bool `json:"from_marketplace,omitempty"`

	RepoURL           string                          `json:"repo_url,omitempty"`
	Chart             string                          `json:"chart,omitempty"`
	Version           string                          `json:"version,omitempty"`
	Namespace         string                          `json:"namespace,omitempty"`
	Settings          *config.AddonSettings           `json:"settings,omitempty"`
	Secrets           []config.AddonSecretRequirement `json:"secrets,omitempty"`
	AdditionalSources []models.AddonSource            `json:"additional_sources,omitempty"`
	ExtraHelmValues   map[string]string               `json:"extra_helm_values,omitempty"`
}

// AddToCatalogRequest adds one or more addons to catalog.yaml, optionally
// switching them on for a cluster in the same pull request.
type AddToCatalogRequest struct {
	// Addons is the list to add. One element is the ordinary single add;
	// several elements still produce exactly ONE pull request.
	Addons []CatalogAddonInput `json:"addons"`

	// EnableOnCluster, when set, also switches every addon in this request
	// on for that cluster — one pull request touching catalog.yaml and
	// clusters/<name>.yaml together. Empty means catalog only.
	EnableOnCluster string `json:"enable_on_cluster,omitempty"`

	DryRun bool `json:"dry_run,omitempty"`
	// AutoMerge is the per-request auto-merge decision. nil falls back to
	// the connection-level default.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// AddToCatalogResult reports what the pull request will do.
type AddToCatalogResult struct {
	*GitResult
	// Added names the addons written into catalog.yaml, sorted.
	Added []string `json:"added"`
	// Enabled names the addons also switched on, and on which cluster.
	// Empty when the request was catalog-only.
	Enabled []string `json:"enabled,omitempty"`
	Cluster string   `json:"cluster,omitempty"`
}

// ErrAddonNotInMarketplace marks a from_marketplace add naming an addon the
// curated list does not carry.
var ErrAddonNotInMarketplace = errors.New("addon not in the Marketplace")

// AddToCatalog writes the given addons into catalog.yaml — and, when
// EnableOnCluster is set, into clusters/<name>.yaml too — and commits the
// lot as one pull request.
//
// A missing catalog.yaml is not an error: a fresh repo has no approved
// addons on purpose, and this call creates the file. An addon already in
// the catalog is overwritten in place, so re-adding is an update rather
// than a duplicate.
//
// Nothing is written unless everything checks out. A name that could escape
// the data folders, an entry missing its chart location, an unknown cluster
// or an addon whose required values are not set all fail before any branch
// exists.
func (o *Orchestrator) AddToCatalog(ctx context.Context, req AddToCatalogRequest) (*AddToCatalogResult, error) {
	if len(req.Addons) == 0 {
		return nil, fmt.Errorf("name at least one addon to add to the catalog")
	}

	// Resolve every entry first — no git reads, no writes, so a bad
	// request costs nothing.
	entries := make(map[string]config.AddonCatalogEntry, len(req.Addons))
	names := make([]string, 0, len(req.Addons))
	for _, in := range req.Addons {
		if in.Name == "" {
			return nil, fmt.Errorf("every addon in the request needs a name")
		}
		// The addon name becomes a values/global/<addon>.yaml path segment
		// and a Kubernetes label key the moment it is enabled anywhere, so
		// it is checked at the point it enters the repo — the only place
		// that covers both.
		if err := checkV4PathSegment("addon", in.Name); err != nil {
			return nil, err
		}
		if _, dup := entries[in.Name]; dup {
			return nil, fmt.Errorf("addon %q is listed twice in the same request", in.Name)
		}
		entry, err := o.buildCatalogEntry(in)
		if err != nil {
			return nil, err
		}
		entries[in.Name] = entry
		names = append(names, in.Name)
	}
	sort.Strings(names)

	// Refuse an unknown cluster before any write, same as EnableAddonV4.
	var clusterPath string
	if req.EnableOnCluster != "" {
		var err error
		clusterPath, err = v4ClusterAddonsPath(req.EnableOnCluster)
		if err != nil {
			return nil, err
		}
		if !o.v4ClusterExists(ctx, req.EnableOnCluster) {
			return nil, fmt.Errorf("%w: %q", ErrV4ClusterNotFound, req.EnableOnCluster)
		}
	}

	spec, err := o.readCatalog(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
	}
	if spec.Addons == nil {
		spec.Addons = make(map[string]config.AddonCatalogEntry)
	}
	for name, entry := range entries {
		spec.Addons[name] = entry
	}

	if err := catalog.ValidateCatalogSpec(spec); err != nil {
		return nil, err
	}

	catalogBody, err := config.SaveAddonCatalog(spec)
	if err != nil {
		return nil, fmt.Errorf("rendering %s: %w", config.AddonCatalogPath, err)
	}

	files := map[string][]byte{config.AddonCatalogPath: catalogBody}
	existingCatalog, _ := o.readFileIfExists(ctx, config.AddonCatalogPath)

	var warnings []string
	var existingClusterAddons, updatedClusterAddons []byte
	if req.EnableOnCluster != "" {
		// The assignment file is rewritten whole, so a swallowed read
		// error would open a pull request wiping every other addon on this
		// cluster.
		var readErr error
		existingClusterAddons, _, readErr = o.readFileForRewrite(ctx, clusterPath)
		if readErr != nil {
			return nil, readErr
		}
		updatedClusterAddons = existingClusterAddons

		view := catalog.BuildCatalogView(o.curated, spec)
		for _, name := range names {
			addonWarnings, err := o.checkEnableReady(ctx, req.EnableOnCluster, name, view[name])
			if err != nil {
				return nil, err
			}
			warnings = append(warnings, addonWarnings...)

			updatedClusterAddons, err = gitops.SetClusterAddonsAddon(
				updatedClusterAddons, req.EnableOnCluster, name, true, nil, nil)
			if err != nil {
				return nil, fmt.Errorf("updating %s: %w", clusterPath, err)
			}
		}
		files[clusterPath] = updatedClusterAddons
	}

	title := addToCatalogTitle(names, req.EnableOnCluster)

	if req.DryRun {
		previews := []FilePreview{{
			Path:   config.AddonCatalogPath,
			Action: fileActionFromExists(existingCatalog),
			Diff:   o.buildFileDiff(config.AddonCatalogPath, existingCatalog, catalogBody, fileActionFromExists(existingCatalog)),
		}}
		if req.EnableOnCluster != "" {
			previews = append(previews, FilePreview{
				Path:   clusterPath,
				Action: fileActionFromExists(existingClusterAddons),
				Diff:   o.buildFileDiff(clusterPath, existingClusterAddons, updatedClusterAddons, fileActionFromExists(existingClusterAddons)),
			})
		}
		return &AddToCatalogResult{
			GitResult: &GitResult{
				DryRun: &DryRunResult{
					EffectiveAddons: names,
					FilesToWrite:    previews,
					PRTitle:         o.gitops.CommitPrefix + " " + title,
					SecretsToCreate: []string{},
				},
				Warnings: warnings,
			},
			Added:   names,
			Enabled: enabledNames(names, req.EnableOnCluster),
			Cluster: req.EnableOnCluster,
		}, nil
	}

	// One addon in the request gets its name on the tracked pull request so
	// the dashboard can show it; a batch has no single addon to name.
	trackedAddon := ""
	if len(names) == 1 {
		trackedAddon = names[0]
	}
	opCode := "catalog-add"
	if req.EnableOnCluster != "" {
		opCode = "catalog-add-enable"
	}

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, strings.ToLower(title),
		o.prMeta(req.AutoMerge, opCode, title, req.EnableOnCluster, trackedAddon))
	if err != nil {
		return nil, fmt.Errorf("committing catalog change to Git: %w", err)
	}
	gitResult.Warnings = append(gitResult.Warnings, warnings...)

	return &AddToCatalogResult{
		GitResult: gitResult,
		Added:     names,
		Enabled:   enabledNames(names, req.EnableOnCluster),
		Cluster:   req.EnableOnCluster,
	}, nil
}

// enabledNames returns names when a cluster was named, nil otherwise.
func enabledNames(names []string, cluster string) []string {
	if cluster == "" {
		return nil
	}
	return names
}

// addToCatalogTitle writes the pull-request title in plain words.
func addToCatalogTitle(names []string, cluster string) string {
	var what string
	switch {
	case len(names) == 1:
		what = fmt.Sprintf("Add %s to the catalog", names[0])
	default:
		what = fmt.Sprintf("Add %d addons to the catalog", len(names))
	}
	if cluster == "" {
		return what
	}
	if len(names) == 1 {
		return fmt.Sprintf("%s and enable it on %s", what, cluster)
	}
	return fmt.Sprintf("%s and enable them on %s", what, cluster)
}

// buildCatalogEntry turns one request item into the entry written to
// catalog.yaml, filling from the Marketplace's curated list when asked.
func (o *Orchestrator) buildCatalogEntry(in CatalogAddonInput) (config.AddonCatalogEntry, error) {
	entry := config.AddonCatalogEntry{
		RepoURL:           in.RepoURL,
		Chart:             in.Chart,
		Version:           in.Version,
		Namespace:         in.Namespace,
		Settings:          in.Settings,
		Secrets:           in.Secrets,
		AdditionalSources: in.AdditionalSources,
		ExtraHelmValues:   in.ExtraHelmValues,
	}

	if in.FromMarketplace {
		curatedEntry, ok := o.curatedEntry(in.Name)
		if !ok {
			return config.AddonCatalogEntry{}, fmt.Errorf(
				"%w: %q — the Marketplace has no entry to copy, so give the chart location and version yourself",
				ErrAddonNotInMarketplace, in.Name)
		}
		if entry.RepoURL == "" {
			entry.RepoURL = curatedEntry.Repo
		}
		if entry.Chart == "" {
			entry.Chart = curatedEntry.Chart
		}
		if entry.Namespace == "" {
			entry.Namespace = curatedEntry.DefaultNamespace
		}
		if len(entry.Secrets) == 0 {
			entry.Secrets = catalog.CuratedSecretsForEntry(curatedEntry.Secrets)
		}
	}

	// The Marketplace deliberately ships no version — a version baked into
	// a signed artefact goes stale — so this one always comes from the
	// caller, whichever door they came through.
	if entry.Version == "" {
		return config.AddonCatalogEntry{}, fmt.Errorf(
			"addon %q needs a version — pick one from the chart's available versions", in.Name)
	}
	if entry.RepoURL == "" {
		return config.AddonCatalogEntry{}, fmt.Errorf(
			"addon %q needs a chart repository URL (https:// or oci://)", in.Name)
	}
	if entry.Chart == "" {
		return config.AddonCatalogEntry{}, fmt.Errorf(
			"addon %q needs a chart name", in.Name)
	}

	return entry, nil
}

// curatedEntry looks one addon up in the Marketplace's curated list.
func (o *Orchestrator) curatedEntry(name string) (catalog.CatalogEntry, bool) {
	if o.curated == nil {
		return catalog.CatalogEntry{}, false
	}
	for _, e := range o.curated.Entries() {
		if e.Name == name {
			return e, true
		}
	}
	return catalog.CatalogEntry{}, false
}

// checkEnableReady runs the same semantic validation EnableAddonV4 runs, so
// the combined add-and-enable pull request cannot promise something the
// plain enable would refuse. Returns the non-blocking warnings.
func (o *Orchestrator) checkEnableReady(ctx context.Context, clusterName, addonName string, entry catalog.CatalogAddon) ([]string, error) {
	if !entry.Deployable {
		return nil, &V4SemanticValidationError{
			Cluster:  clusterName,
			Addon:    addonName,
			Problems: incompleteEntryProblems(addonName, entry.MissingFields),
		}
	}

	globalValuesPath, err := v4GlobalValuesPath(addonName)
	if err != nil {
		return nil, err
	}
	clusterValuesPath, err := v4ClusterValuesPath(clusterName, addonName)
	if err != nil {
		return nil, err
	}

	globalRaw, _ := o.readFileIfExists(ctx, globalValuesPath)
	clusterRaw, _ := o.readFileIfExists(ctx, clusterValuesPath)

	globalValues, err := parseYAMLMap(globalRaw)
	if err != nil {
		return nil, fmt.Errorf("parsing existing %s: %w", globalValuesPath, err)
	}
	clusterValues, err := parseYAMLMap(clusterRaw)
	if err != nil {
		return nil, fmt.Errorf("parsing existing %s: %w", clusterValuesPath, err)
	}

	mergedValues := deepCopyYAMLMap(globalValues)
	deepMergeYAMLMaps(mergedValues, clusterValues)

	problems, warnings := o.validateV4AddonInputs(entry, mergedValues)
	if len(problems) > 0 {
		return nil, &V4SemanticValidationError{Cluster: clusterName, Addon: addonName, Problems: problems}
	}
	return warnings, nil
}

// readCatalog reads and parses the org's catalog.yaml. A file that does not
// exist yet is NOT an error — a fresh repo has approved nothing, and the
// first add creates the file.
func (o *Orchestrator) readCatalog(ctx context.Context) (config.AddonCatalogSpec, error) {
	data, err := o.git.GetFileContent(ctx, config.AddonCatalogPath, o.gitops.BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return config.AddonCatalogSpec{}, nil
		}
		return config.AddonCatalogSpec{}, err
	}
	return config.LoadAddonCatalog(data)
}

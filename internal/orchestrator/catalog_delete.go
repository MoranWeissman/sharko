// catalog_delete.go — DELETE /api/v1/catalog/addons/{name}, the removal
// door for an addon in catalog.yaml.
//
// LOCKED DESIGN DECISION: an addon still switched on anywhere refuses
// before any write — a delete pull request must never leave the repo
// semantically invalid, i.e. a cluster still pointing an enabled addon at a
// catalog entry that no longer exists.
package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// DeleteFromCatalogRequest is the input for DELETE /api/v1/catalog/addons/{name}.
type DeleteFromCatalogRequest struct {
	// Name is set by the handler from the path.
	Name string `json:"-"`

	// Yes is the caller's confirmation — the same word every other v4
	// write asks for. Without it (and without DryRun) DeleteFromCatalog
	// refuses with a *CatalogDeleteConfirmationError carrying the impact
	// report — deliberately mapped to 400 by the API layer, mirroring the
	// v3 DELETE /addons/{name} contract, unlike every other catalog-write
	// confirmation gate in this package (422): a client written against
	// the v3 delete confirms this door the same way.
	Yes    bool `json:"yes,omitempty"`
	DryRun bool `json:"dry_run,omitempty"`
	// AutoMerge mirrors every other v4/v3 write's per-request override.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// CatalogDeleteBlockedError marks a delete request for an addon that is
// still switched on in at least one cluster-addons/<cluster>.yaml. A delete
// pull request must never leave the repo semantically invalid — a cluster
// still pointing an enabled addon at a catalog entry that no longer exists
// — so DeleteFromCatalog refuses BEFORE any write, rather than opening a PR
// whose merge would break the fleet. Callers map this to 409; Clusters
// names exactly which clusters to switch it off on first.
type CatalogDeleteBlockedError struct {
	Addon    string
	Clusters []string
}

func (e *CatalogDeleteBlockedError) Error() string {
	return fmt.Sprintf(
		"%s is still enabled on %s — switch it off there first (DELETE /api/v1/v4/clusters/{cluster}/addons/%s on each), then delete it from the catalog",
		e.Addon, strings.Join(e.Clusters, ", "), e.Addon)
}

// CatalogDeleteConfirmationError is DeleteFromCatalog's confirmation gate,
// returned when the request set neither Yes nor DryRun. It carries the
// impact report — every file the real delete would touch — computed by the
// same cluster-footprint check that already proved (by not returning
// *CatalogDeleteBlockedError first) the addon is not enabled anywhere: that
// gate always runs first, confirmed or not, so a preview or an impact
// report can never promise a delete the real call would then refuse.
type CatalogDeleteConfirmationError struct {
	Addon        string
	FilesRemoved []string
}

func (e *CatalogDeleteConfirmationError) Error() string {
	return fmt.Sprintf(
		"removing %s from the catalog is destructive — send yes: true to confirm (or dry_run: true to see the change first)",
		e.Addon)
}

// v4AllClusterAddonsSpecs reads every cluster-addons/<cluster>.yaml in
// V4ClustersDir. An absent or empty directory returns an empty, nil slice
// rather than an error — mirrors the stance internal/service/v4addons.go's
// listClusterAddonsSpecs takes for the same directory; that helper cannot
// be reused here because internal/service depends on internal/orchestrator,
// never the other way around.
func (o *Orchestrator) v4AllClusterAddonsSpecs(ctx context.Context) ([]models.ClusterAddonsSpec, error) {
	entries, err := o.git.ListDirectory(ctx, V4ClustersDir, o.gitops.BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var specs []models.ClusterAddonsSpec
	for _, name := range entries {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		p := path.Join(V4ClustersDir, name)
		data, err := o.git.GetFileContent(ctx, p, o.gitops.BaseBranch)
		if err != nil {
			if errors.Is(err, gitprovider.ErrFileNotFound) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		spec, err := models.LoadClusterAddons(data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", p, err)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// v4ClusterFootprintForAddon reports every cluster where addonName is
// switched on (enabledOn) and every stray per-cluster values file it left
// behind — values/clusters/<cluster>/<addon>.yaml — across EVERY known
// cluster, not only the enabled ones: a cluster can carry a values file for
// an addon it once enabled and later switched off, and a delete should not
// leave that orphaned either.
func (o *Orchestrator) v4ClusterFootprintForAddon(ctx context.Context, addonName string) (enabledOn []string, strayValues []string, err error) {
	specs, err := o.v4AllClusterAddonsSpecs(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, spec := range specs {
		if a, ok := spec.Addons[addonName]; ok && a.Enabled {
			enabledOn = append(enabledOn, spec.Cluster)
		}
		p, pathErr := v4ClusterValuesPath(spec.Cluster, addonName)
		if pathErr != nil {
			// A cluster name that could not become a safe path here would
			// already have failed when its cluster-addons file was
			// written; treat it as "nothing to sweep" rather than
			// aborting the whole delete over an unrelated cluster's odd
			// name.
			continue
		}
		if _, exists := o.readFileIfExists(ctx, p); exists {
			strayValues = append(strayValues, p)
		}
	}
	sort.Strings(enabledOn)
	sort.Strings(strayValues)
	return enabledOn, strayValues, nil
}

// DeleteFromCatalog removes one addon's entry from catalog.yaml — and any
// values/global/<addon>.yaml and stray values/clusters/*/<addon>.yaml
// alongside it — and commits the lot as one pull request.
func (o *Orchestrator) DeleteFromCatalog(ctx context.Context, req DeleteFromCatalogRequest) (*GitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("%w: addon name is required", ErrCatalogRequestInvalid)
	}
	if err := checkV4PathSegment("addon", req.Name); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrCatalogRequestInvalid, err.Error())
	}

	// A v3 repo has no catalog.yaml for this door to delete from — refuse
	// before anything happens, and point at the v3 doors that do exist
	// there.
	if err := o.refuseV4ShapedWriteOnV3Repo(ctx, "removing a catalog entry"); err != nil {
		return nil, err
	}
	if err := o.refuseOnMixedLayout(ctx); err != nil {
		return nil, err
	}

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
	if _, ok := spec.Addons[req.Name]; !ok {
		return nil, fmt.Errorf("%w: %s is not in your catalog", ErrV4AddonNotInCatalog, req.Name)
	}

	// The semantic-invalidity refusal always runs first, confirmed or not
	// and dry-run or not — a preview must never promise a delete the real
	// call would then reject.
	enabledOn, strayValues, err := o.v4ClusterFootprintForAddon(ctx, req.Name)
	if err != nil {
		return nil, fmt.Errorf("checking cluster usage of %s: %w", req.Name, err)
	}
	if len(enabledOn) > 0 {
		return nil, &CatalogDeleteBlockedError{Addon: req.Name, Clusters: enabledOn}
	}

	globalValuesPath, err := v4GlobalValuesPath(req.Name)
	if err != nil {
		return nil, err
	}
	var existingDeletePaths []string
	if _, exists := o.readFileIfExists(ctx, globalValuesPath); exists {
		existingDeletePaths = append(existingDeletePaths, globalValuesPath)
	}
	existingDeletePaths = append(existingDeletePaths, strayValues...)

	if !req.Yes && !req.DryRun {
		return nil, &CatalogDeleteConfirmationError{
			Addon:        req.Name,
			FilesRemoved: append([]string{config.AddonCatalogPath}, existingDeletePaths...),
		}
	}

	delete(spec.Addons, req.Name)
	catalogBody, reformatted, err := writeCatalogFileRemove(existingCatalog, spec, req.Name)
	if err != nil {
		return nil, fmt.Errorf("rendering %s: %w", config.AddonCatalogPath, err)
	}
	var warnings []string
	if reformatted {
		warnings = append(warnings, CatalogRewriteNote)
	}

	title := fmt.Sprintf("Remove %s from the catalog", req.Name)

	if req.DryRun {
		previews := []FilePreview{{
			Path:   config.AddonCatalogPath,
			Action: "update",
			Diff:   o.buildFileDiff(config.AddonCatalogPath, existingCatalog, catalogBody, "update"),
		}}
		for _, p := range existingDeletePaths {
			old, _ := o.readFileIfExists(ctx, p)
			previews = append(previews, FilePreview{
				Path:   p,
				Action: "delete",
				Diff:   o.buildFileDiff(p, old, nil, "delete"),
			})
		}
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{},
				FilesToWrite:    previews,
				PRTitle:         o.gitops.CommitPrefix + " " + title,
				SecretsToCreate: []string{},
			},
			Warnings: warnings,
		}, nil
	}

	files := map[string][]byte{config.AddonCatalogPath: catalogBody}
	gitResult, err := o.commitChangesWithMeta(ctx, files, existingDeletePaths, strings.ToLower(title),
		o.prMeta(req.AutoMerge, OpCodeCatalogDelete, title, "", req.Name))
	if err != nil {
		return nil, fmt.Errorf("committing catalog removal to Git: %w", err)
	}
	gitResult.Warnings = append(gitResult.Warnings, warnings...)
	return gitResult, nil
}

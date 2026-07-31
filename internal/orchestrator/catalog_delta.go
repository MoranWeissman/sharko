package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// AddInternalAddonRequest is the input for adding (or updating) one
// first-class in-house addon in the caller's v4 catalog delta —
// catalog.yaml, kind AddonCatalog (design doc §2.3, v4 wave 1
// Story 3.3). RepoURL, Chart, and Version are all required: for an addon
// with no shipped-catalog entry nothing else can supply them (the same
// rule catalog.MergeDelta enforces at read time via
// *catalog.MissingRequiredFieldError).
type AddInternalAddonRequest struct {
	Name      string `json:"name"`
	RepoURL   string `json:"repo_url"`
	Chart     string `json:"chart"`
	Version   string `json:"version"`
	Namespace string `json:"namespace,omitempty"`

	// AutoMerge is the per-request auto-merge decision. nil means "fall
	// back to the connection-level PRAutoMerge default"; a non-nil value
	// overrides it for this operation only. Mirrors AddAddonRequest.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// AddInternalAddon adds or updates one entry in the caller's v4 catalog
// delta file (catalog.yaml, kind AddonCatalog) and commits it
// via a pull request — "via PR, like everything" (v4 wave 1 Story 3.3 AC).
//
// The delta file is read-modify-write: a missing file is NOT an error
// (design doc D16, "missing means empty") — AddInternalAddon starts from an
// empty AddonCatalogSpec and creates the file. An existing entry for
// req.Name is overwritten in place (idempotent re-add / update), matching
// AddAddon's upsert-by-name shape for the v3 catalog.
//
// This writes ONLY the delta file — it does not touch clusters/*.yaml. An
// addon added here is immediately assignable to a cluster (referencing it
// by name in a ClusterAddons's spec.addons map has no catalog-
// membership precondition in the v4 schema) and appears in the merged
// catalog view (catalog.MergeDelta, Origin=OriginInternal) the moment the
// PR merges.
func (o *Orchestrator) AddInternalAddon(ctx context.Context, req AddInternalAddonRequest) (*GitResult, error) {
	// Same name gate the v4 enable/disable paths use. An internal addon's
	// name is not a path segment HERE (it is a map key in
	// catalog.yaml), but it becomes one the moment somebody enables
	// the addon on a cluster (values/global/<addon>.yaml), and it becomes a
	// Kubernetes label key on the cluster Secret. Rejecting it at the point
	// it enters the repo is the only place that covers both.
	if err := checkV4PathSegment("addon", req.Name); err != nil {
		return nil, err
	}
	if req.RepoURL == "" {
		return nil, fmt.Errorf("addon repo_url is required")
	}
	if req.Chart == "" {
		return nil, fmt.Errorf("addon chart is required")
	}
	if req.Version == "" {
		return nil, fmt.Errorf("addon version is required")
	}

	spec, err := o.readCatalogDelta(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", config.AddonCatalogPath, err)
	}
	if spec.Addons == nil {
		spec.Addons = make(map[string]config.AddonCatalogEntry)
	}
	spec.Addons[req.Name] = config.AddonCatalogEntry{
		RepoURL:   req.RepoURL,
		Chart:     req.Chart,
		Version:   req.Version,
		Namespace: req.Namespace,
	}

	body, err := config.SaveAddonCatalog(spec)
	if err != nil {
		return nil, fmt.Errorf("rendering %s: %w", config.AddonCatalogPath, err)
	}

	files := map[string][]byte{
		config.AddonCatalogPath: body,
	}

	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, fmt.Sprintf("add internal addon %s", req.Name),
		o.prMeta(req.AutoMerge, "catalog-delta-add-internal-addon", fmt.Sprintf("Add internal addon %s", req.Name), "", req.Name))
	if err != nil {
		return nil, fmt.Errorf("committing internal addon %q to Git: %w", req.Name, err)
	}

	return gitResult, nil
}

// readCatalogDelta reads and parses the caller's v4 catalog.yaml. A
// file that does not exist yet is NOT an error (design doc D16, "missing
// means empty") — it returns a zero-value AddonCatalogSpec instead,
// mirroring the same gitprovider.ErrFileNotFound convention the read-side
// API handlers use (internal/api/catalog_delta.go's loadCatalogDelta).
func (o *Orchestrator) readCatalogDelta(ctx context.Context) (config.AddonCatalogSpec, error) {
	data, err := o.git.GetFileContent(ctx, config.AddonCatalogPath, o.gitops.BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return config.AddonCatalogSpec{}, nil
		}
		return config.AddonCatalogSpec{}, err
	}
	return config.LoadAddonCatalog(data)
}

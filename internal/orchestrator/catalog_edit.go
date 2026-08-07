// catalog_edit.go — PATCH /api/v1/catalog/addons/{name}, the merge-semantics
// edit door for an addon ALREADY in catalog.yaml.
//
// This is the third write path against catalog.yaml, alongside AddToCatalog
// (catalog_ops.go) and DeleteFromCatalog (catalog_delete.go). It shares
// AddToCatalog's validate-then-splice-then-PR pipeline but is narrowed to a
// single existing entry: no from_marketplace resolution, no enable-on-cluster
// combo, no batch. Editing an addon's chart, version or settings never
// switches it on or off anywhere by itself — that stays EnableAddonV4 /
// DisableAddonV4's job.
package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// EditCatalogEntryRequest is the input for PATCH /api/v1/catalog/addons/{name}
// — a merge-semantics edit of one EXISTING catalog.yaml entry. Every field
// below is optional; a nil pointer (or a nil slice/map — the field absent
// from the request body) means "leave this alone".
//
// This is deliberately NOT CatalogAddonInput: that type builds a brand-new
// entry from scratch, where an empty string is a legitimate "not set yet"
// on the way to buildCatalogEntry filling it in. This type edits an entry
// that is already complete, so it has to tell "the caller sent an empty
// string on purpose" apart from "the caller didn't mention this field" —
// hence pointers on every scalar.
type EditCatalogEntryRequest struct {
	// Name is set by the handler from the path, never from the request body.
	Name string `json:"-"`

	RepoURL   *string `json:"repo_url,omitempty"`
	Chart     *string `json:"chart,omitempty"`
	Version   *string `json:"version,omitempty"`
	Namespace *string `json:"namespace,omitempty"`

	// Settings, when non-nil, is merged field-by-field onto the existing
	// entry's settings block via catalog.MergeAddonSettings — a field this
	// request does not set is left exactly as it was, even when the
	// existing settings block already has other fields populated. A nil
	// existing settings block is treated as empty (every field unset)
	// before the merge, so the first settings edit on an entry that has
	// none yet still only sets what was asked for.
	Settings *config.AddonSettings `json:"settings,omitempty"`

	// Secrets, AdditionalSources and ExtraHelmValues replace the existing
	// value WHOLE when non-nil — the same "list/map fields replace, never
	// merge element-by-element" rule the rest of the v4 format uses
	// (design doc §3.3 decision D12). nil (the field absent from the
	// request) leaves the existing value untouched.
	Secrets           []config.AddonSecretRequirement `json:"secrets,omitempty"`
	AdditionalSources []models.AddonSource            `json:"additional_sources,omitempty"`
	ExtraHelmValues   map[string]string               `json:"extra_helm_values,omitempty"`

	DryRun bool `json:"dry_run,omitempty"`
	// AutoMerge is the per-request auto-merge decision. nil falls back to
	// the connection-level default — same contract as every other
	// catalog-write request in this package.
	AutoMerge *bool `json:"auto_merge,omitempty"`
}

// hasAnyField reports whether the request sets at least one field to
// change. An edit with nothing to change is a caller mistake, not a
// no-op PR — same stance ConfigureAddon (the v3 sibling) takes with its
// "no updatable fields provided" error.
func (req EditCatalogEntryRequest) hasAnyField() bool {
	return req.RepoURL != nil || req.Chart != nil || req.Version != nil || req.Namespace != nil ||
		req.Settings != nil || req.Secrets != nil || req.AdditionalSources != nil || req.ExtraHelmValues != nil
}

// EditCatalogEntry changes one or more fields of an addon ALREADY in
// catalog.yaml and opens a pull request with exactly that edit — the same
// validate-then-splice-then-PR pipeline AddToCatalog uses (readFileForRewrite
// fail-closed, catalog.ValidateCatalogEntry, writeCatalogFile's surgical
// splice with verified fallback), narrowed to a single existing entry.
//
// Merge semantics: fields left nil on the request are copied unchanged from
// the entry already on disk — this is an EDIT, never a rebuild from scratch
// the way AddToCatalog's buildCatalogEntry is. 404 (via ErrV4AddonNotInCatalog)
// when the name is not already in catalog.yaml — PATCH edits, it does not
// create; POST /catalog/addons is the door for a brand-new entry.
func (o *Orchestrator) EditCatalogEntry(ctx context.Context, req EditCatalogEntryRequest) (*GitResult, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("%w: addon name is required", ErrCatalogRequestInvalid)
	}
	if err := checkV4PathSegment("addon", req.Name); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrCatalogRequestInvalid, err.Error())
	}
	if !req.hasAnyField() {
		return nil, fmt.Errorf("%w: no fields to update — the request body was empty", ErrCatalogRequestInvalid)
	}

	// A v3 repo has no catalog.yaml for this door to edit — refuse before
	// anything happens, and point at the v3 doors that do exist there.
	if err := o.refuseV4ShapedWriteOnV3Repo(ctx, "editing a catalog entry"); err != nil {
		return nil, err
	}
	// A repo carrying both layouts writes into the half nothing reads —
	// refuse before anything happens, same guard AddToCatalog runs.
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

	entry, ok := spec.Addons[req.Name]
	if !ok {
		return nil, fmt.Errorf("%w: %s is not in your catalog — add it first", ErrV4AddonNotInCatalog, req.Name)
	}

	if req.RepoURL != nil {
		entry.RepoURL = *req.RepoURL
	}
	if req.Chart != nil {
		entry.Chart = *req.Chart
	}
	if req.Version != nil {
		entry.Version = *req.Version
	}
	if req.Namespace != nil {
		entry.Namespace = *req.Namespace
	}
	if req.Settings != nil {
		merged := entry.Settings
		if merged == nil {
			merged = &config.AddonSettings{}
		}
		catalog.MergeAddonSettings(merged, req.Settings)
		entry.Settings = merged
	}
	if req.Secrets != nil {
		entry.Secrets = req.Secrets
	}
	if req.AdditionalSources != nil {
		entry.AdditionalSources = req.AdditionalSources
	}
	if req.ExtraHelmValues != nil {
		entry.ExtraHelmValues = req.ExtraHelmValues
	}

	if err := catalog.ValidateCatalogEntry(req.Name, entry); err != nil {
		return nil, err
	}
	spec.Addons[req.Name] = entry

	catalogBody, reformatted, err := writeCatalogFile(
		existingCatalog, spec,
		map[string]config.AddonCatalogEntry{req.Name: entry},
		[]string{req.Name},
	)
	if err != nil {
		return nil, fmt.Errorf("rendering %s: %w", config.AddonCatalogPath, err)
	}

	var warnings []string
	if reformatted {
		warnings = append(warnings, CatalogRewriteNote)
	}

	title := fmt.Sprintf("Edit %s in the catalog", req.Name)

	if req.DryRun {
		return &GitResult{
			DryRun: &DryRunResult{
				EffectiveAddons: []string{req.Name},
				FilesToWrite: []FilePreview{{
					Path:   config.AddonCatalogPath,
					Action: "update",
					Diff:   o.buildFileDiff(config.AddonCatalogPath, existingCatalog, catalogBody, "update"),
				}},
				PRTitle:         o.gitops.CommitPrefix + " " + title,
				SecretsToCreate: []string{},
			},
			Warnings: warnings,
		}, nil
	}

	files := map[string][]byte{config.AddonCatalogPath: catalogBody}
	gitResult, err := o.commitChangesWithMeta(ctx, files, nil, strings.ToLower(title),
		o.prMeta(req.AutoMerge, OpCodeCatalogEdit, title, "", req.Name))
	if err != nil {
		return nil, fmt.Errorf("committing catalog edit to Git: %w", err)
	}
	gitResult.Warnings = append(gitResult.Warnings, warnings...)
	return gitResult, nil
}

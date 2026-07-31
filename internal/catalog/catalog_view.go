// The org's catalog, read from catalog.yaml — and nothing else.
//
// Design doc .bmad/output/architecture/2026-07-31-catalog-approved-model.md
// §1 and §4. catalog.yaml holds the addons this org approved for its
// clusters. Every entry is FULL and self-contained: chart, chart repo,
// version, namespace, settings, needed secrets. Nothing is inherited from
// a list that ships inside Sharko.
//
// This replaces the v4 "delta" model, where the shipped curated list was
// copied into the result first and the user's file was laid on top. That
// made the menu "everything Sharko curates, shown to everyone, no way to
// opt out" — which is how 45 addons nobody chose ended up looking approved.
//
// The curated list still exists. It feeds the Marketplace (the read-only
// discovery window) and it is where BuildCatalogView gets an entry's
// description, docs link, known gotchas and required values from — display
// knowledge about a chart the org already approved by name. It NEVER adds
// an addon to the view, and it never supplies a deployment field: what
// actually gets deployed is what the repo says, so the repo alone tells the
// whole story and the server and the engine chart cannot drift apart.
package catalog

import (
	"fmt"
	"sort"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Origin says whether an approved addon is also one the Marketplace knows
// about. It is provenance for the UI, not a source of deployment data —
// both origins deploy from the same catalog.yaml fields.
type Origin string

const (
	// OriginCurated means the Marketplace's curated list has an entry
	// under this name, so Sharko can show a description, docs link and
	// known gotchas alongside the org's own entry.
	OriginCurated Origin = "curated"
	// OriginInternal means nothing but the org's own catalog.yaml entry
	// has ever described this addon — an in-house chart, a private OCI
	// reference, anything the Marketplace does not carry. Every knowledge
	// field is empty for it.
	OriginInternal Origin = "internal"
)

// MissingRequiredFieldError names one catalog entry that cannot be
// deployed and the first field it is missing. Every entry needs repoURL,
// chart and version — there is no shipped list to fall back on any more,
// which is the whole point of the full-entry format.
type MissingRequiredFieldError struct {
	Addon string
	Field string
}

func (e *MissingRequiredFieldError) Error() string {
	return fmt.Sprintf(
		"addon %q in %s is missing %q: a catalog entry carries everything needed to deploy it, so repoURL, chart and version must all be set",
		e.Addon, config.AddonCatalogPath, e.Field,
	)
}

// CatalogAddon is one approved addon, as the API and the UI see it.
//
// The deployment fields are copied straight out of catalog.yaml. The
// knowledge fields are filled in from the Marketplace's curated list when
// it happens to know this addon by name, and are all empty otherwise.
type CatalogAddon struct {
	Name   string `json:"name"`
	Origin Origin `json:"origin"`

	// Deployment fields — always, only, from catalog.yaml.
	RepoURL           string                `json:"repo_url,omitempty"`
	Chart             string                `json:"chart,omitempty"`
	Version           string                `json:"version,omitempty"`
	Namespace         string                `json:"namespace,omitempty"`
	Settings          *config.AddonSettings `json:"settings,omitempty"`
	AdditionalSources []models.AddonSource  `json:"additional_sources,omitempty"`
	ExtraHelmValues   map[string]string     `json:"extra_helm_values,omitempty"`

	// Deployable is false when the entry is missing something the engine
	// needs. MissingFields names what, so a list view can flag a
	// half-written entry instead of the whole page failing over one bad
	// line somebody hand-edited.
	Deployable    bool     `json:"deployable"`
	MissingFields []string `json:"missing_fields,omitempty"`

	// Secrets: the credentials this addon needs before it works. Read from
	// the entry itself. When the entry carries none and the Marketplace
	// knows the addon, the curated list's own secrets list stands in — it
	// is knowledge about the chart, and an entry somebody hand-wrote
	// without it should still get the heads-up.
	Secrets []SecretRequirement `json:"secrets,omitempty"`

	// Knowledge fields — from the Marketplace's curated entry of the same
	// name. Always zero-value when Origin is OriginInternal.
	Description          string          `json:"description,omitempty"`
	DocsURL              string          `json:"docs_url,omitempty"`
	Homepage             string          `json:"homepage,omitempty"`
	SourceURL            string          `json:"source_url,omitempty"`
	Maintainers          []string        `json:"maintainers,omitempty"`
	License              string          `json:"license,omitempty"`
	Category             string          `json:"category,omitempty"`
	CuratedBy            []string        `json:"curated_by,omitempty"`
	SecurityScore        ScoreValue      `json:"security_score,omitempty"`
	SecurityTier         string          `json:"security_tier,omitempty"`
	GitHubStars          int             `json:"github_stars,omitempty"`
	MinKubernetesVersion string          `json:"min_kubernetes_version,omitempty"`
	Deprecated           bool            `json:"deprecated,omitempty"`
	SupersededBy         string          `json:"superseded_by,omitempty"`
	RequiredValues       []RequiredValue `json:"required_values,omitempty"`
	Quirks               []string        `json:"quirks,omitempty"`
	Verified             bool            `json:"verified,omitempty"`
	SignatureIdentity    string          `json:"signature_identity,omitempty"`
}

// entrySecrets converts the entry's own secrets block into the shape the
// rest of the code reads. The two types are field-for-field mirrors —
// internal/catalog imports internal/config, so config cannot import back
// and the duplication is forced (see config.AddonSecretRequirement).
func entrySecrets(in []config.AddonSecretRequirement) []SecretRequirement {
	if len(in) == 0 {
		return nil
	}
	out := make([]SecretRequirement, 0, len(in))
	for _, s := range in {
		out = append(out, SecretRequirement{
			Name:        s.Name,
			Description: s.Description,
			RequiredFor: s.RequiredFor,
			Push:        copyPushDefinition(s.Push),
		})
	}
	return out
}

// copyPushDefinition deep-copies a push block so the converted requirement
// never shares its Keys map with the entry it came from — one caller
// editing a copy must not reach back into the parsed catalog.
func copyPushDefinition(in *models.AddonSecretRef) *models.AddonSecretRef {
	if in == nil {
		return nil
	}
	out := models.AddonSecretRef{
		SecretName: in.SecretName,
		Namespace:  in.Namespace,
	}
	if len(in.Keys) > 0 {
		out.Keys = make(map[string]string, len(in.Keys))
		for k, v := range in.Keys {
			out.Keys[k] = v
		}
	}
	return &out
}

// CuratedSecretsForEntry converts a curated entry's secrets list into the
// entry-side shape, so the add-to-catalog operation can copy a
// Marketplace addon's needed-secrets straight into the org's file.
func CuratedSecretsForEntry(in []SecretRequirement) []config.AddonSecretRequirement {
	if len(in) == 0 {
		return nil
	}
	out := make([]config.AddonSecretRequirement, 0, len(in))
	for _, s := range in {
		out = append(out, config.AddonSecretRequirement{
			Name:        s.Name,
			Description: s.Description,
			RequiredFor: s.RequiredFor,
			Push:        copyPushDefinition(s.Push),
		})
	}
	return out
}

// missingDeploymentFields lists, in a fixed order, the deployment fields an
// entry has left blank.
func missingDeploymentFields(e config.AddonCatalogEntry) []string {
	var missing []string
	if e.RepoURL == "" {
		missing = append(missing, "repoURL")
	}
	if e.Chart == "" {
		missing = append(missing, "chart")
	}
	if e.Version == "" {
		missing = append(missing, "version")
	}
	return missing
}

// applyCuratedKnowledge fills a's display fields from the Marketplace's
// curated entry for the same addon. Deployment fields are deliberately
// untouched — the file is the only thing that says what gets deployed.
func applyCuratedKnowledge(a *CatalogAddon, e CatalogEntry) {
	a.Origin = OriginCurated
	a.Description = e.Description
	a.DocsURL = e.DocsURL
	a.Homepage = e.Homepage
	a.SourceURL = e.SourceURL
	a.Maintainers = append([]string(nil), e.Maintainers...)
	a.License = e.License
	a.Category = e.Category
	a.CuratedBy = append([]string(nil), e.CuratedBy...)
	a.SecurityScore = e.SecurityScore
	a.SecurityTier = e.SecurityTier
	a.GitHubStars = e.GitHubStars
	a.MinKubernetesVersion = e.MinKubernetesVersion
	a.Deprecated = e.Deprecated
	a.SupersededBy = e.SupersededBy
	a.RequiredValues = append([]RequiredValue(nil), e.RequiredValues...)
	a.Quirks = append([]string(nil), e.Quirks...)
	a.Verified = e.Verified
	a.SignatureIdentity = e.SignatureIdentity
	if len(a.Secrets) == 0 {
		a.Secrets = append([]SecretRequirement(nil), e.Secrets...)
	}
}

// BuildCatalogView turns the org's catalog.yaml into the per-addon view the
// API serves, keyed by addon name.
//
// The result has exactly one entry per addon in spec.Addons — no more, no
// fewer. curated may be nil; every addon is then OriginInternal with no
// knowledge fields, which is exactly what a build with no embedded
// Marketplace list should show.
//
// It never returns an error. A half-written entry comes back with
// Deployable false and MissingFields naming what is missing, so a list view
// can show the problem next to the addon instead of failing the whole page.
// The write paths that must refuse (enable, migrate) check Deployable, or
// call ValidateCatalogSpec.
func BuildCatalogView(curated *Catalog, spec config.AddonCatalogSpec) map[string]CatalogAddon {
	out := make(map[string]CatalogAddon, len(spec.Addons))

	var byName map[string]CatalogEntry
	if curated != nil {
		entries := curated.Entries()
		byName = make(map[string]CatalogEntry, len(entries))
		for _, e := range entries {
			byName[e.Name] = e
		}
	}

	for name, e := range spec.Addons {
		a := CatalogAddon{
			Name:              name,
			Origin:            OriginInternal,
			RepoURL:           e.RepoURL,
			Chart:             e.Chart,
			Version:           e.Version,
			Namespace:         e.Namespace,
			Settings:          e.Settings,
			AdditionalSources: append([]models.AddonSource(nil), e.AdditionalSources...),
			ExtraHelmValues:   copyStringMap(e.ExtraHelmValues),
			Secrets:           entrySecrets(e.Secrets),
			MissingFields:     missingDeploymentFields(e),
		}
		a.Deployable = len(a.MissingFields) == 0
		if ce, ok := byName[name]; ok {
			applyCuratedKnowledge(&a, ce)
		}
		out[name] = a
	}

	return out
}

// copyStringMap returns a copy so a caller mutating the view can never
// reach back into the parsed file.
func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ValidateCatalogSpec returns a *MissingRequiredFieldError for the first
// entry that cannot be deployed, checked in sorted-name order so the same
// file always reports the same problem. nil means every entry is complete.
//
// This is the gate for anything that is about to WRITE a catalog file —
// the v3 migration converter most of all: a catalog the engine cannot
// render should fail in plain English before the pull request exists, not
// after it merges.
func ValidateCatalogSpec(spec config.AddonCatalogSpec) error {
	names := make([]string, 0, len(spec.Addons))
	for name := range spec.Addons {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if missing := missingDeploymentFields(spec.Addons[name]); len(missing) > 0 {
			return &MissingRequiredFieldError{Addon: name, Field: missing[0]}
		}
	}
	return nil
}

// MergeAddonSettings applies delta's fields onto base, field by field —
// delta wins whenever it sets one. Slice fields (SyncOptions,
// IgnoreDifferences) replace base's slice whole rather than merging
// element by element, matching Sprig's mergeOverwrite treatment of a slice
// as one leaf value (v4 data-file design §3.3 decision D12).
//
// The catalog itself no longer merges anything — every entry is complete.
// This stays because the per-cluster settings layer still overlays the
// addon-wide block at render time, and tests/enginerender cross-checks this
// Go implementation against a real `helm template` run of the identical
// Sprig call so the two can never disagree about what "overlay" means.
func MergeAddonSettings(base, delta *config.AddonSettings) {
	if delta.Namespace != "" {
		base.Namespace = delta.Namespace
	}
	if delta.CreateNamespace != nil {
		base.CreateNamespace = delta.CreateNamespace
	}
	if len(delta.SyncOptions) > 0 {
		base.SyncOptions = delta.SyncOptions
	}
	if len(delta.IgnoreDifferences) > 0 {
		base.IgnoreDifferences = delta.IgnoreDifferences
	}
	if delta.Prune != nil {
		base.Prune = delta.Prune
	}
	if delta.SelfHeal != nil {
		base.SelfHeal = delta.SelfHeal
	}
	if delta.PreserveResourcesOnDeletion != nil {
		base.PreserveResourcesOnDeletion = delta.PreserveResourcesOnDeletion
	}
}

package config

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/invopop/jsonschema"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/schema"
)

// AddonCatalogPath is the repo-relative path of the catalog file: a root
// file called catalog.yaml (design doc 2026-07-31-catalog-approved-model.md
// §2). Single source of truth so callers — the orchestrator write path, the
// API read path, the CLI — never hand-assemble it and drift apart.
//
// It sits at the root, not under a folder: a folder is for when there are
// genuinely many files, and there is exactly one catalog. The name is
// catalog.yaml and not addons.yaml because other files in the repo already
// list addons that RUN, and a root addons.yaml would read as "deployed"
// rather than "allowed".
const AddonCatalogPath = "catalog.yaml"

// AddonCatalogSchemaHeaderV4 is the yaml-language-server header line
// written as the first line of every Sharko-emitted catalog.yaml.
const AddonCatalogSchemaHeaderV4 = "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/catalog.v1.json"

// Secret timing values for AddonSecretRequirement.RequiredFor.
//
// These mirror catalog.SecretRequiredForInstall / ...Runtime exactly and
// MUST keep the same string values. They are re-declared rather than
// imported because internal/catalog already imports internal/config, so the
// arrow cannot point back. See AddonSecretRequirement.
const (
	// SecretRequiredForInstall means the chart will not come up at all
	// until this secret exists.
	SecretRequiredForInstall = "install"
	// SecretRequiredForRuntime means the secret is consumed later, by the
	// running workload rather than by the install.
	SecretRequiredForRuntime = "runtime"
)

// AddonSecretRequirement documents ONE credential an addon needs, in plain
// English, and when it is needed.
//
// It is a field-for-field mirror of catalog.SecretRequirement — same yaml
// and json keys, same RequiredFor values, same "empty means install"
// default. The duplication is forced by package direction: internal/catalog
// imports internal/config, so internal/config cannot import internal/catalog
// back. Keep the two in lockstep; a field added to one belongs in the other
// the same day. (Unifying them means moving the type down into
// internal/models, which both packages already import — worth doing, but it
// touches the curated catalog loader and belongs in its own change.)
//
// The mirror is what lets an addon be copied out of the Marketplace into
// the catalog with its secrets list intact, and what gives a v3 catalog's
// secrets: block a real home when it migrates.
type AddonSecretRequirement struct {
	// Name is a short human label for the secret. It is not necessarily a
	// literal Kubernetes Secret name — addons differ in how they expect a
	// credential to be supplied.
	Name string `json:"name" yaml:"name"`
	// Description says what the secret is for and, where it helps, how it
	// is normally wired in (env var, mounted file, IRSA role).
	Description string `json:"description" yaml:"description"`
	// RequiredFor is SecretRequiredForInstall or SecretRequiredForRuntime.
	// Empty means install — the stricter reading, so an entry written
	// before anyone classified it keeps blocking exactly as it did.
	RequiredFor string `json:"required_for,omitempty" yaml:"required_for,omitempty"`
}

// EffectiveRequiredFor returns RequiredFor, defaulting empty to
// SecretRequiredForInstall so the "unclassified stays strict" rule lives in
// one place. Mirrors catalog.SecretRequirement.EffectiveRequiredFor.
func (s AddonSecretRequirement) EffectiveRequiredFor() string {
	if s.RequiredFor == "" {
		return SecretRequiredForInstall
	}
	return s.RequiredFor
}

// AddonSettings is the FULL seven-field v1 settings schema (v4 data-file
// design §3.2) as it appears inside an AddonCatalog entry: the six
// per-Application fields PLUS PreserveResourcesOnDeletion, the one
// per-ApplicationSet field. This is fleet-wide, addon-wide data — the
// engine builds exactly one ApplicationSet per addon covering every
// cluster that runs it, so PreserveResourcesOnDeletion (a
// syncPolicy-level field on that ApplicationSet) can only be set here,
// never per cluster. See models.ClusterAddonsAddonSettings for the
// six-field sibling used in clusters/<name>.yaml, which deliberately
// omits this field so a hand-authored per-cluster override of it fails
// JSON Schema validation.
type AddonSettings struct {
	Namespace         string                   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	CreateNamespace   *bool                    `json:"createNamespace,omitempty" yaml:"createNamespace,omitempty"`
	SyncOptions       []string                 `json:"syncOptions,omitempty" yaml:"syncOptions,omitempty"`
	IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences,omitempty" yaml:"ignoreDifferences,omitempty"`
	Prune             *bool                    `json:"prune,omitempty" yaml:"prune,omitempty"`
	SelfHeal          *bool                    `json:"selfHeal,omitempty" yaml:"selfHeal,omitempty"`
	// PreserveResourcesOnDeletion: if the Application is ever removed,
	// leave the running workloads alone. Addon-wide only — see the type
	// doc comment above.
	PreserveResourcesOnDeletion *bool `json:"preserveResourcesOnDeletion,omitempty" yaml:"preserveResourcesOnDeletion,omitempty"`
}

// AddonCatalogEntry is one entry in AddonCatalogSpec.Addons, keyed by addon
// name. It is a FULL, self-contained entry: everything needed to deploy the
// addon is written here, nothing is inherited from a list that ships inside
// Sharko (design doc 2026-07-31-catalog-approved-model.md §1).
//
// That is the point of the format. Adding an addon to the catalog copies
// the whole entry in, so the pull-request reviewer sees exactly what is
// entering the org, and the repo on its own tells the whole story — you can
// read what your fleet is allowed to run without Sharko running. It also
// removes a real bug class: when the server and the engine chart each
// carried their own copy of a shipped list, the two could drift and
// disagree about what an addon actually was. Now both read this file.
//
// RepoURL, Chart and Version are what make an entry deployable. They are
// deliberately NOT required by the JSON Schema: a half-written entry in an
// open pull request should come back with Sharko's own plain-English
// message about what is missing, not a schema violation. The deployability
// check belongs with the code that reads the catalog.
type AddonCatalogEntry struct {
	// RepoURL: a Helm repo URL or an oci:// reference.
	RepoURL string `json:"repoURL,omitempty" yaml:"repoURL,omitempty"`
	// Chart: the chart name inside RepoURL.
	Chart string `json:"chart,omitempty" yaml:"chart,omitempty"`
	// Version: the fleet-wide default chart version for this addon.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Namespace: default namespace for this addon. Falls back to the
	// addon name.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	// Settings: fleet-wide deployment settings for this addon (all
	// seven v1 fields — see AddonSettings doc comment).
	Settings *AddonSettings `json:"settings,omitempty" yaml:"settings,omitempty"`
	// Secrets: the credentials this addon needs before it will work, in
	// plain English, split by WHEN they are needed. See
	// AddonSecretRequirement.
	Secrets []AddonSecretRequirement `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	// AdditionalSources: extra Helm sources deployed alongside the main
	// chart. Reuses the existing models.AddonSource type rather than
	// declaring a new one.
	AdditionalSources []models.AddonSource `json:"additionalSources,omitempty" yaml:"additionalSources,omitempty"`
	// ExtraHelmValues: extra Helm parameters as name/value pairs.
	ExtraHelmValues map[string]string `json:"extraHelmValues,omitempty" yaml:"extraHelmValues,omitempty"`
}

// AddonCatalogSpec is the payload of catalog.yaml (kind: AddonCatalog) —
// the addons this org has approved for its clusters, and nothing else.
//
// Addons is required but may be an empty map ({}). A fresh repo has zero
// approved addons on purpose: nothing runs in the org that somebody did not
// put there, from minute one.
type AddonCatalogSpec struct {
	Addons map[string]AddonCatalogEntry `json:"addons" yaml:"addons"`
}

// JSONSchemaExtend constrains the KEYS of the addons map in the generated
// JSON Schema (invopop/jsonschema calls this hook while reflecting the
// type; cmd/schema-gen is the only caller, and internal/schema's runtime
// validator enforces the emitted schema on every read AND every write).
//
// Without this, `addons:` accepts any string as an addon name — including
// "../../engine" — and a hand-authored catalog.yaml could name an addon
// whose name later becomes a git path segment (values/global/<addon>.yaml)
// and a Kubernetes label key (addons.sharko.dev/<addon>). The API edge
// rejects such a name on the write path; propertyNames is what rejects it
// on the READ path, for a file somebody committed by hand.
//
// The pattern is models.ResourceNamePattern — the same literal the API edge
// and the orchestrator's path builders compile, so a name that is legal in
// one place is legal in all of them.
func (AddonCatalogSpec) JSONSchemaExtend(base *jsonschema.Schema) {
	addons, ok := base.Properties.Get("addons")
	if !ok || addons == nil {
		return
	}
	addons.PropertyNames = &jsonschema.Schema{Pattern: models.ResourceNamePattern}
}

// addonCatalogLabel names the file in parser errors.
const addonCatalogLabel = "catalog.yaml"

// LoadAddonCatalog parses the on-disk bytes of a catalog.yaml document and
// returns its spec.
//
// FLAT only: apiVersion + kind + a top-level addons: map, no spec: wrapper
// (design doc §9). There is no legacy shape to accept — a wrapped form of
// THIS file never shipped, so a spec:-wrapped body is a mistake worth
// reporting rather than a repo Sharko has to keep working.
//
// Hard error, never a silent "zero addons", on a non-Sharko or wrong-kind
// body: an empty catalog means "this org approved nothing", which would
// switch every addon off across the whole fleet.
func LoadAddonCatalog(body []byte) (AddonCatalogSpec, error) {
	spec, err := schema.DecodeFlat[AddonCatalogSpec](body, schema.KindAddonCatalog, addonCatalogLabel)
	if err != nil {
		return AddonCatalogSpec{}, err
	}

	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.SchemaKeyAddonCatalogV4, body); err != nil {
			var vf *schema.ValidationFailure
			if errors.As(err, &vf) {
				schema.LogValidationFailure(addonCatalogLabel, vf)
			}
			return AddonCatalogSpec{}, fmt.Errorf("validating %s: %w", addonCatalogLabel, err)
		}
	}
	return spec, nil
}

// SaveAddonCatalog renders spec as a catalog.yaml document, schema header
// line first.
func SaveAddonCatalog(spec AddonCatalogSpec) ([]byte, error) {
	body, err := schema.EncodeFlat(schema.KindAddonCatalog, spec)
	if err != nil {
		return nil, err
	}

	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.SchemaKeyAddonCatalogV4, body); err != nil {
			var vf *schema.ValidationFailure
			if errors.As(err, &vf) {
				schema.LogValidationFailure(addonCatalogLabel+" (write)", vf)
			}
			return nil, fmt.Errorf("validating %s before write: %w", addonCatalogLabel, err)
		}
	}

	var buf bytes.Buffer
	buf.WriteString(AddonCatalogSchemaHeaderV4)
	buf.WriteByte('\n')
	buf.Write(body)
	return buf.Bytes(), nil
}

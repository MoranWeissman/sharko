package config

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/schema"
)

// AddonCatalogDeltaFilename is the canonical v4 catalog-delta filename
// (design doc §2.3 — "catalog/addons.yaml"). Distinct from
// AddonCatalogFilename ("addons-catalog.yaml"), which stays the v3
// filename for the v3 AddonCatalog kind. Both formats coexist until
// Wave 2's migration (design doc §8).
const AddonCatalogDeltaFilename = "addons.yaml"

// AddonCatalogDeltaPath is the full repo-relative path to the v4 catalog
// delta file — design doc §2.3's worked example, "catalog/addons.yaml".
// Single source of truth so callers (the orchestrator write path, the API
// merged-view read path, CLI) never hand-assemble the directory + filename
// and risk drifting from each other.
const AddonCatalogDeltaPath = "catalog/" + AddonCatalogDeltaFilename

// AddonCatalogDeltaSchemaHeader is the yaml-language-server header line
// written as the first line of every Sharko-emitted catalog/addons.yaml
// file. Mirrors AddonCatalogSchemaHeader's pattern.
const AddonCatalogDeltaSchemaHeader = "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addon-catalog-delta.v1.json"

// AddonSettings is the FULL seven-field v1 settings schema (design doc
// §3.2) as it appears inside an AddonCatalogDelta entry: the six
// per-Application fields PLUS PreserveResourcesOnDeletion, the one
// per-ApplicationSet field. This is fleet-wide, addon-wide data — the
// engine builds exactly one ApplicationSet per addon covering every
// cluster that runs it, so PreserveResourcesOnDeletion (a
// syncPolicy-level field on that ApplicationSet) can only be set here,
// never per cluster. See models.ClusterAssignmentAddonSettings for the
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

// AddonCatalogDeltaEntry is one entry in an AddonCatalogDeltaSpec.Addons
// map, keyed by addon name (design doc §2.3).
//
// Requiredness note (design doc, "The note on required"): for an addon
// Sharko already ships, every field here is optional — the user writes
// only what they're changing. For an in-house chart, RepoURL, Chart, and
// Version are effectively required, but that rule can only be checked
// AFTER merging with the shipped catalog (to know whether an addon is
// "already shipped" at all) — data this lane's static JSON Schema has no
// access to (the shipped catalog lives inside the engine chart, wired up
// by Story 2.4/2.2). None of RepoURL/Chart/Version/Namespace are marked
// required in this struct or its generated schema; the merge-time check
// is deliberately deferred to whichever story has the shipped catalog in
// hand.
type AddonCatalogDeltaEntry struct {
	// RepoURL: a Helm repo URL or an oci:// reference. Required (after
	// merge) only for addons with no shipped-catalog entry.
	RepoURL string `json:"repoURL,omitempty" yaml:"repoURL,omitempty"`
	// Chart: the chart name inside RepoURL. Required (after merge) only
	// for addons with no shipped-catalog entry.
	Chart string `json:"chart,omitempty" yaml:"chart,omitempty"`
	// Version: the fleet-wide default version for this addon. Required
	// (after merge) only for addons with no shipped-catalog entry.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Namespace: default namespace for this addon. Falls back to the
	// addon name.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	// Settings: fleet-wide deployment settings for this addon (all
	// seven v1 fields — see AddonSettings doc comment).
	Settings *AddonSettings `json:"settings,omitempty" yaml:"settings,omitempty"`
	// AdditionalSources: extra Helm sources deployed alongside the main
	// chart. Carried over from v3 unchanged (design doc §2.3) — reuses
	// the existing models.AddonSource type rather than declaring a new
	// one.
	AdditionalSources []models.AddonSource `json:"additionalSources,omitempty" yaml:"additionalSources,omitempty"`
	// ExtraHelmValues: extra Helm parameters as name/value pairs.
	// Carried over from v3 unchanged.
	ExtraHelmValues map[string]string `json:"extraHelmValues,omitempty" yaml:"extraHelmValues,omitempty"`
}

// AddonCatalogDeltaSpec is the spec block of a catalog/addons.yaml
// envelope (kind: AddonCatalogDelta). "Delta" means only your changes:
// this file never holds a copy of the addons Sharko ships (design doc
// §2.3). Addons is required but may be an empty map ({}) — design doc
// decision D16, "missing means empty".
type AddonCatalogDeltaSpec struct {
	Addons map[string]AddonCatalogDeltaEntry `json:"addons" yaml:"addons"`
}

// AddonCatalogDeltaDoc is the on-disk shape for an enveloped
// catalog/addons.yaml (apiVersion: sharko.dev/v1, kind: AddonCatalogDelta).
// It is the canonical Save target; the reader only ever accepts this
// enveloped shape — AddonCatalogDelta is a brand-new v4 kind, distinct
// from the v3 AddonCatalog kind (design doc decision D5), with no legacy
// bare-YAML precedent.
type AddonCatalogDeltaDoc = schema.Envelope[AddonCatalogDeltaSpec]

// AddonCatalogDeltaMetadataName is the conventional metadata.name value
// emitted by SaveAddonCatalogDelta. Mirrors the design doc's worked
// example (§2.3): `metadata: name: addon-catalog-delta`.
const AddonCatalogDeltaMetadataName = "addon-catalog-delta"

// LoadAddonCatalogDelta parses the on-disk bytes of a catalog/addons.yaml
// document and returns its spec. Mirrors models.LoadClusterAssignment:
// no legacy bare-YAML branch, hard error (never a silent "zero addons"
// result) on a non-enveloped or wrong-kind body.
func LoadAddonCatalogDelta(body []byte) (AddonCatalogDeltaSpec, error) {
	enveloped, err := schema.IsEnveloped(body)
	if err != nil {
		return AddonCatalogDeltaSpec{}, fmt.Errorf("parsing addon catalog delta: %w", err)
	}
	if !enveloped {
		return AddonCatalogDeltaSpec{}, fmt.Errorf(
			"parsing addon catalog delta: not a Sharko-enveloped document (apiVersion missing or not %s)",
			schema.APIVersion,
		)
	}

	var doc AddonCatalogDeltaDoc
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return AddonCatalogDeltaSpec{}, fmt.Errorf("parsing addon catalog delta envelope: %w", err)
	}
	if doc.Kind != schema.KindAddonCatalogDelta {
		return AddonCatalogDeltaSpec{}, fmt.Errorf(
			"addon catalog delta envelope kind %q, expected %q",
			doc.Kind, schema.KindAddonCatalogDelta,
		)
	}

	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindAddonCatalogDelta, body); err != nil {
			var vf *schema.ValidationFailure
			if errors.As(err, &vf) {
				schema.LogValidationFailure("addon-catalog-delta", vf)
			}
			return AddonCatalogDeltaSpec{}, fmt.Errorf("validating addon catalog delta envelope: %w", err)
		}
	}
	return doc.Spec, nil
}

// SaveAddonCatalogDelta renders spec as an enveloped catalog/addons.yaml
// document.
func SaveAddonCatalogDelta(spec AddonCatalogDeltaSpec) ([]byte, error) {
	doc := AddonCatalogDeltaDoc{
		APIVersion: schema.APIVersion,
		Kind:       schema.KindAddonCatalogDelta,
		Metadata:   schema.Metadata{Name: AddonCatalogDeltaMetadataName},
		Spec:       spec,
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshalling addon catalog delta envelope: %w", err)
	}

	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindAddonCatalogDelta, body); err != nil {
			var vf *schema.ValidationFailure
			if errors.As(err, &vf) {
				schema.LogValidationFailure("addon-catalog-delta (write)", vf)
			}
			return nil, fmt.Errorf("validating addon catalog delta before write: %w", err)
		}
	}

	var buf bytes.Buffer
	buf.WriteString(AddonCatalogDeltaSchemaHeader)
	buf.WriteByte('\n')
	buf.Write(body)
	return buf.Bytes(), nil
}

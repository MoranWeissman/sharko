package models

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/schema"
)

// ClusterAddonsSchemaHeader is the yaml-language-server header line
// written as the first line of every Sharko-emitted cluster-addons/<name>.yaml
// file. Mirrors ManagedClustersSchemaHeader's pattern.
const ClusterAddonsSchemaHeader = "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/cluster-addons.v1.json"

// ClusterAddonsAddonSettings is the PER-APPLICATION tier of the v1
// settings schema (docs/design/2026-07-30-v4-data-file-format.md §3.2) as
// it appears inside a cluster-addons/<name>.yaml file: six of the seven v1
// settings fields — every one EXCEPT PreserveResourcesOnDeletion.
//
// PreserveResourcesOnDeletion is deliberately NOT a field on this struct.
// It lives on the ApplicationSet, not the per-cluster Application (the
// engine builds one ApplicationSet per addon covering every cluster that
// runs it), so it cannot vary per cluster. This struct's absence of the
// field is the enforcement mechanism: invopop/jsonschema defaults every
// reflected struct to `additionalProperties: false`, so a hand-authored
// cluster-addons/*.yaml with `settings.preserveResourcesOnDeletion` fails JSON
// Schema validation outright (belt) — cmd/sharko's validate-config CLI
// additionally detects this specific key ahead of the generic schema
// error and reports the contract's plain-English redirect to
// catalog.yaml (suspenders). See config.AddonSettings for the
// seven-field sibling used in AddonCatalog entries, where the field
// IS legal (fleet-wide, addon-wide).
type ClusterAddonsAddonSettings struct {
	// Namespace overrides which namespace this addon installs into on
	// this cluster. Falls back to the addon's shipped/delta namespace,
	// then to the addon name.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	// CreateNamespace lets Argo CD create Namespace if it is missing.
	// Pointer so "explicitly false" is distinguishable from "not set"
	// (the engine's default is true).
	CreateNamespace *bool `json:"createNamespace,omitempty" yaml:"createNamespace,omitempty"`
	// SyncOptions replaces (never merges with) the shipped/delta list —
	// design doc §3.3 decision D12: there is no honest way to know
	// whether a per-cluster list was meant to add to or replace the
	// fleet-wide one, so it always replaces.
	SyncOptions []string `json:"syncOptions,omitempty" yaml:"syncOptions,omitempty"`
	// IgnoreDifferences replaces (never merges with) the shipped/delta
	// list, same reasoning as SyncOptions. Modelled as
	// []map[string]interface{} to mirror the existing v3
	// AddonCatalogEntry.IgnoreDifferences shape (group/kind/jsonPointers
	// objects) rather than inventing a new typed struct.
	IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences,omitempty" yaml:"ignoreDifferences,omitempty"`
	// Prune: when something disappears from git, delete it from the
	// cluster too. Pointer for the same explicit-false reason as
	// CreateNamespace.
	Prune *bool `json:"prune,omitempty" yaml:"prune,omitempty"`
	// SelfHeal: Argo CD's own Application-level self-heal (NOT Sharko's
	// label self-heal loop — see design doc §3.2 "two words that both
	// mean self-heal"). Pointer for the same explicit-false reason.
	SelfHeal *bool `json:"selfHeal,omitempty" yaml:"selfHeal,omitempty"`
}

// ClusterAddonsAddon is one entry in a ClusterAddonsSpec.Addons
// map, keyed by addon name. Enabled and Version follow the precedence
// chain in design doc §3.3: Version absent means "follow the catalog
// default" — the only per-cluster version pin location in the whole v4
// format (§2.1 "Where per-cluster version pins live, exactly").
type ClusterAddonsAddon struct {
	// Enabled: true deploys the addon on this cluster. false keeps the
	// entry (and its Settings/Version) but stops deploying — switching
	// it back on later is a one-word change. Required, no omitempty:
	// an addon entry with no enabled key is a schema violation, not an
	// implicit false.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Version pins the chart version for THIS cluster only. Empty means
	// "use whatever catalog.yaml (or the shipped catalog) says".
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Settings holds this cluster's per-Application overrides for this
	// addon. Pointer (not a plain struct) so "no settings block at all"
	// is distinguishable from "an all-zero-value settings block" —
	// encoding/json's `omitempty` never omits a non-pointer struct.
	Settings *ClusterAddonsAddonSettings `json:"settings,omitempty" yaml:"settings,omitempty"`
}

// ClusterAddonsSpec is the spec block of a cluster-addons/<cluster-name>.yaml
// envelope (kind: ClusterAddons). One file per cluster; the file name
// (minus .yaml) MUST equal Cluster — that invariant is enforced by
// cmd/sharko's validate-config CLI (which has the file path in hand),
// not by this type or its JSON Schema (neither has access to the
// filename).
type ClusterAddonsSpec struct {
	// Cluster is the cluster name. Must equal the file's basename
	// (without .yaml). Required.
	Cluster string `json:"cluster" yaml:"cluster"`
	// Addons is keyed by addon name so an addon can only appear once,
	// a pull request touching one addon's config is a one-block diff,
	// and the engine can look an addon up directly. Required, but may
	// be an empty map ({}).
	Addons map[string]ClusterAddonsAddon `json:"addons" yaml:"addons"`
}

// clusterAddonsLabel names the file in parser errors.
const clusterAddonsLabel = "cluster assignment"

// LoadClusterAddons parses the on-disk bytes of a
// cluster-addons/<cluster-name>.yaml document and returns its spec.
//
// FLAT only: apiVersion + kind + cluster: and addons: at the top level, no
// spec: wrapper (design doc 2026-07-31-catalog-approved-model.md §9).
// ClusterAddons is a v4-only kind and v4 has not shipped, so there is no
// older shape of this file anywhere to stay compatible with.
//
// A body that is not a Sharko document, or names the wrong kind, is a hard
// error — never a silent "zero addons" fallthrough (the same failure class
// internal/schema/envelope.go's UnknownSharkoAPIVersionError guards against
// elsewhere: an empty read here reads as "this cluster runs nothing").
func LoadClusterAddons(body []byte) (ClusterAddonsSpec, error) {
	spec, err := schema.DecodeFlat[ClusterAddonsSpec](body, schema.KindClusterAddons, clusterAddonsLabel)
	if err != nil {
		return ClusterAddonsSpec{}, err
	}

	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindClusterAddons, body); err != nil {
			var vf *schema.ValidationFailure
			if errors.As(err, &vf) {
				schema.LogValidationFailure("cluster-addons", vf)
			}
			return ClusterAddonsSpec{}, fmt.Errorf("validating %s: %w", clusterAddonsLabel, err)
		}
	}
	return spec, nil
}

// SaveClusterAddons renders spec as a cluster-addons/<cluster-name>.yaml
// document: apiVersion, kind, then cluster: and addons: at the top level.
//
// The file's identity is its NAME on disk — cluster-addons/prod-eu.yaml is the
// cluster called prod-eu — with spec.cluster inside repeating it so a
// mismatch is catchable. There is no metadata.name to keep in step with
// either of them any more, which is one fewer place for the three to
// disagree.
func SaveClusterAddons(spec ClusterAddonsSpec) ([]byte, error) {
	body, err := schema.EncodeFlat(schema.KindClusterAddons, spec)
	if err != nil {
		return nil, err
	}

	// Validate-before-write safety net — same stance as
	// SaveManagedClusters: a failure here means a Sharko bug or a bad
	// in-memory spec, not legitimate user data, so the write is refused
	// rather than committing something that would fail validate-config
	// downstream.
	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindClusterAddons, body); err != nil {
			var vf *schema.ValidationFailure
			if errors.As(err, &vf) {
				schema.LogValidationFailure("cluster-addons (write)", vf)
			}
			return nil, fmt.Errorf("validating cluster assignment before write: %w", err)
		}
	}

	var buf bytes.Buffer
	buf.WriteString(ClusterAddonsSchemaHeader)
	buf.WriteByte('\n')
	buf.Write(body)
	return buf.Bytes(), nil
}

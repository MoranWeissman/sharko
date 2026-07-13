package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/schema"
	"gopkg.in/yaml.v3"
)

// File-naming constants for the addon catalog. `addons-catalog.yaml`
// (plural) is the single canonical name across the codebase.
const (
	// AddonCatalogFilename is the canonical addon-catalog filename. All
	// Sharko readers and writers use this name.
	AddonCatalogFilename = "addons-catalog.yaml"

	// AddonCatalogSchemaHeader is the editor schema directive emitted as the
	// first line of every Sharko-written addons-catalog.yaml. yaml-language-server
	// (VS Code / IntelliJ) uses it for inline validation + autocomplete.
	AddonCatalogSchemaHeader = "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json"
)

// clusterAddonsFile represents the legacy bare-YAML structure of
// managed-clusters.yaml (top-level clusters: key, no envelope). It is
// also the parse target for the spec block extracted from an enveloped
// document — see ParseClusterAddons for the envelope routing.
type clusterAddonsFile struct {
	Clusters []clusterEntry `yaml:"clusters"`
}

// envelopedClusterAddonsFile is the parse target when the document is
// enveloped (apiVersion: sharko.dev/v1, kind: ManagedClusters). The spec
// field re-uses clusterAddonsFile so the downstream label normalisation
// is identical between legacy and enveloped reads.
type envelopedClusterAddonsFile struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Spec       clusterAddonsFile `yaml:"spec"`
}

type clusterEntry struct {
	Name       string      `yaml:"name"`
	SecretPath string      `yaml:"secretPath,omitempty"`
	Labels     interface{} `yaml:"labels"` // Can be map[string]string or []interface{} (empty)
	Region     string      `yaml:"region,omitempty"`
	// ConnectionManagedBy: "" / "sharko" = Sharko owns the ArgoCD cluster
	// Secret (default); "user" = self-managed connection, Sharko syncs addon
	// labels only (V2-cleanup-57.2). Mirrors models.ManagedClusterEntry.
	ConnectionManagedBy string `yaml:"connectionManagedBy,omitempty"`
	// CredsSource: where the cluster's credentials live (V2-cleanup-60.4):
	// inline-kubeconfig / secret-kubeconfig / eks-token, or "" for records
	// that predate the field. Mirrors models.ManagedClusterEntry.
	CredsSource string `yaml:"credsSource,omitempty"`
	// RoleARN: per-cluster IAM role for EKS token minting
	// (V2-cleanup-62.2), or "" for records that predate the field.
	// Mirrors models.ManagedClusterEntry.
	RoleARN string `yaml:"roleArn,omitempty"`
}

// AddonCatalogSpec is the spec body of an enveloped addons-catalog.yaml.
// It holds the same payload as the legacy bare-YAML file (the
// `applicationsets` list of AddonCatalogEntry). The envelope wraps this
// struct in an apiVersion/kind/metadata frame so the file can be
// schema-validated and is editor-friendly via the published JSON Schema.
//
// The YAML field name must remain `applicationsets` (lowercase, plural)
// so legacy bare-YAML files keep deserializing into the same shape —
// only the outer envelope is new.
type AddonCatalogSpec struct {
	ApplicationSets []models.AddonCatalogEntry `json:"applicationsets" yaml:"applicationsets"`
}

// DefaultAddonsSpec is the spec body of an enveloped default-addons.yaml.
// It holds the list of addon names that are auto-enabled when a cluster
// is registered/adopted without explicit addon selection. V3-Phase-2 moves
// the source of truth from the connection's gitops.default_addons string
// to a git file; this is the enveloped shape.
type DefaultAddonsSpec struct {
	Addons []string `json:"addons" yaml:"addons"`
}

// addonsCatalogFile is the legacy on-disk shape of addons-catalog.yaml
// — a bare YAML document with `applicationsets:` at the top level.
// The reader still accepts this shape; the writer always emits the
// enveloped form via MarshalAddonCatalog.
type addonsCatalogFile struct {
	ApplicationSets []models.AddonCatalogEntry `yaml:"applicationsets"`
}

// clusterValuesFile represents a per-cluster values file.
type clusterValuesFile struct {
	ClusterGlobalValues map[string]interface{} `yaml:"clusterGlobalValues"`
}

// RepoConfig holds the fully parsed configuration from the Git repository.
type RepoConfig struct {
	Clusters []models.Cluster
	Addons   []models.AddonCatalogEntry
}

// Parser parses the argocd-cluster-addons repo configuration files.
type Parser struct{}

// NewParser creates a new config parser.
func NewParser() *Parser {
	return &Parser{}
}

// ParseClusterAddons parses managed-clusters.yaml (alias: cluster-addons.yaml)
// content into a flat []models.Cluster suitable for the service / orchestrator
// / reconciler consumers.
//
// Accepts BOTH the legacy bare-YAML shape AND the envelope shape
// (apiVersion: sharko.dev/v1, kind: ManagedClusters). Detection is
// delegated to schema.IsEnveloped so the routing primitive is shared
// with the addon-catalog reader. The legacy reader is kept rather than
// fully delegated to models.LoadManagedClusters because the
// label-normalisation logic (interface{} → map[string]string, see
// parseLabels) lives in this package.
//
// On the ENVELOPED branch, the body is JSON-Schema-validated against
// docs/schemas/managed-clusters.v1.json BEFORE yaml.Unmarshal.
// Validation failures return *schema.ValidationFailure to the caller
// and emit a slog.Error with the full violation list. Legacy bare YAML
// is NOT validated — same back-compat contract as
// models.LoadManagedClusters.
func (p *Parser) ParseClusterAddons(data []byte) ([]models.Cluster, error) {
	enveloped, err := schema.IsEnveloped(data)
	if err != nil {
		return nil, fmt.Errorf("parsing managed-clusters: %w", err)
	}

	var file clusterAddonsFile
	if enveloped {
		// Wrong-kind check FIRST — same precedence as
		// models.LoadManagedClusters so the actionable "wrong file
		// handed to wrong loader" error surfaces ahead of any generic
		// schema violation. Downstream tooling (reconciler audit log,
		// validate-config CLI) depends on this format.
		var env envelopedClusterAddonsFile
		if err := yaml.Unmarshal(data, &env); err != nil {
			return nil, fmt.Errorf("parsing managed-clusters envelope: %w", err)
		}
		if env.Kind != schema.KindManagedClusters {
			return nil, fmt.Errorf(
				"managed-clusters envelope kind %q, expected %q",
				env.Kind, schema.KindManagedClusters,
			)
		}

		// Validate AFTER wrong-kind check. Validator failures are
		// surfaced as the canonical "validating managed-clusters
		// envelope" wrapper so callers can errors.As into
		// *schema.ValidationFailure (the validate-config CLI
		// prefix-matches this string to render a user-friendly
		// message).
		if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
			if err := validator.Validate(schema.KindManagedClusters, data); err != nil {
				var vf *schema.ValidationFailure
				if errors.As(err, &vf) {
					schema.LogValidationFailure("managed-clusters.yaml", vf)
				}
				return nil, fmt.Errorf("validating managed-clusters envelope: %w", err)
			}
		}
		file = env.Spec
	} else {
		// Legacy bare YAML — back-compat path, no validation by design.
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parsing managed-clusters: %w", err)
		}
	}

	clusters := make([]models.Cluster, 0, len(file.Clusters))
	for _, entry := range file.Clusters {
		cluster := models.Cluster{
			Name:                entry.Name,
			SecretPath:          entry.SecretPath,
			Labels:              parseLabels(entry.Labels),
			Region:              entry.Region,
			ConnectionManagedBy: entry.ConnectionManagedBy,
			CredsSource:         entry.CredsSource,
			RoleARN:             entry.RoleARN,
		}
		clusters = append(clusters, cluster)
	}

	return clusters, nil
}

// parseLabels handles the labels field which can be a map or an empty array.
func parseLabels(raw interface{}) map[string]string {
	if raw == nil {
		return map[string]string{}
	}

	switch v := raw.(type) {
	case map[string]interface{}:
		labels := make(map[string]string, len(v))
		for key, val := range v {
			labels[key] = fmt.Sprintf("%v", val)
		}
		return labels
	case []interface{}:
		// Empty array means no labels
		return map[string]string{}
	default:
		return map[string]string{}
	}
}

// ParseAddonsCatalog parses an addons-catalog.yaml document. The reader
// accepts both the legacy bare-YAML shape (top-level `applicationsets:`
// array) AND the enveloped shape (apiVersion/kind/metadata/spec — see
// internal/schema).
//
// Detection is byte-level via schema.IsEnveloped, which only inspects the
// top-level apiVersion field. When the body declares an apiVersion of
// sharko.dev/v1 but a kind other than AddonCatalog, this function returns an
// error — a foreign envelope (e.g. ManagedClusters) is a structural bug, not
// a legacy file, and silently treating it as bare YAML would mask the
// mismatch.
//
// Returns the flat slice of AddonCatalogEntry for consistency with every
// existing caller. The envelope's metadata (Name, Annotations) is not yet
// surfaced anywhere in Sharko's runtime; later stories can extend the API if
// needed.
//
// On the ENVELOPED branch, the body is JSON-Schema-validated against
// docs/schemas/addons-catalog.v1.json BEFORE yaml.Unmarshal. Validation
// failures return *schema.ValidationFailure to the caller and emit a
// slog.Error with the full violation list. Legacy bare YAML is NOT
// validated — same back-compat contract as ParseClusterAddons.
func (p *Parser) ParseAddonsCatalog(data []byte) ([]models.AddonCatalogEntry, error) {
	enveloped, err := schema.IsEnveloped(data)
	if err != nil {
		// IsEnveloped surfaces YAML-parse errors so we can distinguish "broken
		// file" from "intentionally legacy". Propagate with the same context
		// shape callers already log against.
		return nil, fmt.Errorf("parsing addons-catalog.yaml: %w", err)
	}

	if enveloped {
		// Wrong-kind check FIRST — same precedence as
		// models.LoadManagedClusters and ParseClusterAddons. Pinned
		// by TestLoadCatalog_EnvelopedWrongKind_Reject.
		var doc schema.Envelope[AddonCatalogSpec]
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("parsing addons-catalog.yaml: %w", err)
		}
		if doc.Kind != schema.KindAddonCatalog {
			return nil, fmt.Errorf(
				"parsing addons-catalog.yaml: envelope kind %q, expected %q",
				doc.Kind, schema.KindAddonCatalog)
		}

		// Validate AFTER wrong-kind check.
		if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
			if err := validator.Validate(schema.KindAddonCatalog, data); err != nil {
				var vf *schema.ValidationFailure
				if errors.As(err, &vf) {
					schema.LogValidationFailure("addons-catalog.yaml", vf)
				}
				return nil, fmt.Errorf("validating addons-catalog envelope: %w", err)
			}
		}
		return doc.Spec.ApplicationSets, nil
	}

	// Legacy bare-YAML path — no validation by design.
	var file addonsCatalogFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing addons-catalog.yaml: %w", err)
	}
	return file.ApplicationSets, nil
}

// MarshalAddonCatalog serializes the supplied entries to the enveloped
// on-disk shape, prefixed with the yaml-language-server schema header.
// This is the canonical writer — every Sharko code path that updates
// the addon catalog SHOULD route through here so the resulting file
// stays schema-conformant.
//
// The output is deterministic: the schema header is always the first line,
// followed by the marshalled envelope. yaml.v3 marshals struct fields in
// declaration order, so the envelope fields appear as
// apiVersion / kind / metadata / spec.
//
// metadataName is the value emitted under metadata.name; callers usually pass
// "addon-catalog" but the parameter exists so future tooling (operator mode,
// multi-tenant catalogs) can stamp a different identifier without touching
// this function.
func MarshalAddonCatalog(metadataName string, entries []models.AddonCatalogEntry) ([]byte, error) {
	if metadataName == "" {
		metadataName = "addon-catalog"
	}
	// Normalize a nil slice to [] so the YAML always renders `applicationsets: []`
	// instead of `applicationsets: null` — matches the bootstrap template's
	// bootstrap template shape.
	if entries == nil {
		entries = []models.AddonCatalogEntry{}
	}

	doc := schema.Envelope[AddonCatalogSpec]{
		APIVersion: schema.APIVersion,
		Kind:       schema.KindAddonCatalog,
		Metadata:   schema.Metadata{Name: metadataName},
		Spec:       AddonCatalogSpec{ApplicationSets: entries},
	}

	body, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, fmt.Errorf("marshalling addon-catalog envelope: %w", err)
	}

	// Validate-before-commit safety net (V2-cleanup-22, Part 1 / decisions
	// #1+#2). MarshalAddonCatalog is the single choke point every
	// addon-catalog writer funnels through (the gitops catalog mutators in
	// internal/gitops/yaml_mutator_catalog.go all end in this call), so
	// validating here means a new caller cannot bypass the gate. We run the
	// SAME embedded validator the readers use against the marshalled bytes
	// BEFORE any commit/PR happens — a failure means a Sharko bug or a
	// genuinely bad in-memory entry set, not legitimate user data, so we
	// FAIL the operation and emit nothing. By construction Sharko-generated
	// content passes; this only fires on a regression. If DefaultValidator
	// itself fails to compile (a build-time bug, never runtime data) we do
	// not block the write — same stance the reader paths take.
	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindAddonCatalog, body); err != nil {
			var vf *schema.ValidationFailure
			if errors.As(err, &vf) {
				schema.LogValidationFailure("addons-catalog.yaml (write)", vf)
			}
			return nil, fmt.Errorf("validating addons-catalog before write: %w", err)
		}
	}

	var buf bytes.Buffer
	buf.WriteString(AddonCatalogSchemaHeader)
	buf.WriteByte('\n')
	buf.Write(body)
	return buf.Bytes(), nil
}

// ResolveAddonCatalogPath returns the on-disk path Sharko should read for the
// addon catalog under `configDir` (typically `<repo>/configuration`).
//
// Returns the canonical `addons-catalog.yaml` path if the file exists, or
// ("", os.ErrNotExist) otherwise so callers can fall back to whatever
// default-creation logic they already have.
//
// The signature returns the resolved absolute-or-relative path string rather
// than the bytes so callers that care about audit logging / commit messages
// can name the actual file they read.
func ResolveAddonCatalogPath(configDir string) (string, error) {
	path := filepath.Join(configDir, AddonCatalogFilename)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	// Return the canonical missing-file error so callers can use errors.Is.
	return "", os.ErrNotExist
}

// ParseClusterValues parses a per-cluster values file and extracts global values.
func (p *Parser) ParseClusterValues(data []byte) (map[string]interface{}, error) {
	var file clusterValuesFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing cluster values: %w", err)
	}

	return file.ClusterGlobalValues, nil
}

// GetEnabledAddons returns which addons are enabled/disabled for a cluster based on its labels.
func (p *Parser) GetEnabledAddons(cluster models.Cluster, catalog []models.AddonCatalogEntry) []models.ClusterAddonInfo {
	addons := make([]models.ClusterAddonInfo, 0)

	for _, catalogEntry := range catalog {
		addonName := catalogEntry.Name
		labelValue, hasLabel := cluster.Labels[addonName]

		if !hasLabel {
			continue
		}

		// Only include addons whose label value means "on" (the canonical
		// "enabled"). The ArgoCD cluster generator only listens to
		// key: enabled; disabled, commented out, or missing labels are all
		// equivalent. models.AddonLabelEnabled is the single source of truth
		// for this predicate so the parser, the chart selector, and the
		// register/enable writers can never drift apart.
		if !models.AddonLabelEnabled(labelValue) {
			continue
		}

		// Check for version override: <addon-name>-version label
		versionKey := addonName + "-version"
		customVersion, hasOverride := cluster.Labels[versionKey]

		currentVersion := catalogEntry.Version
		if hasOverride && customVersion != "" {
			currentVersion = customVersion
		}

		addon := models.ClusterAddonInfo{
			AddonName:          addonName,
			Chart:              catalogEntry.Chart,
			RepoURL:            catalogEntry.RepoURL,
			CurrentVersion:     currentVersion,
			Enabled:            true,
			Namespace:          catalogEntry.Namespace,
			EnvironmentVersion: catalogEntry.Version,
			CustomVersion:      customVersion,
			HasVersionOverride: hasOverride,
		}

		addons = append(addons, addon)
	}

	return addons
}

// ParseAll parses all configuration files and returns a combined RepoConfig.
func (p *Parser) ParseAll(clusterAddonsData, addonsCatalogData []byte) (*RepoConfig, error) {
	clusters, err := p.ParseClusterAddons(clusterAddonsData)
	if err != nil {
		return nil, err
	}

	addons, err := p.ParseAddonsCatalog(addonsCatalogData)
	if err != nil {
		return nil, err
	}

	return &RepoConfig{
		Clusters: clusters,
		Addons:   addons,
	}, nil
}

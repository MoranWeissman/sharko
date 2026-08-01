// Command schema-gen emits the canonical Sharko JSON Schemas to
// docs/schemas/ AND internal/schema/.
//
// Run via:
//
//	go run ./cmd/schema-gen      # from repo root
//	make generate-schemas         # via the Makefile target
//
// Output (seven schemas, each mirrored to two locations, overwritten if
// they exist): managed-clusters, addons-catalog, default-addons,
// marketplace-sources, cluster-addons, catalog — the
// last two are the v4 Wave 1 Story 2.6 kinds. Every schema lands at
// both:
//
//	docs/schemas/<name>.v1.json
//	internal/schema/<name>.v1.json   (embed source)
//
// The two locations exist for different consumers:
//
//   - docs/schemas/ is the human-facing copy and the URL target for
//     editor `# yaml-language-server: $schema=...` headers and the docs
//     site links.
//   - internal/schema/ is the build-time copy. internal/schema/embed.go
//     declares `//go:embed managed-clusters.v1.json addons-catalog.v1.json`
//     so the runtime validator compiles schemas from the binary, not
//     from disk. Embedding from docs/schemas/ would require a `..` path
//     which Go forbids.
//
// CI ("Schemas Up To Date") runs `make generate-schemas` then
// `git diff --exit-code` against BOTH locations. The binary is strictly
// idempotent — running it N times produces byte-identical output. The
// determinism comes from invopop/jsonschema preserving struct field
// declaration order plus encoding/json sorting map keys.
//
// This file is intentionally thin. The reflection + serialization logic
// lives in internal/schema/generator.go so it can be unit-tested without
// exec'ing this binary. Main exists only to:
//
//  1. Construct the per-kind wrapper structs whose Spec field has the
//     concrete payload type — these are local to main because they import
//     internal/models + internal/config and we don't want the schema
//     package to depend on either (would create a cycle: models imports
//     schema for the envelope, generator can't then import models).
//  2. Hand the wrappers to schema.GenerateSchema with their per-kind id +
//     title + description + kindConst.
//  3. Write the bytes to BOTH output directories and log a summary.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/schema"
)

// outputDirs are the canonical locations for generated schemas, relative
// to the repository root (which is the expected CWD when running via
// `go run ./cmd/schema-gen` or `make generate-schemas`).
//
//   - docsOutputDir is the human-facing copy under docs/schemas/. CI
//     diff-checks it; the public schema URLs resolve to it.
//   - embedOutputDir is the build-time copy under internal/schema/. The
//     runtime validator's go:embed directives in internal/schema/embed.go
//     pick these files up at compile time; embedding from docs/schemas/
//     would require a `..` path which Go's embed package rejects.
//
// Both paths are hard-coded — no CLI flags — because both are part of
// the public contract (CI gates + go:embed directives reference these
// exact locations and would silently drift if a flag let an operator
// change them).
const (
	docsOutputDir  = "docs/schemas"
	embedOutputDir = "internal/schema"
)

// The wrapper structs below are what invopop/jsonschema reflects. They
// exist so the generated root schema does not leak a Go generic
// instantiation name (Envelope[github.com/...]) into a public document.
//
// There are two families, matching the two on-disk shapes described in
// internal/schema's package comment:
//
//   - FLAT wrappers embed the payload type anonymously, so the payload's
//     own fields are promoted and land beside apiVersion and kind at the
//     top level — exactly what schema.EncodeFlat writes. Used for every
//     file a v4 repo contains.
//   - WRAPPED wrappers keep a named Spec field, mirroring
//     schema.Envelope[T]. Used only for the v3-era files that still carry
//     the spec: wrapper on disk.
//
// PARITY INVARIANT (wrapped family): a wrapped wrapper's field set + json
// tags MUST exactly match schema.Envelope[T]'s. Change Envelope[T] in
// internal/schema/envelope.go and you change these identically. The
// TestGenerator_EnvelopeParity test in internal/schema/generator_test.go
// pins it — if the schemas drift from what a real Envelope[T]
// yaml-marshals to, that test fails.
//
// PARITY INVARIANT (flat family): the embedded payload type must be THE
// SAME type the loader unmarshals into, so the schema and the reader can
// never disagree about the field set. Embedding rather than re-declaring
// the fields is what makes that structural rather than a matter of
// discipline.

// managedClustersDoc is the flat shape of managed-clusters.yaml.
type managedClustersDoc struct {
	APIVersion                 string `json:"apiVersion"`
	Kind                       string `json:"kind"`
	models.ManagedClustersSpec `json:",inline"`
}

// managedClustersV3Doc is the WRAPPED shape of a pre-v4
// configuration/managed-clusters.yaml. Same payload as
// managedClustersDoc's, one level down under spec:.
type managedClustersV3Doc struct {
	APIVersion string                     `json:"apiVersion"`
	Kind       string                     `json:"kind"`
	Metadata   schema.Metadata            `json:"metadata"`
	Spec       models.ManagedClustersSpec `json:"spec"`
}

// addonCatalogDoc mirrors schema.Envelope[config.AddonCatalogV3Spec] for the
// same reason as managedClustersDoc. Same parity invariant applies.
type addonCatalogDoc struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Metadata   schema.Metadata           `json:"metadata"`
	Spec       config.AddonCatalogV3Spec `json:"spec"`
}

// defaultAddonsDoc mirrors schema.Envelope[config.DefaultAddonsSpec] for the
// same reason as managedClustersDoc. Same parity invariant applies.
type defaultAddonsDoc struct {
	APIVersion string                   `json:"apiVersion"`
	Kind       string                   `json:"kind"`
	Metadata   schema.Metadata          `json:"metadata"`
	Spec       config.DefaultAddonsSpec `json:"spec"`
}

// marketplaceSourcesDoc mirrors schema.Envelope[config.MarketplaceSourcesSpec] for the
// same reason as managedClustersDoc. Same parity invariant applies.
type marketplaceSourcesDoc struct {
	APIVersion string                        `json:"apiVersion"`
	Kind       string                        `json:"kind"`
	Metadata   schema.Metadata               `json:"metadata"`
	Spec       config.MarketplaceSourcesSpec `json:"spec"`
}

// clusterAddonsDoc is the flat shape of cluster-addons/<cluster-name>.yaml.
type clusterAddonsDoc struct {
	APIVersion               string `json:"apiVersion"`
	Kind                     string `json:"kind"`
	models.ClusterAddonsSpec `json:",inline"`
}

// addonCatalogV4Doc is the flat shape of catalog.yaml — the org's approved
// addon list. Its kind (AddonCatalog) is shared with the v3
// addons-catalog.yaml that addonCatalogDoc above describes; the two are
// told apart by shape, never by kind. See schema.KindAddonCatalog.
type addonCatalogV4Doc struct {
	APIVersion              string `json:"apiVersion"`
	Kind                    string `json:"kind"`
	config.AddonCatalogSpec `json:",inline"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("schema generation failed", "error", err)
		os.Exit(1)
	}
}

// run is the executable body of main, broken out so any future test that
// wants to exercise the file-write path end-to-end (currently only the
// reflection path is unit-tested in internal/schema/generator_test.go) can
// call it with a temp directory.
//
// Writes happen via writeSchemaToBoth so the docs/schemas/ and
// internal/schema/ copies are guaranteed byte-identical — the
// alternative (two separate write calls per kind) drifts under
// refactoring; centralising the writes in a single helper makes the
// "both locations always agree" invariant a structural property rather
// than a discipline.
func run(logger *slog.Logger) error {
	if err := os.MkdirAll(docsOutputDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", docsOutputDir, err)
	}
	if err := os.MkdirAll(embedOutputDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", embedOutputDir, err)
	}

	// managed-clusters.v1.json
	mcBytes, err := schema.GenerateSchema(
		&managedClustersDoc{},
		schema.ManagedClustersSchemaID,
		"Sharko ManagedClusters",
		"managed-clusters.yaml — the registry of clusters Sharko manages, including their credential paths and addon labels.",
		schema.KindManagedClusters,
	)
	if err != nil {
		return fmt.Errorf("generating managed-clusters schema: %w", err)
	}
	mcDocsPath, mcEmbedPath, err := writeSchemaToBoth("managed-clusters.v1.json", mcBytes)
	if err != nil {
		return err
	}

	// managed-clusters-v3.v1.json — the wrapped shape a pre-v4 repo has.
	mc3Bytes, err := schema.GenerateSchema(
		&managedClustersV3Doc{},
		schema.ManagedClustersV3SchemaID,
		"Sharko ManagedClusters (pre-v4 shape)",
		"configuration/managed-clusters.yaml — the same cluster registry as managed-clusters.v1.json, in the older spec:-wrapped shape a repository bootstrapped before v4 still has on disk. Sharko reads this shape but no longer writes it.",
		schema.KindManagedClusters,
	)
	if err != nil {
		return fmt.Errorf("generating managed-clusters-v3 schema: %w", err)
	}
	mc3DocsPath, mc3EmbedPath, err := writeSchemaToBoth("managed-clusters-v3.v1.json", mc3Bytes)
	if err != nil {
		return err
	}

	// addons-catalog.v1.json
	acBytes, err := schema.GenerateSchema(
		&addonCatalogDoc{},
		schema.AddonCatalogSchemaID,
		"Sharko AddonCatalog",
		"addons-catalog.yaml — the catalog of addons (ApplicationSets) Sharko can deploy to managed clusters.",
		schema.KindAddonCatalog,
	)
	if err != nil {
		return fmt.Errorf("generating addons-catalog schema: %w", err)
	}
	acDocsPath, acEmbedPath, err := writeSchemaToBoth("addons-catalog.v1.json", acBytes)
	if err != nil {
		return err
	}

	// default-addons.v1.json
	daBytes, err := schema.GenerateSchema(
		&defaultAddonsDoc{},
		schema.DefaultAddonsSchemaID,
		"Sharko DefaultAddons",
		"default-addons.yaml — the list of addons auto-enabled on clusters registered without explicit addon selection.",
		schema.KindDefaultAddons,
	)
	if err != nil {
		return fmt.Errorf("generating default-addons schema: %w", err)
	}
	daDocsPath, daEmbedPath, err := writeSchemaToBoth("default-addons.v1.json", daBytes)
	if err != nil {
		return err
	}

	// marketplace-sources.v1.json
	msBytes, err := schema.GenerateSchema(
		&marketplaceSourcesDoc{},
		schema.MarketplaceSourcesSchemaID,
		"Sharko MarketplaceSources",
		"marketplace-sources.yaml — the list of third-party catalog source URLs pulled by Sharko's marketplace fetcher.",
		schema.KindMarketplaceSources,
	)
	if err != nil {
		return fmt.Errorf("generating marketplace-sources schema: %w", err)
	}
	msDocsPath, msEmbedPath, err := writeSchemaToBoth("marketplace-sources.v1.json", msBytes)
	if err != nil {
		return err
	}

	// cluster-addons.v1.json (v4 Wave 1 Story 2.6)
	caBytes, err := schema.GenerateSchema(
		&clusterAddonsDoc{},
		schema.ClusterAddonsSchemaID,
		"Sharko ClusterAddons",
		"cluster-addons/<cluster-name>.yaml — which addons run on this cluster, at which version, tuned how (v4).",
		schema.KindClusterAddons,
	)
	if err != nil {
		return fmt.Errorf("generating cluster-addons schema: %w", err)
	}
	caDocsPath, caEmbedPath, err := writeSchemaToBoth("cluster-addons.v1.json", caBytes)
	if err != nil {
		return err
	}

	// catalog.v1.json
	acdBytes, err := schema.GenerateSchema(
		&addonCatalogV4Doc{},
		schema.AddonCatalogV4SchemaID,
		"Sharko AddonCatalog",
		"catalog.yaml — the addons this org has approved for its clusters. Each entry is complete on its own: chart, repo, version, namespace, settings and the secrets it needs.",
		schema.KindAddonCatalog,
	)
	if err != nil {
		return fmt.Errorf("generating catalog schema: %w", err)
	}
	acdDocsPath, acdEmbedPath, err := writeSchemaToBoth("catalog.v1.json", acdBytes)
	if err != nil {
		return err
	}

	logger.Info("generated 7 schemas (mirrored to 2 locations each)",
		"managed_clusters_v3_docs", mc3DocsPath,
		"managed_clusters_v3_embed", mc3EmbedPath,
		"managed_clusters_docs", mcDocsPath,
		"managed_clusters_embed", mcEmbedPath,
		"addons_catalog_docs", acDocsPath,
		"addons_catalog_embed", acEmbedPath,
		"default_addons_docs", daDocsPath,
		"default_addons_embed", daEmbedPath,
		"marketplace_sources_docs", msDocsPath,
		"marketplace_sources_embed", msEmbedPath,
		"cluster_addons_docs", caDocsPath,
		"cluster_addons_embed", caEmbedPath,
		"catalog_docs", acdDocsPath,
		"catalog_embed", acdEmbedPath,
	)
	fmt.Printf("generated 7 schemas to %s + %s: managed-clusters.v1.json, managed-clusters-v3.v1.json, addons-catalog.v1.json, default-addons.v1.json, marketplace-sources.v1.json, cluster-addons.v1.json, catalog.v1.json\n",
		docsOutputDir, embedOutputDir)
	return nil
}

// writeSchemaToBoth writes the same bytes to both docs/schemas/ and
// internal/schema/ under the same filename. Returns both paths for
// logging. The two writes are sequential — if the first succeeds and
// the second fails, the operator runs `make generate-schemas` again to
// re-sync; the failure mode is loud enough (CI's drift gate will catch
// a partial write) that a fancier transactional shape would be wasted
// complexity for a build-time tool.
func writeSchemaToBoth(filename string, body []byte) (docsPath, embedPath string, err error) {
	docsPath = filepath.Join(docsOutputDir, filename)
	embedPath = filepath.Join(embedOutputDir, filename)
	if err := os.WriteFile(docsPath, body, 0o644); err != nil {
		return "", "", fmt.Errorf("writing %s: %w", docsPath, err)
	}
	if err := os.WriteFile(embedPath, body, 0o644); err != nil {
		return "", "", fmt.Errorf("writing %s: %w", embedPath, err)
	}
	return docsPath, embedPath, nil
}

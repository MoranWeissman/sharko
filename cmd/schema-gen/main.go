// Command schema-gen emits the canonical Sharko JSON Schemas to
// docs/schemas/ AND internal/schema/.
//
// Run via:
//
//	go run ./cmd/schema-gen      # from repo root
//	make generate-schemas         # via the Makefile target
//
// Output (always four files — same two schemas mirrored to two
// locations, overwritten if they exist):
//
//	docs/schemas/managed-clusters.v1.json
//	docs/schemas/addons-catalog.v1.json
//	internal/schema/managed-clusters.v1.json   (embed source)
//	internal/schema/addons-catalog.v1.json      (embed source)
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

// managedClustersDoc mirrors schema.Envelope[models.ManagedClustersSpec]
// structurally so invopop/jsonschema reflects a clean root schema without
// leaking the generic instantiation name (Envelope[...]) into the output.
//
// PARITY INVARIANT: this struct's field set + json tags MUST exactly match
// schema.Envelope[T]'s field set + json tags. If you change Envelope[T] in
// internal/schema/envelope.go, change this struct and addonCatalogDoc
// below identically. The TestGenerator_EnvelopeParity test in
// internal/schema/generator_test.go pins this — if the schemas drift from
// what a real Envelope[T] yaml-marshals to, that test fails.
type managedClustersDoc struct {
	APIVersion string                     `json:"apiVersion"`
	Kind       string                     `json:"kind"`
	Metadata   schema.Metadata            `json:"metadata"`
	Spec       models.ManagedClustersSpec `json:"spec"`
}

// addonCatalogDoc mirrors schema.Envelope[config.AddonCatalogSpec] for the
// same reason as managedClustersDoc. Same parity invariant applies.
type addonCatalogDoc struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Metadata   schema.Metadata         `json:"metadata"`
	Spec       config.AddonCatalogSpec `json:"spec"`
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

	logger.Info("generated 2 schemas (mirrored to 2 locations each)",
		"managed_clusters_docs", mcDocsPath,
		"managed_clusters_embed", mcEmbedPath,
		"addons_catalog_docs", acDocsPath,
		"addons_catalog_embed", acEmbedPath,
	)
	fmt.Printf("generated 2 schemas to %s + %s: managed-clusters.v1.json, addons-catalog.v1.json\n",
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

// Command schema-gen emits the canonical Sharko JSON Schemas to
// docs/schemas/. V125-1-9 Story 9.3.
//
// Run via:
//
//	go run ./cmd/schema-gen      # from repo root
//	make generate-schemas         # via the Makefile target
//
// Output (always two files, overwritten if they exist):
//
//	docs/schemas/managed-clusters.v1.json
//	docs/schemas/addon-catalog.v1.json
//
// CI ("Schemas Up To Date") runs `make generate-schemas` then
// `git diff --exit-code docs/schemas/`; the binary is therefore strictly
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
//  3. Write to docs/schemas/ and log a summary.
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

// outputDir is the canonical location for generated schemas, relative to
// the repository root (which is the expected CWD when running via
// `go run ./cmd/schema-gen` or `make generate-schemas`). Hard-coding here
// avoids a CLI flag that nobody actually uses — the path is part of the
// public contract (CI checks `git diff --exit-code docs/schemas/`, the
// editor headers point at sharko.io/schemas/, etc.).
const outputDir = "docs/schemas"

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
func run(logger *slog.Logger) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", outputDir, err)
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
	mcPath := filepath.Join(outputDir, "managed-clusters.v1.json")
	if err := os.WriteFile(mcPath, mcBytes, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", mcPath, err)
	}

	// addon-catalog.v1.json
	acBytes, err := schema.GenerateSchema(
		&addonCatalogDoc{},
		schema.AddonCatalogSchemaID,
		"Sharko AddonCatalog",
		"addon-catalog.yaml — the catalog of addons (ApplicationSets) Sharko can deploy to managed clusters.",
		schema.KindAddonCatalog,
	)
	if err != nil {
		return fmt.Errorf("generating addon-catalog schema: %w", err)
	}
	acPath := filepath.Join(outputDir, "addon-catalog.v1.json")
	if err := os.WriteFile(acPath, acBytes, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", acPath, err)
	}

	logger.Info("generated 2 schemas",
		"managed_clusters", mcPath,
		"addon_catalog", acPath,
	)
	fmt.Printf("generated 2 schemas: %s, %s\n", mcPath, acPath)
	return nil
}

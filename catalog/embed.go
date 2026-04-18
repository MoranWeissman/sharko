// Package catalog holds the embedded curated addon catalog + its JSON Schema.
//
// The catalog is byte-baked into the Sharko binary at build time via //go:embed
// so the server is self-contained and works offline. The loader, search index,
// and OpenSSF Scorecard refresh job live in internal/catalog — this package
// exists only to host the embedded bytes (because //go:embed paths must be
// relative to the package directory and the YAML lives at the repo root under
// `catalog/`).
//
// See docs/design/2026-04-17-v1.21-catalog-discovery.md.
package catalog

import _ "embed"

// rawAddonsYAML is the byte content of catalog/addons.yaml at the repo root,
// embedded at build time.
//
//go:embed addons.yaml
var rawAddonsYAML []byte

// rawSchemaJSON is the byte content of catalog/schema.json (the human-readable
// JSON Schema for the catalog). Runtime validation is done in Go in
// internal/catalog/loader.go — this schema file is for documentation and for
// the catalog-validate CI workflow.
//
//go:embed schema.json
var rawSchemaJSON []byte

// AddonsYAML returns a copy of the embedded catalog YAML bytes.
// Returning a copy prevents accidental mutation of the embedded payload.
func AddonsYAML() []byte {
	out := make([]byte, len(rawAddonsYAML))
	copy(out, rawAddonsYAML)
	return out
}

// SchemaJSON returns a copy of the embedded JSON Schema bytes.
func SchemaJSON() []byte {
	out := make([]byte, len(rawSchemaJSON))
	copy(out, rawSchemaJSON)
	return out
}

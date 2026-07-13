// Embedded schema sources for the runtime validator.
//
// The two .v1.json files in this package directory are byte-identical
// copies of docs/schemas/*.v1.json. They are duplicated here so the
// validator can load schemas via go:embed rather than from a runtime
// filesystem path — deployed Sharko binaries ship the schemas inside
// the binary.
//
// Idempotency / drift:
//
//   - cmd/schema-gen/main.go writes to BOTH docs/schemas/ AND
//     internal/schema/ on every `make generate-schemas` run so the two
//     locations cannot diverge.
//   - CI's "Schemas Up To Date" check (`.github/workflows/ci.yml`) runs
//     `git diff --exit-code` against both paths so any contributor who
//     edits one without the other gets a red build.
//
// Why duplicate rather than `//go:embed ../../docs/schemas/*.v1.json`:
// Go's embed package rejects paths containing `..` (it only embeds files
// inside or below the package directory). Copying is the cleanest fix —
// the alternative (symlinks / generator-managed embed.FS rooted
// elsewhere) introduces toolchain edge cases on Windows + Bazel + module
// graphs that Sharko explicitly does not want to chase.
//
// DO NOT hand-edit the embedded JSON files. Run `make generate-schemas`.
package schema

import _ "embed"

// embeddedManagedClustersSchema is the JSON Schema bytes for
// managed-clusters.yaml. Loaded once at validator construction and
// compiled into the lazy singleton — the embed directive resolves at
// build time, so the binary carries the bytes and no disk read happens at
// runtime.
//
//go:embed managed-clusters.v1.json
var embeddedManagedClustersSchema []byte

// embeddedAddonCatalogSchema is the JSON Schema bytes for
// addons-catalog.yaml. Same lifecycle as embeddedManagedClustersSchema.
//
//go:embed addons-catalog.v1.json
var embeddedAddonCatalogSchema []byte

// embeddedDefaultAddonsSchema is the JSON Schema bytes for
// default-addons.yaml. Same lifecycle as the other embedded schemas.
//
//go:embed default-addons.v1.json
var embeddedDefaultAddonsSchema []byte

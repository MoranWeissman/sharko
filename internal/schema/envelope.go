// Package schema defines the shared self-describing YAML envelope used by
// Sharko's gitops configuration files (managed-clusters.yaml and
// addons-catalog.yaml). The envelope wraps the file's payload in an
// apiVersion/kind/metadata/spec structure so the reader can validate the
// document against a versioned JSON Schema before handing it to the
// domain-specific loader.
//
// Layout:
//
//   - The envelope types and the IsEnveloped detector live here so that
//     the models cluster reader and the catalog loader can both depend
//     on a single shared definition.
//   - JSON Schema generation (cmd/schema-gen) and the read-time
//     validator (validator.go) introspect these same types.
//
// The envelope shape:
//
//	# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/managed-clusters.v1.json
//	apiVersion: sharko.dev/v1
//	kind: ManagedClusters
//	metadata:
//	  name: managed-clusters
//	spec:
//	  clusters: [...]
//
// Stories 9.1 + 9.2 each pick a concrete Spec type and use
// Envelope[ConcreteSpec] as the on-disk representation.
package schema

import (
	"bytes"
	"log/slog"
	"sync"

	"gopkg.in/yaml.v3"
)

// APIVersion is the canonical Sharko envelope apiVersion. Every writer emits
// this value. The reader additionally accepts APIVersionLegacy (see below);
// any other value (including the absence of apiVersion) is treated as a
// legacy bare-YAML document.
const APIVersion = "sharko.dev/v1"

// APIVersionLegacy is the pre-V2-cleanup-59 envelope group. Sharko originally
// shipped under sharko.io — a domain the project never owned — and files
// carrying that group exist in every repo bootstrapped before the rename to
// the maintainer-owned sharko.dev. The reader accepts BOTH groups for all of
// v2.x (READ-BOTH / EMIT-NEW): no forced migration of user git files — an
// old-group managed-clusters.yaml or addons-catalog.yaml keeps parsing and
// validating exactly as before, with a single Info-level deprecation log per
// process. Writers never emit this value.
const APIVersionLegacy = "sharko.io/v1"

// legacyAPIVersionLogOnce rate-limits the deprecation notice to one Info log
// per process. Reader paths (reconcilers) re-parse the same file every tick;
// logging on every parse would flood the log with thousands of identical
// lines per day while adding no information.
var legacyAPIVersionLogOnce sync.Once

// Kind constants name the Sharko envelope payload types. Each on-disk file
// declares exactly one kind in its top-level kind: field.
const (
	// KindManagedClusters identifies a managed-clusters.yaml document.
	// Consumed by Story 9.1.
	KindManagedClusters = "ManagedClusters"

	// KindAddonCatalog identifies an addons-catalog.yaml document.
	// Consumed by Story 9.2.
	KindAddonCatalog = "AddonCatalog"
)

// Metadata holds the envelope's metadata block. Only Name is required by the
// schema; Annotations is reserved as a forward-compatible extension point so
// downstream stories can attach optional fields (e.g. a content hash, a
// last-rendered-by note) without a schema bump.
type Metadata struct {
	Name        string            `json:"name" yaml:"name"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// Envelope is the generic wrapper for every Sharko-managed YAML file. Stories
// 9.1 and 9.2 instantiate it with their kind-specific Spec types.
//
// The yaml.v3 + encoding/json struct tags follow the existing Sharko
// convention in internal/models (lowerCamelCase for YAML, snake_case-or-camel
// for JSON depending on the field's API exposure; the envelope itself is not
// JSON-API-exposed today so the JSON tags match the YAML tags).
type Envelope[T any] struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
	Spec       T        `json:"spec" yaml:"spec"`
}

// envelopeHeader is the minimal subset IsEnveloped needs to peek at to decide
// whether a body is enveloped. Keeping this private prevents callers from
// accidentally relying on a partial decode.
type envelopeHeader struct {
	APIVersion string `yaml:"apiVersion"`
}

// IsEnveloped reports whether body is a Sharko-enveloped YAML document
// (apiVersion: sharko.dev/v1 — or the legacy sharko.io/v1 — at the top
// level).
//
// Return contract:
//
//   - empty body              -> (false, nil)   treat as legacy; caller decides
//     whether "missing/empty" is itself an
//     error in its own context
//   - malformed YAML          -> (false, err)   so callers can distinguish
//     "intentionally legacy" from "broken file"
//   - unknown apiVersion      -> (false, nil)   treat as legacy; downstream
//     readers may decide whether to
//     fail-loudly on a foreign apiVersion
//     (e.g. sharko.dev/v2 from a newer
//     installation)
//   - apiVersion == sharko.dev/v1 -> (true, nil)
//   - apiVersion == sharko.io/v1  -> (true, nil)  READ-BOTH compat
//     (V2-cleanup-59): files authored
//     before the group rename keep
//     working for all of v2.x; one
//     Info-level deprecation log fires
//     per process.
//
// The detector deliberately does not validate kind, metadata, or spec — that
// is the job of the read-time validator landing in Story 9.4. IsEnveloped is
// only the routing primitive that decides between the legacy reader path and
// the enveloped reader path.
func IsEnveloped(body []byte) (bool, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return false, nil
	}
	var header envelopeHeader
	if err := yaml.Unmarshal(body, &header); err != nil {
		return false, err
	}
	switch header.APIVersion {
	case APIVersion:
		return true, nil
	case APIVersionLegacy:
		legacyAPIVersionLogOnce.Do(func() {
			slog.Info(
				"deprecated_api_group",
				"found", APIVersionLegacy,
				"use", APIVersion,
				"detail", "this file uses the old sharko.io API group; it keeps working throughout v2.x, but new files are written with the maintainer-owned sharko.dev group",
			)
		})
		return true, nil
	default:
		return false, nil
	}
}

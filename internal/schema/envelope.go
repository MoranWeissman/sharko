// Package schema defines the shared self-describing YAML header used by
// Sharko's gitops configuration files. Every file names itself with an
// apiVersion and a kind so the reader can validate it against a versioned
// JSON Schema before handing it to the domain-specific loader.
//
// # Two file shapes: flat (current) and wrapped (legacy)
//
// FLAT is the shape Sharko writes today — the "kustomize school" (design
// doc 2026-07-31-catalog-approved-model.md §9). apiVersion and kind sit at
// the top, and the payload's own fields sit beside them at the SAME level.
// There is no spec: wrapper and no metadata: block:
//
//	apiVersion: sharko.dev/v1
//	kind: ManagedClusters
//	clusters: [...]
//
// The reason: kind: plus spec: together promise a real Kubernetes resource
// you could kubectl apply, and there is no CRD behind these files — nobody
// applies them, Sharko reads them out of git. kustomize, kind, kubeadm and
// Skaffold all use apiVersion + kind + top-level fields for exactly this
// sort of read-only config file, and they keep metadata:/spec: for things
// that really are applied. Sharko now follows that convention. The one
// genuine manifest Sharko writes — the engine pin, an ArgoCD Application
// that IS applied to the hub — keeps its full Kubernetes form and does not
// go through this package.
//
// WRAPPED is the older shape, payload under a spec: key, modelled by
// Envelope[T] below. Sharko no longer writes it. It is still READ for the
// file kinds that shipped in v3 and therefore exist in real user repos
// (see DecodeFlatOrWrapped) — the same read-both/emit-new stance this
// package already takes for the sharko.io -> sharko.dev group rename.
//
// Layout:
//
//   - The envelope types and the IsEnveloped detector live here so that
//     the models cluster reader and the catalog loader can both depend
//     on a single shared definition.
//   - JSON Schema generation (cmd/schema-gen) and the read-time
//     validator (validator.go) introspect these same types.
package schema

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
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

// sharkoGroupPrefix marks the apiVersion family this project owns. Any
// apiVersion that starts with this prefix but is NOT in the accepted set
// (APIVersion, APIVersionLegacy) was written by a newer or unknown Sharko —
// IsEnveloped refuses to route such a body anywhere (hard error, never the
// bare-YAML legacy fallthrough). See UnknownSharkoAPIVersionError.
const sharkoGroupPrefix = "sharko."

// UnknownSharkoAPIVersionError is returned by IsEnveloped when a document
// declares an apiVersion in the sharko.* family that this binary does not
// recognize (e.g. sharko.dev/v2 written by a future release, or a group
// this project never shipped).
//
// This is the H2 forward guard (V2-cleanup-60.2): before it existed, an
// unrecognized sharko.* apiVersion fell through to the bare-YAML legacy
// reader, which silently parsed the file as ZERO clusters — and a
// downstream orphan sweep then deleted every Sharko-managed ArgoCD cluster
// Secret. An unknown Sharko identity must be a loud, actionable error,
// never an empty result.
type UnknownSharkoAPIVersionError struct {
	// Found is the apiVersion value the document declared.
	Found string
}

func (e *UnknownSharkoAPIVersionError) Error() string {
	return fmt.Sprintf(
		"unrecognized Sharko apiVersion %q: this file was written by a newer or unknown Sharko — refusing to guess how to read it (accepted: %s, %s (legacy)). Upgrade this Sharko instance to a version that understands %q; do not hand-edit the apiVersion unless you know the payload shape is compatible",
		e.Found, APIVersion, APIVersionLegacy, e.Found,
	)
}

// Kind constants name the Sharko envelope payload types. Each on-disk file
// declares exactly one kind in its top-level kind: field.
const (
	// KindManagedClusters identifies a managed-clusters.yaml document.
	// Consumed by Story 9.1.
	KindManagedClusters = "ManagedClusters"

	// KindAddonCatalog identifies a catalog document — the org's approved
	// addon list. TWO different file formats declare this one kind, and
	// they are told apart by SHAPE, never by the kind string:
	//
	//   - v4 catalog.yaml — FLAT (no spec:), payload is an addons MAP.
	//     This is what Sharko writes and what the engine chart reads.
	//     Validate it with SchemaKeyAddonCatalogV4.
	//   - v3 addons-catalog.yaml — WRAPPED (spec:), payload is an
	//     applicationsets LIST. Read-only legacy: it exists in every repo
	//     bootstrapped before v4, and the v3->v4 migration converter has to
	//     keep parsing it. Validate it with SchemaKeyAddonCatalogV3.
	//
	// The two shapes share a kind because the v4 design (design doc
	// 2026-07-31-catalog-approved-model.md §1) renamed the v4 kind from
	// AddonCatalogDelta to AddonCatalog — the file stopped being a delta
	// against a shipped list and became the whole approved list, so "Delta"
	// had become a lie in the name.
	// The v3 kind string cannot follow, because v3 shipped and those bytes
	// are already sitting in users' repos.
	//
	// This is safe ONLY because the shapes are unmistakable: a v4 file has
	// no spec: key at all, and a v3 file always has one. Every loader knows
	// which format it wants and passes the matching schema key, so nothing
	// guesses. The one place that cannot know — the validate-config CLI,
	// handed an arbitrary file — disambiguates on that same spec: key (see
	// Validator.ValidateAutoDetect). Do NOT add a third format under this
	// kind: the shape tiebreak only stretches to two.
	KindAddonCatalog = "AddonCatalog"

	// KindDefaultAddons identifies a default-addons.yaml document.
	// V3-Phase-2: GitOps-native default addons (UI opens PR, git file is truth).
	KindDefaultAddons = "DefaultAddons"

	// KindMarketplaceSources identifies a marketplace-sources.yaml document.
	// V3-Phase-3: GitOps-native third-party catalog source URLs (env fallback for tokened URLs).
	KindMarketplaceSources = "MarketplaceSources"

	// KindClusterAddons identifies a clusters/<cluster-name>.yaml
	// document (v4 Wave 1 Story 2.6). One file per cluster: which addons
	// run there, at which version, tuned how. See
	// docs/design/2026-07-30-v4-data-file-format.md §2.1.
	KindClusterAddons = "ClusterAddons"

	// KindManagedClusters, KindClusterAddons and KindAddonCatalog are the
	// three kinds a v4 repo contains. KindDefaultAddons and
	// KindMarketplaceSources are v3-only files that a v4 repo never has.
)

// Schema registry keys.
//
// For every kind EXCEPT the catalog pair, the validator registers a schema
// under the kind string itself and these keys are unnecessary. The catalog
// is the exception: two file formats share the kind "AddonCatalog" (see the
// KindAddonCatalog doc comment), so the registry — a plain map — needs two
// distinct keys or the second registration would silently overwrite the
// first and every file of one format would start failing validation against
// the other format's schema.
//
// A schema key is an INTERNAL registry identifier. It is never written to
// disk and never compared against a document's kind: field.
const (
	// SchemaKeyAddonCatalogV4 selects the flat v4 catalog.yaml schema.
	SchemaKeyAddonCatalogV4 = "AddonCatalog.v4"

	// SchemaKeyAddonCatalogV3 selects the wrapped v3 addons-catalog.yaml
	// schema. Its value is the bare kind string, so the v3 read path that
	// shipped in v3 keeps calling the validator with exactly the argument
	// it always did.
	SchemaKeyAddonCatalogV3 = KindAddonCatalog

	// SchemaKeyManagedClustersV3 selects the WRAPPED managed-clusters
	// schema — the spec:-under-metadata shape every repo bootstrapped
	// before v4 still has on disk.
	//
	// ManagedClusters is the one kind that spans both repo layouts: a v3
	// repo keeps configuration/managed-clusters.yaml (wrapped) and a v4
	// repo has managed-clusters.yaml at the root (flat), and ONE reader
	// serves both. Giving the old shape its own schema — rather than
	// waving validation through for it — means a v3 repo keeps exactly the
	// checking it has always had. Skipping instead would quietly turn
	// `sharko validate-config` into a no-op for every v3 file, which is
	// the opposite of what that command is for.
	SchemaKeyManagedClustersV3 = "ManagedClusters.v3"
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
//   - non-sharko apiVersion   -> (false, nil)   treat as legacy (a k8s
//     manifest, someone else's file —
//     not ours to police)
//   - sharko.* apiVersion NOT in the accepted set
//     -> (false, *UnknownSharkoAPIVersionError)
//     hard error, NEVER the bare-YAML
//     fallthrough. A newer/unknown Sharko
//     wrote this file; silently reading it
//     as "zero entries" is the H2 failure
//     class (orphan sweep deletes every
//     managed Secret). V2-cleanup-60.2.
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
		if strings.HasPrefix(header.APIVersion, sharkoGroupPrefix) {
			return false, &UnknownSharkoAPIVersionError{Found: header.APIVersion}
		}
		return false, nil
	}
}

// kindHeader is the minimal subset the flat decoders peek at to check a
// document names the kind the caller asked for.
type kindHeader struct {
	Kind string `yaml:"kind"`
}

// hasSpecKey reports whether a document has a top-level spec: key — the one
// signal that tells a legacy WRAPPED file from a current FLAT one.
//
// It decodes into a *yaml.Node rather than a concrete type so that "spec:"
// present but empty still counts as present. A nil pointer means the key
// was absent; an explicitly-null value gives a non-nil node, which is the
// honest answer — the file was authored in the wrapped shape, it just has
// no payload.
func hasSpecKey(body []byte) bool {
	var probe struct {
		Spec *yaml.Node `yaml:"spec"`
	}
	if err := yaml.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Spec != nil
}

// checkHeader runs the shared apiVersion + kind checks both flat decoders
// need, and returns an error naming label so the caller's message says
// which file was wrong.
func checkHeader(body []byte, wantKind, label string) error {
	enveloped, err := IsEnveloped(body)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", label, err)
	}
	if !enveloped {
		return fmt.Errorf(
			"parsing %s: not a Sharko document (apiVersion missing or not %s)",
			label, APIVersion,
		)
	}
	var header kindHeader
	if err := yaml.Unmarshal(body, &header); err != nil {
		return fmt.Errorf("parsing %s header: %w", label, err)
	}
	if header.Kind != wantKind {
		return fmt.Errorf("%s kind %q, expected %q", label, header.Kind, wantKind)
	}
	return nil
}

// DecodeFlat reads a FLAT Sharko document: apiVersion + kind at the top,
// payload fields beside them at the same level. T is the payload type; the
// apiVersion/kind keys are simply ignored while unmarshalling into it.
//
// Use this for kinds that only ever existed in the flat shape — nothing in
// a user's repo was ever written with a spec: wrapper for them, so
// accepting one would only invite a file Sharko cannot round-trip. Kinds
// that DID ship wrapped use DecodeFlatOrWrapped instead.
//
// Errors, never a silent zero value: a body that is not a Sharko document,
// or names the wrong kind, is a hard error. Returning an empty payload for
// a file somebody clearly meant as configuration is the failure class this
// codebase has been bitten by before — an empty read looks like "no
// clusters" and a downstream sweep then deletes everything.
func DecodeFlat[T any](body []byte, wantKind, label string) (T, error) {
	var payload T
	if err := checkHeader(body, wantKind, label); err != nil {
		return payload, err
	}
	if err := yaml.Unmarshal(body, &payload); err != nil {
		return payload, fmt.Errorf("parsing %s: %w", label, err)
	}
	return payload, nil
}

// DecodeFlatOrWrapped reads a Sharko document in EITHER shape and reports
// which shape it found.
//
// It exists for the kinds that shipped in v3 with a spec: wrapper and are
// still read today, so a repo bootstrapped before v4 keeps working with no
// hand-editing. Sharko writes only the flat shape (EncodeFlat); this is
// read-side compatibility, not a format Sharko maintains on both sides.
//
// wrapped is true when the file used the legacy spec: shape. Callers use it
// to decide whether to run JSON Schema validation — the generated schemas
// describe the flat shape, so a wrapped body must skip them, exactly as
// pre-envelope bare YAML always has.
func DecodeFlatOrWrapped[T any](body []byte, wantKind, label string) (payload T, wrapped bool, err error) {
	if err := checkHeader(body, wantKind, label); err != nil {
		return payload, false, err
	}
	if hasSpecKey(body) {
		var doc Envelope[T]
		if err := yaml.Unmarshal(body, &doc); err != nil {
			return payload, true, fmt.Errorf("parsing %s (legacy spec: shape): %w", label, err)
		}
		return doc.Spec, true, nil
	}
	if err := yaml.Unmarshal(body, &payload); err != nil {
		return payload, false, fmt.Errorf("parsing %s: %w", label, err)
	}
	return payload, false, nil
}

// EncodeFlat renders a payload as a FLAT Sharko document: the apiVersion
// and kind lines first, then the payload's own fields at the same level.
//
// The two header lines are prepended as text rather than marshalled from a
// struct because Go generics cannot embed a type parameter — there is no
// way to declare a flatDoc[T] whose fields are T's fields promoted to the
// top level. Writing the header by hand keeps the payload type free of
// header fields, which is what lets the SAME type be the loader's return
// value and the schema generator's reflection target.
func EncodeFlat[T any](kind string, payload T) ([]byte, error) {
	body, err := yaml.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling %s payload: %w", kind, err)
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "apiVersion: %s\nkind: %s\n", APIVersion, kind)
	buf.Write(body)
	return buf.Bytes(), nil
}

// Read-time JSON Schema validator.
//
// This file wires the generated schemas into a runtime validator that
// envelope-aware reader paths call before returning their parsed
// structs. The flow:
//
//	enveloped YAML bytes
//	       │
//	       ▼
//	IsEnveloped(body) -> true  ─► validator.Validate(kind, body)
//	       │                       │
//	       │ false (legacy)        │ on error: structured error
//	       │                       │            + slog.Error
//	       ▼                       ▼
//	legacy bare-YAML path      enveloped parse path
//	(SKIPS validation —        (only reached when validation passes)
//	 pre-envelope files keep
//	 working as-is)
//
// Validator construction is a lazy package singleton (sync.Once-guarded
// DefaultValidator getter). Reasoning:
//
//   - Reader paths are scattered across internal/models +
//     internal/config + (future) internal/secrets/reconciler — wiring a
//     constructor-injected validator through all of them would touch
//     every server/service boot point.
//   - Schema compilation is non-trivial (santhosh-tekuri compiles to an
//     internal Schema tree) so per-call compile is wasted work. The
//     per-call cost target from the plan (§164: "< 5ms") is unreachable
//     without caching.
//   - The validator carries no mutable state and is concurrency-safe
//     once compiled — a singleton is the simplest correct shape.
//
// Error shape: *ValidationFailure carries the kind + all underlying
// schema violations (not just the first), because the operator authoring
// a malformed file benefits from seeing every issue at once. The audit
// log emitted alongside surfaces the same list as a structured slog
// field so log scrapers / SRE dashboards can route on it.
package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"
)

// Validator holds compiled JSON Schema validators keyed by Sharko kind.
// Construct via NewValidator or — preferred — DefaultValidator.
//
// The zero value is NOT usable; nil schema map would NPE on Validate.
// The constructor enforces non-nil by failing the build with the embed
// asset error (and tests cover the no-embed path explicitly).
type Validator struct {
	// schemas is the compiled-per-kind cache. Indexed by the kind
	// constants in this package (KindManagedClusters, KindAddonCatalog)
	// so callers can pass the same constants they pass to the legacy
	// envelope routing.
	schemas map[string]*jsonschema.Schema
}

// ValidationFailure is the structured error returned by Validate when a
// body fails schema validation. Carries the kind that was validated
// against and the full list of underlying violations as human-readable
// strings.
//
// The audit-log path in the reader wiring (internal/models/cluster.go,
// internal/config/parser.go) inspects this type explicitly and emits the
// Violations slice as a structured slog field so log scrapers can route
// on the issue list without having to re-parse the error string.
type ValidationFailure struct {
	// Kind is the Sharko envelope kind the validator attempted —
	// KindManagedClusters or KindAddonCatalog.
	Kind string
	// Violations is the flattened list of every schema violation found
	// during the call. Empty slice never occurs (a failure always has
	// at least one violation); callers can range over without a length
	// check.
	Violations []string
}

// Error implements the error interface. Format:
//
//	schema validation failed for kind "ManagedClusters": 2 violation(s):
//	  - /spec/clusters: missing required property "name"
//	  - /metadata: additional property "extra" not allowed
//
// The leading "schema validation failed" prefix is load-bearing — the
// reader paths can errors.As into *ValidationFailure to distinguish
// schema failures from infrastructure failures (e.g. malformed YAML
// bytes that never made it to the validator), but downstream string
// matchers (notably Story 9.5's CLI) look for this prefix.
func (f *ValidationFailure) Error() string {
	if f == nil {
		return "schema validation failed: <nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "schema validation failed for kind %q: %d violation(s):", f.Kind, len(f.Violations))
	for _, v := range f.Violations {
		b.WriteString("\n  - ")
		b.WriteString(v)
	}
	return b.String()
}

var (
	defaultValidatorOnce sync.Once
	defaultValidator     *Validator
	defaultValidatorErr  error
)

// DefaultValidator returns the lazily-initialised package-level
// validator. Construction happens once per process; subsequent calls
// return the same pointer.
//
// Error contract: returns (nil, err) only when the embedded schemas
// fail to compile — which can only happen if the generator produces
// bytes the validator library rejects. That is a build-time invariant
// (the generator + validator test suites pin it), so production code
// can treat the error as a panic-worthy bug; reader paths still
// surface it as a regular error so callers stay in control of the
// failure mode (e.g. a CLI returns exit-2 rather than a Go panic).
func DefaultValidator() (*Validator, error) {
	defaultValidatorOnce.Do(func() {
		defaultValidator, defaultValidatorErr = NewValidator()
	})
	return defaultValidator, defaultValidatorErr
}

// NewValidator compiles the embedded schemas and returns a ready-to-use
// validator. Exported (rather than only DefaultValidator) so tests can
// build fresh instances without leaking state through the package
// singleton, and so future callers that need a non-default schema set
// (e.g. a tools binary embedding a different schema bundle) have an
// entry point.
//
// Embed-first contract: the production load path is go:embed (see
// embed.go). NewValidator does NOT fall back to disk — the deployed
// binary must carry the schemas, and silent disk fallback would mask
// "I forgot to run make generate-schemas before building" bugs.
// internal/schema/loader_disk.go (if/when added) would be a separate
// dev-only constructor.
func NewValidator() (*Validator, error) {
	v := &Validator{schemas: make(map[string]*jsonschema.Schema, 4)}

	if err := v.registerEmbedded(KindManagedClusters, ManagedClustersSchemaID, embeddedManagedClustersSchema); err != nil {
		return nil, err
	}
	if err := v.registerEmbedded(KindAddonCatalog, AddonCatalogSchemaID, embeddedAddonCatalogSchema); err != nil {
		return nil, err
	}
	if err := v.registerEmbedded(KindDefaultAddons, DefaultAddonsSchemaID, embeddedDefaultAddonsSchema); err != nil {
		return nil, err
	}
	if err := v.registerEmbedded(KindMarketplaceSources, MarketplaceSourcesSchemaID, embeddedMarketplaceSourcesSchema); err != nil {
		return nil, err
	}
	return v, nil
}

// registerEmbedded compiles a single embedded schema and stores it under
// the given kind. Kept as a method so the loop in NewValidator stays
// compact and the failure path produces a wrapped error naming both the
// kind and the schema id (the operator sees "AddonCatalog schema at
// https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json failed to compile" —
// enough information to pinpoint the breakage).
func (v *Validator) registerEmbedded(kind, id string, body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("schema validator: embedded schema for kind %q is empty (build did not include the embed asset)", kind)
	}
	c := jsonschema.NewCompiler()
	// santhosh-tekuri compiles by URL; the embed asset is treated as an
	// in-memory resource at the schema's canonical $id URL so any
	// internal $ref (none today, but future schemas may add them) can
	// resolve back to the same compiler-known URL rather than escaping
	// to the network.
	if err := c.AddResource(id, bytes.NewReader(body)); err != nil {
		return fmt.Errorf("schema validator: registering embedded schema for kind %q at %q: %w", kind, id, err)
	}
	compiled, err := c.Compile(id)
	if err != nil {
		return fmt.Errorf("schema validator: compiling embedded schema for kind %q at %q: %w", kind, id, err)
	}
	v.schemas[kind] = compiled
	return nil
}

// Validate checks body against the JSON Schema registered for kind.
// Returns nil on success, *ValidationFailure on schema violation, or a
// generic error for unknown kinds / YAML decode failures (which are
// classified separately so callers can react differently — a YAML decode
// failure means the operator's file is structurally broken; a schema
// failure means the file is parseable but semantically wrong).
//
// The body is the raw on-disk bytes (YAML); Validate handles the
// YAML→JSON conversion internally because jsonschema.Schema.Validate
// expects decoded JSON-compatible values (any / map / []any). Doing the
// conversion inside the validator means the caller doesn't have to
// re-decode bytes they already YAML-unmarshalled — and lets us swap
// the validator library in the future without churning every call site.
func (v *Validator) Validate(kind string, body []byte) error {
	if v == nil || v.schemas == nil {
		return errors.New("schema validator: nil validator (call NewValidator first)")
	}
	s, ok := v.schemas[kind]
	if !ok {
		return fmt.Errorf("schema validator: no schema registered for kind %q (known kinds: %s)", kind, knownKinds(v))
	}

	// Convert YAML to a JSON-shaped any value. yaml.v3 decodes into Go
	// types that don't match what jsonschema expects (map[any]any vs
	// map[string]any), so we go through JSON as the bridge — same
	// pattern santhosh-tekuri's README recommends for YAML inputs.
	var asAny any
	if err := yamlToJSONAny(body, &asAny); err != nil {
		return fmt.Errorf("schema validator: decoding body for kind %q: %w", kind, err)
	}

	if err := s.Validate(asAny); err != nil {
		ve, ok := err.(*jsonschema.ValidationError)
		if !ok {
			// Defensive: validator library always returns
			// *ValidationError on validation failure per its docs;
			// surface anything else as-is so we don't lose information
			// during a library upgrade that changes the contract.
			return fmt.Errorf("schema validator: %w", err)
		}
		return &ValidationFailure{
			Kind:       kind,
			Violations: flattenViolations(ve),
		}
	}
	return nil
}

// ValidateAutoDetect peeks the body's `kind:` field via a partial YAML
// decode, routes to Validate for the matched kind, and returns the
// usual error contract. Returns an error when the body is not
// enveloped, or when the envelope's kind is not one Sharko knows about.
//
// Intended for callers that have an enveloped body but don't yet know
// which kind it is (the Story 9.5 CLI, future tooling). Reader paths
// in internal/models / internal/config already know their target kind
// at call time and should call Validate directly — auto-detection is
// purely a UX affordance for the CLI's "give me a file, tell me if
// it's valid" mode.
func (v *Validator) ValidateAutoDetect(body []byte) error {
	if v == nil {
		return errors.New("schema validator: nil validator (call NewValidator first)")
	}
	enveloped, err := IsEnveloped(body)
	if err != nil {
		return fmt.Errorf("schema validator: auto-detect: %w", err)
	}
	if !enveloped {
		return errors.New("schema validator: auto-detect: body is not an enveloped Sharko document (apiVersion missing or not sharko.dev/v1 / legacy sharko.io/v1)")
	}

	var header struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(body, &header); err != nil {
		return fmt.Errorf("schema validator: auto-detect: reading kind: %w", err)
	}
	if header.Kind == "" {
		return errors.New("schema validator: auto-detect: envelope kind is empty")
	}
	if _, ok := v.schemas[header.Kind]; !ok {
		return fmt.Errorf("schema validator: auto-detect: unknown kind %q (known kinds: %s)", header.Kind, knownKinds(v))
	}
	return v.Validate(header.Kind, body)
}

// LogValidationFailure writes a structured slog.Error for a validation
// failure. Helper so the three reader-path wiring sites
// (LoadManagedClusters, ParseClusterAddons, ParseAddonsCatalog) emit
// identical log shapes — log scrapers see one consistent
// schema_validation_failed event regardless of which reader hit it.
//
// resource is a free-form locator (e.g. "managed-clusters.yaml" or a
// commit-relative path) included for operator triage; the reader passes
// whatever context it has at the call site.
func LogValidationFailure(resource string, failure *ValidationFailure) {
	if failure == nil {
		return
	}
	slog.Error(
		"schema_validation_failed",
		"resource", resource,
		"kind", failure.Kind,
		"violation_count", len(failure.Violations),
		"validation_errors", failure.Violations,
	)
}

// yamlToJSONAny decodes YAML body into the supplied target via the
// JSON bridge: yaml.v3 unmarshal → json.Marshal → json.Unmarshal. The
// double-encode is the canonical workaround for yaml.v3 producing
// map[any]any values that jsonschema cannot iterate; encoding to JSON
// normalises every map key to a string. The cost is negligible for the
// envelope sizes Sharko sees (kilobytes, not megabytes) and the
// alternative — a hand-written walker — would be a maintenance trap.
func yamlToJSONAny(body []byte, out *any) error {
	var raw any
	if err := yaml.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("yaml unmarshal: %w", err)
	}
	normalised := normaliseYAMLForJSON(raw)
	jsonBytes, err := json.Marshal(normalised)
	if err != nil {
		return fmt.Errorf("yaml→json marshal: %w", err)
	}
	if err := json.Unmarshal(jsonBytes, out); err != nil {
		return fmt.Errorf("json unmarshal: %w", err)
	}
	return nil
}

// normaliseYAMLForJSON walks a yaml.v3-produced value tree and
// converts every map[any]any into map[string]any. yaml.v3 returns
// interface keys for generic decodes; encoding/json requires string
// keys. Other shapes are returned unchanged.
func normaliseYAMLForJSON(in any) any {
	switch v := in.(type) {
	case map[any]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[fmt.Sprintf("%v", k)] = normaliseYAMLForJSON(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = normaliseYAMLForJSON(val)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = normaliseYAMLForJSON(val)
		}
		return out
	default:
		return v
	}
}

// flattenViolations walks a santhosh-tekuri *ValidationError tree and
// returns every leaf violation as a human-readable "instanceLocation:
// message" string. The library returns a tree where the root carries
// the top-level location and Causes contains nested failures; we want
// the flat list because that's what the operator needs to see — each
// concrete violation, once. Internal nodes that have causes but no
// useful message of their own are skipped to keep the list compact.
func flattenViolations(ve *jsonschema.ValidationError) []string {
	if ve == nil {
		return []string{"<nil validation error>"}
	}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if e == nil {
			return
		}
		// Leaf nodes have no further Causes — emit their message.
		// Internal nodes with Causes still get emitted if they carry a
		// non-empty Message (some library paths set both); deduplicate
		// the obvious "X does not validate" wrappers that don't add
		// information.
		if len(e.Causes) == 0 {
			loc := e.InstanceLocation
			if loc == "" {
				loc = "/"
			}
			out = append(out, fmt.Sprintf("%s: %s", loc, e.Message))
			return
		}
		// Internal node with causes — descend. We deliberately do NOT
		// emit the wrapper message ("doesn't validate with ...") to
		// keep the list focused on actionable leaf violations.
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	if len(out) == 0 {
		// Defensive fall-back: if the walk produced nothing, surface
		// the library's own Error() string so we never lose
		// information.
		out = []string{ve.Error()}
	}
	return out
}

// knownKinds returns a sorted-ish comma-separated list of the kinds
// the validator has compiled. Used in error messages so operators can
// see which kinds are valid without grepping the source. Map iteration
// order is non-deterministic in Go but Sharko only has two kinds
// today, so a fixed ordering keeps the error string stable and
// testable.
func knownKinds(v *Validator) string {
	// Hard-coded order for stability — same convention as the
	// generator's per-kind iteration in cmd/schema-gen/main.go.
	var known []string
	for _, k := range []string{KindManagedClusters, KindAddonCatalog, KindDefaultAddons, KindMarketplaceSources} {
		if _, ok := v.schemas[k]; ok {
			known = append(known, k)
		}
	}
	return strings.Join(known, ", ")
}

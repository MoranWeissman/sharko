// JSON Schema generator — the reusable core of the cmd/schema-gen binary.
// Lives in this package (rather than under cmd/) so:
//
//  1. Tests can exercise the generator and validate outputs without
//     spawning a subprocess.
//  2. The runtime validator can reuse the canonical schema bytes if we
//     ever decide to embed schemas via go:embed rather than refetching
//     from disk.
//
// The generator uses github.com/invopop/jsonschema. Key choices and why:
//
//   - We tag every Sharko-managed Go envelope type with parallel `json` +
//     `yaml` struct tags that use the same key (e.g. `apiVersion`).
//     invopop/jsonschema only reflects `json` tags — yaml support was
//     removed upstream — but because our tags are parallel-named, the
//     generated schema property names match exactly what yaml.v3 reads on
//     disk. If a future Sharko type diverges (json key != yaml key) the
//     schema will silently desync — keep the parity invariant.
//
//   - We set Reflector.DoNotReference = true so the schemas inline every
//     type instead of $ref'ing into a $defs map. This produces a single
//     self-contained document that editor yaml-language-server can consume
//     without resolving cross-references (it has poor $ref UX) and that a
//     human can read top-to-bottom. The schemas are small (a handful of
//     types) so duplication cost is negligible.
//
//   - We set Reflector.ExpandedStruct = true so the root schema is the
//     envelope itself (apiVersion / kind / metadata / spec at the top
//     level) rather than a $ref into a $defs entry named after the
//     generic instantiation. Without this the root would be
//     `{"$ref": "#/$defs/Envelope[github.com/MoranWeissman/...]"}` which
//     leaks Go-internal naming into the public schema.
//
//   - We pin `$schema: https://json-schema.org/draft/2020-12/schema` to
//     match the dialect Story 9.4's validator (santhosh-tekuri/jsonschema
//     v5) will run against.
//
//   - We pin `$id: https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/<name>` to the URL announced
//     in `# yaml-language-server: $schema=...` headers emitted by the
//     writers (see internal/models/cluster.go SchemaHeader and
//     internal/config/parser.go AddonCatalogSchemaHeader). Editors
//     deduplicate by $id, so matching the header URL is mandatory.
//
//   - We inject `kind` as a `const` per schema so a managed-clusters file
//     mistakenly authored with `kind: AddonCatalog` (or vice-versa) is
//     rejected at validation time rather than silently parsed into the
//     wrong domain model.
//
//   - Generic-instantiation naming: reflecting schema.Envelope[Spec]
//     directly would leak names like
//     `Envelope[github.com/MoranWeissman/sharko/internal/models.ManagedClustersSpec]`
//     into $defs. We sidestep this with per-kind anonymous wrapper structs
//     defined in the call sites below; the wrappers mirror Envelope[T]'s
//     field set + json tags exactly. Any future Envelope[T] field-set
//     change MUST be mirrored in BOTH wrappers or the generated schema
//     diverges from what writers emit. The TestGenerator_EnvelopeParity
//     test in generator_test.go pins this invariant.
//
// Output is JSON via encoding/json — encoding/json sorts map keys
// deterministically, and invopop emits structs as orderedmap.OrderedMap
// to preserve declaration order, so back-to-back runs produce byte-
// identical bytes.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
)

// ManagedClustersSchemaID is the public URL embedded in every generated
// managed-clusters.v1.json file's $id, and the URL emitted by Sharko writers
// in the `# yaml-language-server: $schema=` header line. Keeping the constant
// in this package (rather than re-declaring it in cmd/schema-gen/main.go)
// ensures the writer-emitted header URL and the generator-emitted $id can
// never drift apart.
const ManagedClustersSchemaID = "https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/managed-clusters.v1.json"

// AddonCatalogSchemaID is the public URL embedded in every generated
// addons-catalog.v1.json file's $id. Mirrors ManagedClustersSchemaID for the
// addon-catalog kind. The literal value matches AddonCatalogSchemaHeader in
// internal/config/parser.go.
const AddonCatalogSchemaID = "https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json"

// SchemaDialect is the JSON Schema dialect URL emitted as $schema in every
// generated file. Story 9.4's runtime validator (santhosh-tekuri/jsonschema
// v5) defaults to draft 2020-12 when this URL is present.
const SchemaDialect = "https://json-schema.org/draft/2020-12/schema"

// GenerateSchema reflects a per-kind envelope wrapper struct and returns
// deterministic JSON Schema bytes for it.
//
// Callers (cmd/schema-gen/main.go in this commit; potentially go:embed-based
// runtime loaders in later stories) hand in a zero-value pointer to a
// kind-specific wrapper struct whose Spec field has the concrete payload
// type. See the cmd/schema-gen/main.go call sites for examples — they
// declare local wrappers like:
//
//	type managedClustersDoc struct {
//	    APIVersion string                       `json:"apiVersion"`
//	    Kind       string                       `json:"kind"`
//	    Metadata   schema.Metadata              `json:"metadata"`
//	    Spec       models.ManagedClustersSpec   `json:"spec"`
//	}
//
// The wrapper duplicates schema.Envelope[T]'s public layout exactly so the
// emitted schema validates the same bytes that yaml.v3 produces from a
// `Envelope[ManagedClustersSpec]`. The parity is load-bearing — see the
// package doc comment.
//
// id, title, description, and kindConst are baked into the top-level
// schema. kindConst constrains the `kind` field to a single literal so a
// cross-kind file (e.g. `kind: AddonCatalog` in a managed-clusters file) is
// a validation error rather than a silent mis-route.
func GenerateSchema(target any, id, title, description, kindConst string) ([]byte, error) {
	reflector := &jsonschema.Reflector{
		// Inline every type — a single self-contained document, no $ref
		// resolution required by yaml-language-server.
		DoNotReference: true,
		// Root schema is the envelope itself, not a $ref into $defs.
		ExpandedStruct: true,
	}

	s := reflector.Reflect(target)

	// Pin top-level identity. invopop picks a Go-package-derived $id by
	// default; override with the public Sharko schema URL so editors
	// deduplicate against the URL embedded in writer headers.
	s.ID = jsonschema.ID(id)
	s.Version = SchemaDialect
	s.Title = title
	s.Description = description

	// Constrain kind to a single literal per schema.
	if kindSchema, ok := s.Properties.Get("kind"); ok {
		kindSchema.Const = kindConst
		// Const implies a fixed value; clear redundant type/enum.
		kindSchema.Type = ""
		kindSchema.Enum = nil
	}

	// Constrain apiVersion the same way — wrong apiVersion becomes a
	// validation error rather than a quiet fall-through.
	if apiVersionSchema, ok := s.Properties.Get("apiVersion"); ok {
		apiVersionSchema.Const = APIVersion
		apiVersionSchema.Type = ""
		apiVersionSchema.Enum = nil
	}

	// MarshalIndent for human readability and stable map-key ordering.
	// encoding/json sorts map keys deterministically; orderedmap entries
	// preserve insertion order (which invopop derives from struct field
	// declaration order). Together these guarantee byte-identical output
	// across runs — the invariant the CI drift gate depends on.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	// SetEscapeHTML(false) keeps URLs in $id / $schema readable rather
	// than emitting & for &.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, fmt.Errorf("encoding schema: %w", err)
	}
	return buf.Bytes(), nil
}

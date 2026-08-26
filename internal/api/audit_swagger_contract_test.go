package api

// audit_swagger_contract_test.go — the audit endpoints' published contract.
//
// GET /audit used to declare `map[string]interface{}` and write a bare map.
// So `docs/swagger/swagger.json` described the response as an object with
// arbitrary properties, carried no audit.Entry definition at all, and named
// none of an audit entry's fields — including `changes`, which ruling (f)
// added so a reader can tell "this wrote something" from "this deliberately
// wrote nothing" from "this was a read-only check". The field was real on
// the wire and absent from the contract, which for anyone writing a client
// off the spec is the same as not shipping it.
//
// These tests read the GENERATED spec, not the annotation. An annotation
// that is right while the committed spec is stale is exactly the failure CI
// already guards against elsewhere, and this check should fail the same way.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
)

const swaggerJSONPath = "../../docs/swagger/swagger.json"

// loadSwagger reads the committed OpenAPI document.
func loadSwagger(t *testing.T) map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(swaggerJSONPath))
	if err != nil {
		t.Fatalf("reading %s: %v", swaggerJSONPath, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parsing %s: %v", swaggerJSONPath, err)
	}
	return doc
}

// swaggerRefFor returns the $ref a path+method's 200 response points at.
func swaggerRefFor(t *testing.T, doc map[string]any, path string) string {
	t.Helper()
	paths, _ := doc["paths"].(map[string]any)
	item, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("the spec has no %s path at all", path)
	}
	get, ok := item["get"].(map[string]any)
	if !ok {
		t.Fatalf("the spec has no GET operation on %s", path)
	}
	responses, _ := get["responses"].(map[string]any)
	ok200, ok := responses["200"].(map[string]any)
	if !ok {
		t.Fatalf("%s GET declares no 200 response", path)
	}
	schema, ok := ok200["schema"].(map[string]any)
	if !ok {
		t.Fatalf("%s GET's 200 response has no schema", path)
	}
	ref, _ := schema["$ref"].(string)
	if ref == "" {
		t.Fatalf("%s GET's 200 response is not a named type — it is %v.\n"+
			"An untyped response documents none of its fields. Point @Success at a real struct.",
			path, schema)
	}
	return ref
}

// swaggerDefinition resolves a "#/definitions/X" reference.
func swaggerDefinition(t *testing.T, doc map[string]any, ref string) map[string]any {
	t.Helper()
	const prefix = "#/definitions/"
	if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
		t.Fatalf("unexpected reference shape %q", ref)
	}
	defs, _ := doc["definitions"].(map[string]any)
	def, ok := defs[ref[len(prefix):]].(map[string]any)
	if !ok {
		t.Fatalf("the spec references %s but has no such definition", ref)
	}
	return def
}

// TestAuditSwagger_ListResponseIsATypedShape fails if GET /audit goes back to
// an untyped map.
func TestAuditSwagger_ListResponseIsATypedShape(t *testing.T) {
	doc := loadSwagger(t)
	def := swaggerDefinition(t, doc, swaggerRefFor(t, doc, "/audit"))
	props, _ := def["properties"].(map[string]any)
	for _, want := range []string{"entries", "count"} {
		if _, ok := props[want]; !ok {
			t.Errorf("GET /audit's response type does not document a %q field", want)
		}
	}
	entries, _ := props["entries"].(map[string]any)
	if entries["type"] != "array" {
		t.Fatalf("GET /audit's entries field is documented as %v, not an array", entries["type"])
	}
	items, _ := entries["items"].(map[string]any)
	if ref, _ := items["$ref"].(string); ref == "" {
		t.Fatalf("GET /audit's entries array has no element type, so no entry field is documented")
	}
}

// TestAuditSwagger_ChangesFieldIsInTheSpec is the item-1/2 guard: the field
// AND its four values must be readable from the published document, on both
// the list endpoint and the stream.
func TestAuditSwagger_ChangesFieldIsInTheSpec(t *testing.T) {
	doc := loadSwagger(t)

	// The list endpoint, through its entries array.
	listDef := swaggerDefinition(t, doc, swaggerRefFor(t, doc, "/audit"))
	listProps, _ := listDef["properties"].(map[string]any)
	entries, _ := listProps["entries"].(map[string]any)
	items, _ := entries["items"].(map[string]any)
	entryRef, _ := items["$ref"].(string)

	// The stream, whose 200 IS one entry.
	streamRef := swaggerRefFor(t, doc, "/audit/stream")

	if entryRef != streamRef {
		t.Errorf("GET /audit's entry type (%s) and GET /audit/stream's payload type (%s) are "+
			"different definitions. They are the same struct on the wire; a client reading "+
			"the spec must not be told otherwise.", entryRef, streamRef)
	}

	for _, ref := range []string{entryRef, streamRef} {
		def := swaggerDefinition(t, doc, ref)
		props, _ := def["properties"].(map[string]any)
		changes, ok := props["changes"].(map[string]any)
		if !ok {
			t.Fatalf("%s does not document a \"changes\" field. The field is on the wire "+
				"(internal/audit.Entry) — a spec that omits it hides it from every client "+
				"author who reads the contract instead of the source.", ref)
		}
		if changes["type"] != "string" {
			t.Errorf("%s documents changes as %v, not a string", ref, changes["type"])
		}
		var got []string
		raw, _ := changes["enum"].([]any)
		for _, v := range raw {
			if s, ok := v.(string); ok {
				got = append(got, s)
			}
		}
		sort.Strings(got)
		want := []string{
			string(audit.ChangesApplied),
			string(audit.ChangesNone),
			string(audit.ChangesNotApplicable),
			string(audit.ChangesMayBeApplied),
		}
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("%s documents changes with enum %v; the type's own values are %v", ref, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s documents changes with enum %v; the type's own values are %v", ref, got, want)
			}
		}
	}
}

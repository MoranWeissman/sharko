package api

// health_swagger_contract_test.go — the published health vocabulary.
//
// The connection health word has FOUR values since B13. The fleet row's
// comment was updated to name all four; the connection endpoint's was not,
// and it kept saying "connected, unavailable or not_checked" — the one
// endpoint B13 taught to return "unknown". That comment is what swag turns
// into the field description in docs/swagger/swagger.json, and nothing
// else under docs/ names the health words at all, so the generated spec is
// the ONLY published statement of this contract. An integrator writing a
// three-way switch off it gets a value the spec never mentioned.
//
// This reads the GENERATED document, like the audit-schema guard beside
// it. An annotation that is right while the committed spec is stale is the
// same failure with an extra step.
//
// The list of words is read out of the Go constants with go/parser, not
// written out here. A fifth health state added to that const block becomes
// a requirement on both descriptions the moment it is declared, without
// anybody remembering to come back to this file.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// healthWordsFromSource reads every healthState* constant value out of the
// file that declares them.
func healthWordsFromSource(t *testing.T) []string {
	t.Helper()
	const src = "connection_reconciliation.go"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", src, err)
	}

	var words []string
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.CONST {
			return true
		}
		for _, spec := range decl.Specs {
			vs, isValue := spec.(*ast.ValueSpec)
			if !isValue || len(vs.Names) != len(vs.Values) {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "healthState") {
					continue
				}
				lit, isLit := vs.Values[i].(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					continue
				}
				v, unqErr := strconv.Unquote(lit.Value)
				if unqErr != nil {
					continue
				}
				words = append(words, v)
			}
		}
		return true
	})

	sort.Strings(words)
	if len(words) < 4 {
		t.Fatalf("only %d healthState constants found in %s (%v) — the reader is broken, not the code. "+
			"Without them this test would demand nothing.", len(words), src, words)
	}
	return words
}

// TestHealthSwagger_BothSurfacesPublishEveryValue is the guard.
func TestHealthSwagger_BothSurfacesPublishEveryValue(t *testing.T) {
	words := healthWordsFromSource(t)
	doc := loadSwagger(t)

	for _, surface := range []struct {
		what        string
		description string
	}{
		{
			what:        "GET /clusters/{name}/connection-reconciliation → health.state",
			description: connectionHealthStateDescription(t, doc),
		},
		{
			what:        "GET /system/managed-secrets → cluster_connection_secrets[].health",
			description: fleetRowHealthDescription(t, doc),
		},
	} {
		for _, word := range words {
			if !strings.Contains(surface.description, word) {
				t.Errorf("the published spec for %s does not name the health value %q.\n\n"+
					"It says:\n%s\n\n"+
					"The generated spec is the only place the health vocabulary is published. A "+
					"value the code returns and the contract omits is a value every client author "+
					"who reads the contract will fail to handle.",
					surface.what, word, surface.description)
			}
		}
	}
}

// connectionHealthStateDescription walks the spec from the endpoint to the
// field, so a renamed definition fails here rather than silently checking
// something else.
func connectionHealthStateDescription(t *testing.T, doc map[string]any) string {
	t.Helper()
	view := swaggerDefinition(t, doc, swaggerRefFor(t, doc, "/clusters/{name}/connection-reconciliation"))
	props, _ := view["properties"].(map[string]any)
	health, ok := props["health"].(map[string]any)
	if !ok {
		t.Fatal("the reconciliation view's published type documents no health field at all")
	}
	ref, _ := health["$ref"].(string)
	if ref == "" {
		t.Fatalf("the reconciliation view's health field is not a named type — it is %v", health)
	}
	healthDef := swaggerDefinition(t, doc, ref)
	healthProps, _ := healthDef["properties"].(map[string]any)
	state, ok := healthProps["state"].(map[string]any)
	if !ok {
		t.Fatalf("%s documents no state field", ref)
	}
	description, _ := state["description"].(string)
	if strings.TrimSpace(description) == "" {
		t.Fatalf("%s documents state with no description at all, so it names no values", ref)
	}
	return description
}

func fleetRowHealthDescription(t *testing.T, doc map[string]any) string {
	t.Helper()
	response := swaggerDefinition(t, doc, swaggerRefFor(t, doc, "/system/managed-secrets"))
	props, _ := response["properties"].(map[string]any)
	rows, ok := props["cluster_connection_secrets"].(map[string]any)
	if !ok {
		t.Fatal("the managed-secrets response documents no cluster_connection_secrets field")
	}
	items, _ := rows["items"].(map[string]any)
	ref, _ := items["$ref"].(string)
	if ref == "" {
		t.Fatal("cluster_connection_secrets has no element type, so no row field is documented")
	}
	rowDef := swaggerDefinition(t, doc, ref)
	rowProps, _ := rowDef["properties"].(map[string]any)
	health, ok := rowProps["health"].(map[string]any)
	if !ok {
		t.Fatalf("%s documents no health field", ref)
	}
	description, _ := health["description"].(string)
	if strings.TrimSpace(description) == "" {
		t.Fatalf("%s documents health with no description at all, so it names no values", ref)
	}
	return description
}

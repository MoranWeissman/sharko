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
	"regexp"
	"sort"
	"strconv"
	"strings"
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

// The wording this repository settled on: durable repository history is
// "reviewable Git/PR history", and Sharko's own Activity feed — a ring buffer
// in memory that empties on every restart — is the "in-memory activity
// history". That feed is never called an audit trail, because the name
// promises a durable record Sharko does not keep.
const (
	retiredActivityTerm = "audit trail"
	correctActivityTerm = "in-memory activity history"
)

// retiredActivityTermPattern is how the ban is actually matched: the two words
// with a hyphen OR whitespace between them, read off already-flattened text.
//
// A plain strings.Contains on retiredActivityTerm missed "audit-trail". The
// flatteners treat whitespace and the two-character escapes as separators and
// leave a hyphen exactly where it is, so the hyphenated spelling walked straight
// past a guard built to catch a wrapped line. It is the one variant a person
// could plausibly type into a Go doc comment, and to a reader it promises the
// same durable record the ban exists to prevent.
//
// The hyphen is handled HERE and deliberately not in the flatteners. Turning
// hyphens into spaces there would flatten every description to "in memory
// activity history" while correctActivityTerm still reads "in-memory", so
// TestAuditSwagger_TheActivityHistoryIsNamedInTheSpec would go red — one hole
// closed by opening a worse one. Both callers, the sweep over the generated
// files and the sweep over the parsed descriptions, use this one pattern, so
// neither path can be the narrow one.
//
// retiredActivityTerm stays the phrase every failure message names. A stranger
// reading a red build needs to be told "audit trail", not a regular expression.
//
// The match is a substring with no word boundary, which is what catches the
// plural "audit trails". The accepted cost is that a longer word starting with
// "trail" is caught too — "audit trailing", "audit trailer", and, because the
// file sweep flattens a whole file rather than one description at a time, a
// description whose last word is "audit" followed by a key beginning "trail",
// which flattens to "audit trailhead: yes" and flags. Measured on all three
// generated files as they stand: zero hits, so no such join exists today. A word
// boundary was rejected because it would lose the plural, and the plural is far
// likelier to be typed than any of those.
//
// Honest limitation: markdown emphasis splitting the phrase is not caught.
// "**audit** trail" keeps its asterisks through flattening, and swag descriptions
// render as markdown in Redoc, so a reader would see the retired term while this
// stays quiet. Contrived rather than likely, and written down here instead of
// paid for in matcher complexity — the same way
// TestTheReadmeSaysItsStatusOnceInItsOpening in
// tests/serverrender/bf12_banned_wording_test.go records the shape its counts
// cannot see. A reviewer still has to read the wording.
//
// Not covered because nothing can write them: a line break landing INSIDE either
// word, and a zero-width character between them (U+200B, U+2060, U+00AD). And a
// literal backslash-n between the words — the escape written twice — is
// correctly NOT caught, because it renders as a visible \n rather than a space,
// so the published text does not say the phrase.
var retiredActivityTermPattern = regexp.MustCompile(`audit[\s-]+trail`)

// swag writes three files from the same annotations and all three ship. A ban
// proved in one of them while another still carries the wrong term is not a ban.
const (
	swaggerYAMLPath   = "../../docs/swagger/swagger.yaml"
	swaggerDocsGoPath = "../../docs/swagger/docs.go"
)

// flattenWording collapses every run of whitespace — line breaks included — to
// a single space and lowercases the result, so a phrase that got wrapped across
// two lines still reads as one phrase.
//
// This is not a nicety. swag copies a Go doc comment's line breaks straight
// into the description as real newline characters: 287 of the 1403 descriptions
// in swagger.json carry at least one. Go comments wrap around column 72, so a
// two-word phrase landing on a line boundary is ordinary rather than exotic.
// Redoc and Swagger UI reflow those breaks into one visible paragraph, so the
// raw string is not what a reader sees, and a literal single-space match cannot
// see "audit" + newline + "trail" at all — the term ships and the guard stays
// silent.
//
// Same mechanism as flattenForWording in
// tests/serverrender/bf12_banned_wording_test.go, which this repository added
// after the identical failure over there. It does NOT strip comment markers the
// way that helper does, and does not need to: swag drops the "// " prefix when
// it reads a doc comment, so no description value or generated file carries one.
func flattenWording(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// flattenEscapedWording is flattenWording for a file read as RAW TEXT, where a
// line break inside a string may be the TWO characters backslash-n and so is
// not whitespace at all. It treats those escapes as whitespace as well.
//
// All three generated files need this, for two different reasons:
//
//   - swagger.json and docs.go: JSON encodes a newline inside a string as the
//     two-character escape, and docs.go holds that same JSON inside a Go RAW
//     string literal, so the escapes survive as literal character pairs in both.
//   - swagger.yaml: descriptions are |- block scalars holding real newlines, so
//     whitespace collapsing is enough — but a double-quoted YAML scalar could
//     carry the escape too, and treating it as a break there is also correct.
//
// Collapsing whitespace alone leaves "audit\ntrail" as one unreadable word, so a
// check built that way is blind in exactly the files it was added for. Proved by
// experiment, not assumed: with the phrase planted and regenerated, the raw
// single-space term count was 0 in all three files and every break was an escape
// in two of them.
func flattenEscapedWording(s string) string {
	for _, escape := range []string{`\r\n`, `\n`, `\r`, `\t`} {
		s = strings.ReplaceAll(s, escape, " ")
	}
	return flattenWording(s)
}

// retiredTermHit reports the flattened text around where a generated file's raw
// body says the retired term, or "" if it does not say it.
//
// The sweep over the generated files and the regression fixture below both call
// THIS function, so the fixture pins the matcher that really runs rather than a
// copy of its logic. There is deliberately no per-file choice of normalisation:
// one escape-aware pass covers every shape any of the three files can hold, and
// a routing table is one more thing that can send a file to the blind matcher.
func retiredTermHit(body string) string {
	flat := flattenEscapedWording(body)
	at := retiredActivityTermPattern.FindStringIndex(flat)
	if at == nil {
		return ""
	}
	from := at[0] - 120
	if from < 0 {
		from = 0
	}
	to := at[1] + 120
	if to > len(flat) {
		to = len(flat)
	}
	return flat[from:to]
}

// swaggerDescription is one description string and the JSON path it was found at.
type swaggerDescription struct {
	path string
	text string
}

// collectSwaggerDescriptions walks the whole document and appends every
// description string in it, with the JSON path it was found at. Every depth,
// every map and every slice — the two descriptions that were wrong once are not
// the interesting set; the next annotation somebody writes is.
//
// A slice and not a map keyed by path: OpenAPI path keys contain literal
// slashes, so "/paths" + "/" + "/audit" renders as "/paths//audit" and two
// different JSON locations could render to one key. A map would silently
// discard one of them, and a discarded description is never checked. The swept
// count is quoted as evidence in the log line below, and a count that is
// silently a lower bound is not evidence. Map keys are sorted before recursing,
// so the order stays deterministic.
func collectSwaggerDescriptions(node any, path string, out *[]swaggerDescription) {
	switch n := node.(type) {
	case map[string]any:
		keys := make([]string, 0, len(n))
		for k := range n {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := path + "/" + k
			if s, ok := n[k].(string); ok && k == "description" {
				*out = append(*out, swaggerDescription{path: child, text: s})
				continue
			}
			collectSwaggerDescriptions(n[k], child, out)
		}
	case []any:
		for i, v := range n {
			collectSwaggerDescriptions(v, path+"/"+strconv.Itoa(i), out)
		}
	}
}

// TestAuditSwagger_NoDescriptionCallsTheActivityHistoryAnAuditTrail keeps the
// retired term out of the published spec.
//
// MkDocs copies this same swagger.json into the built site as
// api/openapi.json (docs/hooks/copy_openapi.py, wired in mkdocs.yml), so a
// description here is public documentation, not an internal note. Two
// annotations on the orchestrator's aggregate counters used to call the
// memory-only feed an audit trail and shipped that wording to Read the Docs.
//
// This walks descriptions rather than the two known sites so that a new
// annotation anywhere in the API cannot reintroduce the term quietly.
func TestAuditSwagger_NoDescriptionCallsTheActivityHistoryAnAuditTrail(t *testing.T) {
	doc := loadSwagger(t)

	var descriptions []swaggerDescription
	collectSwaggerDescriptions(doc, "", &descriptions)
	if len(descriptions) == 0 {
		t.Fatalf("walked %s and found no description at all — a sweep that reads nothing "+
			"passes on every document, including an empty one", swaggerJSONPath)
	}
	t.Logf("swept %d description strings in %s", len(descriptions), swaggerJSONPath)

	for _, d := range descriptions {
		// Flattened, not raw: swag keeps the doc comment's line breaks, so the
		// term arrives split across two lines as often as not. See flattenWording.
		// Matched through the shared pattern, not a Contains on the constant, so
		// this path sees the hyphenated spelling as well — see
		// retiredActivityTermPattern.
		if !retiredActivityTermPattern.MatchString(flattenWording(d.text)) {
			continue
		}
		t.Errorf("%s\nat JSON path %s\ncalls Sharko's memory-only Activity feed an %q. "+
			"That feed keeps no durable record, so say %q instead.\n"+
			"Fix the Go doc comment the description is generated from, then re-run\n"+
			"  swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal\n"+
			"The description reads:\n%s",
			swaggerJSONPath, d.path, retiredActivityTerm, correctActivityTerm, d.text)
	}
}

// TestAuditSwagger_TheActivityHistoryIsNamedInTheSpec is the other half of the
// ban, and the half that was missing.
//
// A check that only forbids the wrong words is satisfied by saying nothing.
// Delete the sentence, or blur it into something vague, and the sweep above
// stays green — so the wording it exists to protect is not actually protected.
// This repository has been burned by that exact shape before: a wrong
// explanation survived four rounds of review because its only test asserted the
// text was not empty.
//
// So: name the two places the correct term really lives and require it there.
// This is also what makes the constant's name honest — before this,
// correctActivityTerm appeared in one place only, the text of a failure message.
func TestAuditSwagger_TheActivityHistoryIsNamedInTheSpec(t *testing.T) {
	doc := loadSwagger(t)

	var descriptions []swaggerDescription
	collectSwaggerDescriptions(doc, "", &descriptions)

	// The two aggregate-counter descriptions whose wording this whole change is
	// about. Both explain that fanout.Count feeds four surfaces, and both have
	// to name the memory-only feed by what it is.
	const defPrefix = "/definitions/github_com_MoranWeissman_sharko_internal_orchestrator."
	for _, want := range []string{
		defPrefix + "AdoptClustersResult/properties/outcome/description",
		defPrefix + "BatchResult/properties/outcome/description",
	} {
		found := false
		for _, d := range descriptions {
			if d.path != want {
				continue
			}
			found = true
			// Flattened for the same reason as the ban: the term sits one word
			// after a line break in both descriptions today, and any re-wrap at
			// a different column splits the term itself.
			if !strings.Contains(flattenWording(d.text), correctActivityTerm) {
				t.Errorf("%s\nat JSON path %s\nno longer says %q.\n"+
					"Sharko's Activity feed is a ring buffer in memory that empties on every "+
					"restart, and this description is one of only two places the published spec "+
					"says so. Do not remove it and do not soften it into something vaguer — a "+
					"client author reads this instead of the source.\n"+
					"The description reads:\n%s",
					swaggerJSONPath, want, correctActivityTerm, d.text)
			}
		}
		if !found {
			t.Errorf("%s has no description at JSON path %s at all.\n"+
				"Either the field or the type was renamed — in which case fix this path — or the "+
				"documented explanation of the in-memory Activity feed was dropped, which is the "+
				"regression this test exists to catch.", swaggerJSONPath, want)
		}
	}
}

// TestAuditSwagger_NoGeneratedSwaggerFileSaysAuditTrail closes the gap the
// description sweep cannot: that sweep parses swagger.json, and swag writes
// THREE files from the same annotations. All three are committed and all three
// ship, so the ban has to hold in every one of them — otherwise it can be
// satisfied in one generated file while another still carries the wrong term.
//
// A hand-edit to one file does get caught in CI, by the separate job that
// regenerates all three and diffs the directory. Nothing local caught it, which
// is why go test could pass with the retired term live in swagger.yaml.
//
// These are read as raw text, not parsed. The point is that the bytes that ship
// do not carry the phrase, whatever structure holds it.
func TestAuditSwagger_NoGeneratedSwaggerFileSaysAuditTrail(t *testing.T) {
	for _, path := range []string{swaggerJSONPath, swaggerYAMLPath, swaggerDocsGoPath} {
		body, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if len(body) == 0 {
			t.Fatalf("%s is empty — a sweep over nothing passes on every file", path)
		}
		t.Logf("swept %d bytes of %s", len(body), path)

		if hit := retiredTermHit(string(body)); hit != "" {
			t.Errorf("%s calls Sharko's memory-only Activity feed an %q. That feed keeps no "+
				"durable record, so say %q instead.\n"+
				"Fix the Go doc comment the text is generated from — never this file — then re-run\n"+
				"  swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal\n"+
				"Flattened text around the hit:\n%s",
				path, retiredActivityTerm, correctActivityTerm, hit)
		}
	}
}

// TestAuditSwagger_TheWordingCheckSeesALineBreak pins the hole that made the
// first version of this guard useless, so the fix cannot rot back out.
//
// What happened: the check was a literal single-space match, a reviewer planted
// "the audit\ntrail" into a Go doc comment, regenerated, and the guard swept
// 1403 descriptions and said nothing while all three generated files shipped the
// phrase.
//
// Every body below is a shape taken from a real generated file after planting
// that phrase and regenerating — including which files encode the break as an
// escape rather than as whitespace, because that was measured and not assumed.
// Each case goes through retiredTermHit, the function the sweep actually calls,
// rather than a copy of its logic: a fixture that tests a duplicate protects
// nothing.
func TestAuditSwagger_TheWordingCheckSeesALineBreak(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fromFile string
		body     string
		want     bool
	}{
		{
			// The shape swagger.json really ships. JSON has no literal newline
			// inside a string, so swag's copy of the doc comment break arrives as
			// the two characters backslash-n — not as whitespace.
			name:     "json, two-character newline escape",
			fromFile: swaggerJSONPath,
			body:     `"description": "Counted from results[].status by fanout.Count, which the audit\ntrail, the printed summary and the CLI's exit code all read."`,
			want:     true,
		},
		{
			// The shape swagger.yaml really ships: |- block scalars, so the break
			// is a real newline plus about ten spaces of indent.
			name:     "yaml, real newline plus block-scalar indent",
			fromFile: swaggerYAMLPath,
			body: "        description: |-\n          Counted from results[].status, which the audit\n" +
				"          trail and the printed summary both read.",
			want: true,
		},
		{
			// docs.go holds that same JSON inside a Go RAW string literal, so the
			// escapes survive as literal character pairs there too.
			name:     "docs.go, two-character newline escape in a raw string literal",
			fromFile: swaggerDocsGoPath,
			body:     `"description": "Counted from results[].status, which the audit\ntrail and the printed summary both read."`,
			want:     true,
		},
		{
			name:     "docs.go, newline escape followed by a tab escape",
			fromFile: swaggerDocsGoPath,
			body:     `"description": "which the audit\n\ttrail and the printed summary both read."`,
			want:     true,
		},
		{
			// A real newline has to be caught as well. swag writes the escape today,
			// but the sweep reads whole files and nothing guarantees every future
			// break arrives escaped.
			name:     "a real newline, whatever wrote it",
			fromFile: "any",
			body:     "Counted from results[].status, which the audit\ntrail and the printed summary both read.",
			want:     true,
		},
		{
			// The hyphenated spelling. A hyphen is not whitespace and is not one
			// of the escapes, so the flatteners leave it exactly where it is and
			// "audit-trail" walked past every earlier version of this guard. It
			// is the one variant a person could plausibly type into a Go doc
			// comment, and to a reader it promises the same durable record the
			// ban exists to prevent, so it has to be caught here.
			name:     "hyphenated, which reads the same to a reader",
			fromFile: "any",
			body:     "Counted from results[].status, which the audit-trail and the printed summary both read.",
			want:     true,
		},
		{
			// The ban stays narrow. "audit log" is the real name of a real endpoint
			// (/api/v1/audit) and is not retired. Measured: it appears 7 times in
			// each generated file, across 5 descriptions and 2 summaries — a check
			// that flagged it would turn the build red for nothing.
			name:     "audit log stays legal, wrapped across a real newline",
			fromFile: swaggerYAMLPath,
			body:     "Returns the audit\nlog for this server, newest entry first.",
			want:     false,
		},
		{
			name:     "audit log stays legal wrapped across an escape too",
			fromFile: swaggerDocsGoPath,
			body:     `"summary": "Query the audit\nlog"`,
			want:     false,
		},
	} {
		got := retiredTermHit(tc.body) != ""
		if got == tc.want {
			continue
		}
		if tc.want {
			t.Errorf("%s: retiredTermHit missed the retired term in this shape from %s, so the "+
				"guard is blind to a phrase a reader still reads as the retired term — the exact hole "+
				"this fixture exists to hold shut:\n%s", tc.name, tc.fromFile, tc.body)
			continue
		}
		t.Errorf("%s: retiredTermHit flagged text that is allowed. Only the two-word phrase "+
			"%q is banned; %q is the real name of a real endpoint and must stay legal:\n%s",
			tc.name, retiredActivityTerm, "audit log", tc.body)
	}
}

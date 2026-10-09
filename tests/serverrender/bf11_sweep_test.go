package serverrender

// bf11_sweep.go — the machinery the BF11 guards share: render the shipped
// chart as raw text, parse it as a plain tree, and walk every place a value
// can end up.
//
// # Why the sweep walks a plain tree instead of typed structs
//
// render_test.go reads the chart through partial Go structs, which is right
// for a question like "does this Pod set enableServiceLinks". It is wrong for
// "is this password anywhere in the render", because a typed struct can only
// find a value in a field somebody remembered to declare. The carrier that
// leaks is the one nobody thought of — container arguments, an annotation, a
// label, an environment value derived from something else. So the sweep
// decodes each document into map[string]any and walks the whole thing, keys
// and values alike, and reports the PATH of every hit so a failure says where.
//
// # Why every carrier is positive-controlled
//
// A sweep that has never found anything has proved nothing. Every guard that
// asserts "the value is not here" is paired with a check that the same sweep
// DOES find a planted value, in that same carrier. Where the chart has no
// values door into a carrier (there is no way to set a container's argv on the
// Sharko container), the control is a hand-built document instead, and the
// guard says so in its own words rather than pretending the render proved it.

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// renderResult is one `helm template` run.
type renderResult struct {
	// Raw is everything helm wrote to stdout.
	Raw string
	// Docs is every non-empty document, parsed as a plain tree.
	Docs []map[string]any
}

// runHelmTemplate renders charts/sharko and returns stdout, stderr and the
// error. It never fails the test itself: the address guards need the failing
// runs as much as the passing ones.
func runHelmTemplate(t *testing.T, extraArgs ...string) (stdout, stderr string, err error) {
	t.Helper()
	helm, lookErr := exec.LookPath("helm")
	if lookErr != nil {
		t.Fatalf("helm is not on PATH, so the shipped chart cannot be rendered: %v", lookErr)
	}
	root := repoRoot(t)
	args := append([]string{"template", "sharko", filepath.Join(root, "charts", "sharko")}, extraArgs...)
	cmd := exec.Command(helm, args...)
	cmd.Dir = root
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// renderChart renders the chart and fails the test if helm refuses.
func renderChart(t *testing.T, extraArgs ...string) renderResult {
	t.Helper()
	out, errText, err := runHelmTemplate(t, extraArgs...)
	if err != nil {
		t.Fatalf("helm template %v failed: %v\n%s", extraArgs, err, errText)
	}
	return parseRendered(t, out)
}

// parseRendered turns helm's stdout into documents. Rendering zero documents
// is fatal: a guard that walks an empty render passes without looking at
// anything.
func parseRendered(t *testing.T, out string) renderResult {
	t.Helper()
	res := renderResult{Raw: out}
	decoder := yaml.NewDecoder(strings.NewReader(out))
	for {
		var doc map[string]any
		if decodeErr := decoder.Decode(&doc); decodeErr != nil {
			break
		}
		if len(doc) == 0 {
			continue
		}
		res.Docs = append(res.Docs, doc)
	}
	if len(res.Docs) == 0 {
		t.Fatalf("charts/sharko rendered no documents at all, so nothing below is checking anything:\n%s", out)
	}
	return res
}

// docName is "Kind/name", the label a failure uses to say which object.
func bf11DocName(doc map[string]any) string {
	kind, _ := doc["kind"].(string)
	name := ""
	if md, ok := doc["metadata"].(map[string]any); ok {
		name, _ = md["name"].(string)
	}
	if kind == "" {
		kind = "<no kind>"
	}
	if name == "" {
		name = "<no name>"
	}
	return kind + "/" + name
}

// findText walks a parsed document and returns the path of every string —
// key or value — that contains needle. Paths look like
// spec.template.spec.containers[0].env[4].value, so a failure names the
// carrier rather than just saying "somewhere".
//
// It looks at keys as well as values on purpose: a value that ends up as a
// map key (an annotation name, a label name, a Secret key) is just as visible
// as one that ends up as a map value.
func findText(node any, needle string) []string {
	var hits []string
	var walk func(any, string)
	walk = func(n any, path string) {
		switch v := n.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				childPath := k
				if path != "" {
					childPath = path + "." + k
				}
				if strings.Contains(k, needle) {
					hits = append(hits, childPath+" (as a key)")
				}
				walk(v[k], childPath)
			}
		case map[any]any:
			// yaml.v3 gives map[string]any for string keys; this arm is here
			// so a non-string key can never make the sweep walk past a subtree
			// in silence.
			for k, child := range v {
				childPath := fmt.Sprintf("%s[%v]", path, k)
				if strings.Contains(fmt.Sprint(k), needle) {
					hits = append(hits, childPath+" (as a key)")
				}
				walk(child, childPath)
			}
		case []any:
			for i, child := range v {
				walk(child, fmt.Sprintf("%s[%d]", path, i))
			}
		case string:
			if strings.Contains(v, needle) {
				hits = append(hits, path)
			}
		default:
			// Numbers, booleans and nulls cannot carry a sentinel. They are
			// reached and skipped rather than not reached — the walk above
			// descends into every container type there is.
		}
	}
	walk(node, "")
	sort.Strings(hits)
	return hits
}

// sweep returns every hit across every document, labelled by object, skipping
// the objects named in skip. Skipping is how "the Secret is allowed to hold
// the password and nothing else is" gets said.
func (r renderResult) sweep(needle string, skip ...string) []string {
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	var hits []string
	for _, doc := range r.Docs {
		name := bf11DocName(doc)
		if skipped[name] {
			continue
		}
		for _, h := range findText(doc, needle) {
			hits = append(hits, name+": "+h)
		}
	}
	sort.Strings(hits)
	return hits
}

// docsByName indexes the render so a guard can ask about one object.
func (r renderResult) docsByName() map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, doc := range r.Docs {
		out[bf11DocName(doc)] = doc
	}
	return out
}

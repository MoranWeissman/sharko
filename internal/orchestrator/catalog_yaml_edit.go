package orchestrator

// catalog_yaml_edit.go — writing catalog.yaml without wrecking what a
// person put there by hand.
//
// Hand-editing catalog.yaml is a supported door (design doc §3), so people
// will put comments in it, group entries the way they like, and leave notes
// next to the ones they were unsure about. Until now every add re-marshalled
// the whole file from a parsed struct, which threw all of that away and
// re-sorted the entries — the reviewer of the approval pull request then saw
// a diff touching the entire file instead of the six lines that changed
// (review finding M1).
//
// So the write is SURGICAL: parse the original only to LOCATE the lines
// belonging to each addon being added or replaced, and splice the original
// text at exactly those lines. Every other byte — the header, comments,
// blank lines, key order, indentation style — passes through untouched.
// Same trick, and the same reasoning, as UpdateEnginePinVersion in
// internal/gitops.
//
// And it is VERIFIED before it is trusted: the spliced bytes are read back
// through the ordinary loader and compared, field for field, against the
// catalog the caller meant to write. Anything that does not match exactly —
// a document shape this code did not expect, an anchor, a flow mapping —
// falls back to the whole-file re-render, which is always correct even when
// it is ugly. A wrong-but-pretty catalog.yaml is the one outcome that must
// not happen; a reformatted one is only annoying, and the caller is told so
// it can say it in the pull request.

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/config"
)

// CatalogRewriteNote is the plain sentence a pull request carries when the
// surgical edit could not be used and the whole file was re-rendered.
const CatalogRewriteNote = "Sharko could not make a targeted edit to catalog.yaml here, so it rewrote the whole file: comments and the original ordering are not preserved in this pull request."

// writeCatalogFile renders the new catalog.yaml bytes.
//
// It returns the bytes and whether the whole file had to be re-rendered
// (reformatted == true), which is the caller's cue to warn in the pull
// request body.
func writeCatalogFile(
	original []byte,
	spec config.AddonCatalogSpec,
	entries map[string]config.AddonCatalogEntry,
	names []string,
) (body []byte, reformatted bool, err error) {
	full, fullErr := config.SaveAddonCatalog(spec)
	if fullErr != nil {
		return nil, false, fullErr
	}

	// A repo with no catalog.yaml yet has nothing to preserve — and this is
	// the one case that is a genuine CREATE, so it is the one case that
	// gets the plain-English header (v4 naming polish, item 3). Every other
	// return path below is a REWRITE of a file that already existed, and
	// headers ride creation only. The header rides ahead of every future
	// splice edit too, since spliceCatalogEntries only ever touches the
	// lines belonging to the addons it is adding or replacing.
	if len(bytes.TrimSpace(original)) == 0 {
		return append([]byte(catalogYAMLHeader), full...), false, nil
	}

	edited, editErr := spliceCatalogEntries(original, entries, names)
	if editErr != nil {
		return full, true, nil
	}
	// The trust check: the spliced text must load back to exactly the
	// catalog we meant to write, or it does not get used.
	readBack, loadErr := config.LoadAddonCatalog(edited)
	if loadErr != nil || !reflect.DeepEqual(readBack, spec) {
		return full, true, nil
	}
	return edited, false, nil
}

// spliceCatalogEntries replaces (or appends) the named addons' blocks in the
// original text and returns the result. An error means "this document is not
// a shape this code can edit safely" — always a fallback signal, never
// something to show a person.
func spliceCatalogEntries(original []byte, entries map[string]config.AddonCatalogEntry, names []string) ([]byte, error) {
	// Preserve the original line ending style by splitting on "\n" and
	// rejoining the same way; a "\r\n" file keeps its "\r" on every line
	// because it rides along inside the line text.
	lines := strings.Split(string(original), "\n")

	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return nil, fmt.Errorf("catalog.yaml does not parse: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("catalog.yaml is not a single YAML mapping")
	}
	root := doc.Content[0]
	if root.Style != 0 {
		return nil, fmt.Errorf("catalog.yaml root is not a block mapping")
	}

	addonsKey, addonsVal := mappingEntry(root, "addons")
	if addonsKey == nil {
		return nil, fmt.Errorf("catalog.yaml has no addons block")
	}

	// The addons block is empty (`addons:` with nothing under it, or
	// `addons: {}`). There is nothing to splice around, so rewrite that one
	// line and put every entry underneath it.
	if isEmptyAddonsValue(addonsVal) {
		return spliceIntoEmptyAddons(lines, addonsKey, entries, names)
	}
	if addonsVal.Kind != yaml.MappingNode || addonsVal.Style != 0 {
		return nil, fmt.Errorf("catalog.yaml addons block is not a block mapping")
	}

	entryIndent := indentOf(lineAt(lines, addonsVal.Content[0].Line-1))
	innerIndent := detectInnerIndent(lines, addonsVal, entryIndent)

	// Every edit is expressed as a line range plus its replacement, then
	// applied from the bottom of the file upwards so earlier line numbers
	// stay valid while later ones are being rewritten.
	type edit struct {
		start, end int // 0-based, end exclusive
		text       []string
	}
	var edits []edit

	// Where a brand-new addon goes: after the last entry's own content, so
	// it lands inside the addons block and above any trailing comment.
	appendAt := addonsBlockEnd(lines, addonsVal, entryIndent)

	for _, name := range names {
		rendered, err := renderCatalogEntry(name, entries[name], entryIndent, innerIndent)
		if err != nil {
			return nil, err
		}
		idx := keyIndexIn(addonsVal, name)
		if idx < 0 {
			edits = append(edits, edit{start: appendAt, end: appendAt, text: rendered})
			continue
		}
		start := addonsVal.Content[idx*2].Line - 1
		end := entryContentEnd(lines, addonsVal, idx, entryIndent)
		if start < 0 || end > len(lines) || start >= end {
			return nil, fmt.Errorf("catalog.yaml: could not locate the lines for addon %q", name)
		}
		edits = append(edits, edit{start: start, end: end, text: rendered})
	}

	// Bottom-up. Ties (several appends at the same point) keep their order.
	for i := 0; i < len(edits); i++ {
		for j := i + 1; j < len(edits); j++ {
			if edits[j].start > edits[i].start {
				edits[i], edits[j] = edits[j], edits[i]
			}
		}
	}
	for _, e := range edits {
		out := make([]string, 0, len(lines)+len(e.text))
		out = append(out, lines[:e.start]...)
		out = append(out, e.text...)
		out = append(out, lines[e.end:]...)
		lines = out
	}

	return []byte(strings.Join(lines, "\n")), nil
}

// spliceIntoEmptyAddons handles `addons:` (nothing under it) and
// `addons: {}` — rewrite that single line and put the entries below it.
func spliceIntoEmptyAddons(lines []string, addonsKey *yaml.Node, entries map[string]config.AddonCatalogEntry, names []string) ([]byte, error) {
	keyLine := addonsKey.Line - 1
	if keyLine < 0 || keyLine >= len(lines) {
		return nil, fmt.Errorf("catalog.yaml: addons line is out of range")
	}
	raw := lines[keyLine]
	colon := strings.Index(raw, ":")
	if colon < 0 {
		return nil, fmt.Errorf("catalog.yaml: the addons line is not a plain 'addons:' key")
	}
	rebuilt := raw[:colon+1]
	if hash := strings.Index(raw[colon+1:], "#"); hash >= 0 {
		rebuilt += " " + strings.TrimRight(raw[colon+1+hash:], " \t")
	}

	keyIndent := indentOf(raw)
	entryIndent := keyIndent + 2

	out := make([]string, 0, len(lines)+len(names)*6)
	out = append(out, lines[:keyLine]...)
	out = append(out, rebuilt)
	for _, name := range names {
		rendered, err := renderCatalogEntry(name, entries[name], entryIndent, 2)
		if err != nil {
			return nil, err
		}
		out = append(out, rendered...)
	}
	out = append(out, lines[keyLine+1:]...)
	return []byte(strings.Join(out, "\n")), nil
}

// renderCatalogEntry marshals one addon entry and re-indents it so it sits
// under the addons block at the file's own indentation.
func renderCatalogEntry(name string, entry config.AddonCatalogEntry, entryIndent, innerIndent int) ([]string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(innerIndent)
	if err := enc.Encode(map[string]config.AddonCatalogEntry{name: entry}); err != nil {
		return nil, fmt.Errorf("rendering the catalog entry for %q: %w", name, err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("rendering the catalog entry for %q: %w", name, err)
	}

	pad := strings.Repeat(" ", entryIndent)
	raw := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, pad+l)
	}
	return out, nil
}

// writeCatalogFileRemove renders catalog.yaml with name's entry deleted.
// Same surgical-splice-with-verified-fallback contract as writeCatalogFile:
// spliceRemoveCatalogEntry deletes only the lines belonging to name, the
// result is read back and compared field-for-field against spec (which the
// caller has already deleted the entry from), and anything that does not
// match exactly falls back to a whole-file re-render.
func writeCatalogFileRemove(original []byte, spec config.AddonCatalogSpec, name string) (body []byte, reformatted bool, err error) {
	full, fullErr := config.SaveAddonCatalog(spec)
	if fullErr != nil {
		return nil, false, fullErr
	}

	edited, editErr := spliceRemoveCatalogEntry(original, name)
	if editErr != nil {
		return full, true, nil
	}
	// The trust check, same as writeCatalogFile's: the spliced text must
	// load back to exactly the catalog we meant to write, or it does not
	// get used. This is also what catches the one case spliceRemoveCatalogEntry
	// cannot special-case for itself — deleting the last remaining entry
	// leaves an empty `addons:` mapping, which loads back with a nil map
	// rather than spec's empty-but-non-nil one; that mismatch is exactly
	// what routes it to the always-correct whole-file fallback below.
	readBack, loadErr := config.LoadAddonCatalog(edited)
	if loadErr != nil || !reflect.DeepEqual(readBack, spec) {
		return full, true, nil
	}
	return edited, false, nil
}

// spliceRemoveCatalogEntry deletes name's block from the original text and
// returns the result. Same fallback contract as spliceCatalogEntries: an
// error means "this document is not a shape this code can edit safely" —
// always a fallback signal, never something to show a person.
func spliceRemoveCatalogEntry(original []byte, name string) ([]byte, error) {
	lines := strings.Split(string(original), "\n")

	var doc yaml.Node
	if err := yaml.Unmarshal(original, &doc); err != nil {
		return nil, fmt.Errorf("catalog.yaml does not parse: %w", err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("catalog.yaml is not a single YAML mapping")
	}
	root := doc.Content[0]
	if root.Style != 0 {
		return nil, fmt.Errorf("catalog.yaml root is not a block mapping")
	}

	addonsKey, addonsVal := mappingEntry(root, "addons")
	if addonsKey == nil {
		return nil, fmt.Errorf("catalog.yaml has no addons block")
	}
	if isEmptyAddonsValue(addonsVal) {
		return nil, fmt.Errorf("catalog.yaml addons block has no entries to remove")
	}
	if addonsVal.Kind != yaml.MappingNode || addonsVal.Style != 0 {
		return nil, fmt.Errorf("catalog.yaml addons block is not a block mapping")
	}

	entryIndent := indentOf(lineAt(lines, addonsVal.Content[0].Line-1))

	idx := keyIndexIn(addonsVal, name)
	if idx < 0 {
		return nil, fmt.Errorf("catalog.yaml: addon %q not found", name)
	}
	start := addonsVal.Content[idx*2].Line - 1
	end := entryContentEnd(lines, addonsVal, idx, entryIndent)
	if start < 0 || end > len(lines) || start >= end {
		return nil, fmt.Errorf("catalog.yaml: could not locate the lines for addon %q", name)
	}

	out := make([]string, 0, len(lines))
	out = append(out, lines[:start]...)
	out = append(out, lines[end:]...)
	return []byte(strings.Join(out, "\n")), nil
}

// mappingEntry returns the key and value nodes for key in a block mapping.
func mappingEntry(n *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i], n.Content[i+1]
		}
	}
	return nil, nil
}

// keyIndexIn returns the PAIR index (not the Content index) of key in a
// mapping node, or -1.
func keyIndexIn(n *yaml.Node, key string) int {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return i / 2
		}
	}
	return -1
}

// isEmptyAddonsValue reports whether the addons value holds no entries —
// either a null (`addons:` with nothing under it) or an empty mapping.
func isEmptyAddonsValue(n *yaml.Node) bool {
	if n == nil {
		return true
	}
	if n.Kind == yaml.ScalarNode && (n.Tag == "!!null" || strings.TrimSpace(n.Value) == "") {
		return true
	}
	return n.Kind == yaml.MappingNode && len(n.Content) == 0
}

// entryContentEnd returns the exclusive 0-based end line of the idx-th
// addon's block: everything the entry owns, without swallowing the blank
// lines or comments that sit above whatever comes next (those belong to the
// next thing, and taking them would silently delete somebody's note).
func entryContentEnd(lines []string, addonsVal *yaml.Node, idx, entryIndent int) int {
	limit := 0
	if (idx+1)*2 < len(addonsVal.Content) {
		limit = addonsVal.Content[(idx+1)*2].Line - 1
	} else {
		limit = addonsBlockEnd(lines, addonsVal, entryIndent)
	}
	for limit > 0 && isBlankOrComment(lineAt(lines, limit-1)) {
		limit--
	}
	return limit
}

// addonsBlockEnd returns the exclusive 0-based line where the addons block
// stops — the first line at or left of the entry indentation that is not
// blank and not a comment.
func addonsBlockEnd(lines []string, addonsVal *yaml.Node, entryIndent int) int {
	last := addonsVal.Content[len(addonsVal.Content)-2].Line - 1
	i := last + 1
	for i < len(lines) {
		l := lineAt(lines, i)
		if strings.TrimSpace(l) == "" {
			i++
			continue
		}
		if indentOf(l) < entryIndent {
			break
		}
		if indentOf(l) == entryIndent && strings.HasPrefix(strings.TrimSpace(l), "#") {
			break
		}
		i++
	}
	for i > last+1 && isBlankOrComment(lineAt(lines, i-1)) {
		i--
	}
	return i
}

// detectInnerIndent reads the file's own nesting width off the first addon
// entry that has any fields, so a spliced entry matches the indentation
// around it. Falls back to yaml.v3's default of 4.
func detectInnerIndent(lines []string, addonsVal *yaml.Node, entryIndent int) int {
	for i := 0; i+1 < len(addonsVal.Content); i += 2 {
		val := addonsVal.Content[i+1]
		if val.Kind != yaml.MappingNode || len(val.Content) == 0 {
			continue
		}
		child := indentOf(lineAt(lines, val.Content[0].Line-1))
		if child > entryIndent {
			return child - entryIndent
		}
	}
	return 4
}

func lineAt(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
}

func indentOf(line string) int {
	n := 0
	for _, r := range line {
		if r != ' ' {
			break
		}
		n++
	}
	return n
}

func isBlankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// parseCatalogBody reads catalog.yaml bytes, treating absent/empty content
// as an empty catalog. Callers that care about the difference between
// "missing" and "present but blank" check that BEFORE calling this — see
// ErrCatalogFileEmpty.
func parseCatalogBody(body []byte) (config.AddonCatalogSpec, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return config.AddonCatalogSpec{}, nil
	}
	return config.LoadAddonCatalog(body)
}

// Package orchestrator — keep a seeded values file honest across an upgrade.
//
// WHY THIS FILE EXISTS (pre-release walk 4, "an upgrade moves the chart but
// the pod keeps the old image, and Sharko says it is upgraded").
//
// When an addon is added to the catalog, Sharko writes values/global/<addon>.yaml
// as a copy of the chart's own values.yaml (smart_values.go). That copy holds
// EVERY default of the chart version it was made from — including the ones a
// chart changes with every release, like podinfo's `image.tag: 6.7.1`. Helm
// reads a values file as "the user wants this", so after an upgrade to chart
// 6.8.0 the file still says `image.tag: 6.7.1`, the new chart is installed
// with the old image, and ArgoCD reports Synced and Healthy. Nobody chose
// that. Sharko's own seeded file did.
//
// THE RULE. A setting in the file that is exactly the OLD chart's default was
// never changed by a person — it is still Sharko's copy. When the chart's
// default for that setting is different in the NEW version, the copy follows
// it. A setting that is anything other than the old default was changed by a
// person and is never touched. That is the whole rule:
//
//	file value == old default, new default differs  ->  set to the new default
//	file value != old default                       ->  keep (a person chose it)
//
// HOW IT EDITS. One line at a time, in place. The file is NOT re-rendered:
// comments, blank lines, ordering and indentation stay exactly as they are,
// so the pull request diff is one small line per moved setting. Only plain
// one-line values (text, numbers, true/false, null) are moved. A list or a
// multi-line text that still equals the old default cannot be edited safely
// one line at a time, so it is left alone and REPORTED (Kept) — the caller
// says so in plain words rather than staying silent.
//
// IT CHECKS ITS OWN WORK. After the edit the result is parsed again and
// compared with "the original, with exactly these settings changed". If the
// two differ in any way the edit is thrown away and the original is returned
// with everything reported as kept. A wrong line edit can never reach Git.
package orchestrator

import (
	"reflect"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MovedDefault is one setting that followed the chart's default.
type MovedDefault struct {
	Path string
	Old  string
	New  string
}

// KeptDefault is one setting that still equals the OLD chart's default while
// the new chart's default is different — and that this pass did not change.
type KeptDefault struct {
	Path string
	Old  string
	New  string
}

// FollowDefaultsResult is what followChartDefaults hands back.
type FollowDefaultsResult struct {
	// File is the edited file. Equal to the input when nothing moved.
	File []byte
	// Moved lists the settings that were changed, sorted by path.
	Moved []MovedDefault
	// Kept lists the settings that still hold the old chart's default and
	// were not changed, sorted by path.
	Kept []KeptDefault
}

// staleDefault is one place where the file still holds the old default.
type staleDefault struct {
	path    []string
	fileVal *yaml.Node
	oldVal  *yaml.Node
	newVal  *yaml.Node
}

// findStaleDefaults walks the file and returns every setting whose value is
// exactly the old chart default while the new chart default is different.
func findStaleDefaults(file, oldDefaults, newDefaults []byte) []staleDefault {
	fileRoot := yamlDocRoot(file)
	oldRoot := yamlDocRoot(oldDefaults)
	newRoot := yamlDocRoot(newDefaults)
	if fileRoot == nil || oldRoot == nil || newRoot == nil {
		return nil
	}
	var out []staleDefault
	walkStaleDefaults(nil, fileRoot, oldRoot, newRoot, &out)
	return out
}

func walkStaleDefaults(path []string, fileNode, oldNode, newNode *yaml.Node, out *[]staleDefault) {
	if fileNode == nil || oldNode == nil || newNode == nil {
		return
	}
	// An alias or an anchored value is somebody's deliberate YAML; leave it.
	if fileNode.Kind == yaml.AliasNode || fileNode.Anchor != "" {
		return
	}
	if fileNode.Kind == yaml.MappingNode {
		if oldNode.Kind != yaml.MappingNode || newNode.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(fileNode.Content); i += 2 {
			key := fileNode.Content[i].Value
			oldChild := mappingChild(oldNode, key)
			newChild := mappingChild(newNode, key)
			if oldChild == nil || newChild == nil {
				continue
			}
			next := append(append([]string{}, path...), key)
			walkStaleDefaults(next, fileNode.Content[i+1], oldChild, newChild, out)
		}
		return
	}
	// A leaf (or a list). It is stale when it equals the old default and the
	// new default is something else.
	if !yamlNodesEqual(fileNode, oldNode) || yamlNodesEqual(oldNode, newNode) {
		return
	}
	*out = append(*out, staleDefault{path: path, fileVal: fileNode, oldVal: oldNode, newVal: newNode})
}

// followChartDefaults applies THE RULE at the top of this file.
func followChartDefaults(file, oldDefaults, newDefaults []byte) FollowDefaultsResult {
	res := FollowDefaultsResult{File: file}
	stale := findStaleDefaults(file, oldDefaults, newDefaults)
	if len(stale) == 0 {
		return res
	}

	lines := strings.Split(string(file), "\n")
	var moved []staleDefault
	var kept []staleDefault
	usedLines := map[int]bool{}
	for _, s := range stale {
		newText, ok := replacementText(s)
		lineIdx := s.fileVal.Line - 1
		col := s.fileVal.Column - 1
		if !ok || lineIdx < 0 || lineIdx >= len(lines) || usedLines[lineIdx] {
			kept = append(kept, s)
			continue
		}
		line := lines[lineIdx]
		oldText, found := sourceTextAt(line, col, s.fileVal)
		if !found {
			kept = append(kept, s)
			continue
		}
		lines[lineIdx] = line[:col] + newText + line[col+len(oldText):]
		usedLines[lineIdx] = true
		moved = append(moved, s)
	}

	if len(moved) > 0 {
		edited := []byte(strings.Join(lines, "\n"))
		if followDefaultsEditIsExact(file, edited, moved) {
			res.File = edited
		} else {
			// The line edit did not produce exactly what it should have.
			// Nothing is changed; everything is reported instead.
			kept = append(kept, moved...)
			moved = nil
		}
	}

	for _, s := range moved {
		res.Moved = append(res.Moved, MovedDefault{Path: strings.Join(s.path, "."), Old: displayYAMLValue(s.oldVal), New: displayYAMLValue(s.newVal)})
	}
	for _, s := range kept {
		res.Kept = append(res.Kept, KeptDefault{Path: strings.Join(s.path, "."), Old: displayYAMLValue(s.oldVal), New: displayYAMLValue(s.newVal)})
	}
	sort.Slice(res.Moved, func(i, j int) bool { return res.Moved[i].Path < res.Moved[j].Path })
	sort.Slice(res.Kept, func(i, j int) bool { return res.Kept[i].Path < res.Kept[j].Path })
	return res
}

// PreviewFollowDefaults reports what followChartDefaults would do to a
// generated values file, without changing anything: the settings that would
// move to the new chart's defaults, and the ones that would stay on the old
// chart's value. The upgrade Analyze screen uses it so what it says matches
// what the upgrade pull request really does.
func PreviewFollowDefaults(file, oldDefaults, newDefaults []byte) (moved []MovedDefault, kept []KeptDefault) {
	res := followChartDefaults(file, oldDefaults, newDefaults)
	return res.Moved, res.Kept
}

// staleDefaultsReport lists the settings that still hold the old default,
// without changing anything. Used where the file cannot be edited (a
// one-cluster upgrade: the global file is shared by every cluster).
func staleDefaultsReport(file, oldDefaults, newDefaults []byte) []KeptDefault {
	stale := findStaleDefaults(file, oldDefaults, newDefaults)
	out := make([]KeptDefault, 0, len(stale))
	for _, s := range stale {
		out = append(out, KeptDefault{Path: strings.Join(s.path, "."), Old: displayYAMLValue(s.oldVal), New: displayYAMLValue(s.newVal)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// replacementText is the text written in place of the old value. Only a
// one-line plain value is ever written.
func replacementText(s staleDefault) (string, bool) {
	if s.fileVal.Kind != yaml.ScalarNode || s.newVal.Kind != yaml.ScalarNode {
		return "", false
	}
	if s.fileVal.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
		return "", false
	}
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: s.newVal.Tag, Value: s.newVal.Value}
	// Keep the quoting the chart author used for the new value, so a text
	// that has to stay text ("1.0", "true", "#34577c") stays quoted.
	if s.newVal.Style&yaml.DoubleQuotedStyle != 0 {
		n.Style = yaml.DoubleQuotedStyle
	} else if s.newVal.Style&yaml.SingleQuotedStyle != 0 {
		n.Style = yaml.SingleQuotedStyle
	}
	raw, err := yaml.Marshal(n)
	if err != nil {
		return "", false
	}
	text := strings.TrimRight(string(raw), "\n")
	if text == "" || strings.Contains(text, "\n") {
		return "", false
	}
	return text, true
}

// sourceTextAt finds the exact text of the value as written on its line.
func sourceTextAt(line string, col int, n *yaml.Node) (string, bool) {
	if col < 0 || col > len(line) {
		return "", false
	}
	rest := line[col:]
	var candidates []string
	switch {
	case n.Style&yaml.DoubleQuotedStyle != 0:
		candidates = []string{`"` + n.Value + `"`}
	case n.Style&yaml.SingleQuotedStyle != 0:
		candidates = []string{`'` + n.Value + `'`}
	default:
		// A plain value: yaml.v3 keeps the text exactly as it was written.
		candidates = []string{n.Value}
	}
	for _, c := range candidates {
		if c == "" || !strings.HasPrefix(rest, c) {
			continue
		}
		tail := strings.TrimRight(rest[len(c):], " \t\r")
		if tail == "" || strings.HasPrefix(strings.TrimLeft(rest[len(c):], " \t"), "#") {
			return c, true
		}
	}
	return "", false
}

// followDefaultsEditIsExact proves the edited file is the original with
// exactly the moved settings changed to the new defaults, and nothing else.
func followDefaultsEditIsExact(original, edited []byte, moved []staleDefault) bool {
	var want, got interface{}
	if err := yaml.Unmarshal(original, &want); err != nil {
		return false
	}
	if err := yaml.Unmarshal(edited, &got); err != nil {
		return false
	}
	for _, s := range moved {
		var v interface{}
		if err := s.newVal.Decode(&v); err != nil {
			return false
		}
		if !setAtPath(want, s.path, v) {
			return false
		}
	}
	return reflect.DeepEqual(want, got)
}

func setAtPath(root interface{}, path []string, v interface{}) bool {
	cur, ok := root.(map[string]interface{})
	if !ok || len(path) == 0 {
		return false
	}
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]interface{})
		if !ok {
			return false
		}
		cur = next
	}
	last := path[len(path)-1]
	if _, exists := cur[last]; !exists {
		return false
	}
	cur[last] = v
	return true
}

func yamlDocRoot(data []byte) *yaml.Node {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	return root
}

func mappingChild(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// yamlNodesEqual compares two values by what they MEAN, not how they are
// written: `"6.7.1"` and `6.7.1` are the same text, `1` and `1.0` are not
// the same as each other unless YAML reads them the same.
func yamlNodesEqual(a, b *yaml.Node) bool {
	var av, bv interface{}
	if err := a.Decode(&av); err != nil {
		return false
	}
	if err := b.Decode(&bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// displayYAMLValue renders a value for a sentence a person reads: the text
// itself for a plain value, compact YAML for a list or a block.
func displayYAMLValue(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	if n.Kind == yaml.ScalarNode {
		if n.Tag == "!!null" {
			return "null"
		}
		if n.Value == "" {
			return `""`
		}
		return n.Value
	}
	var v interface{}
	if err := n.Decode(&v); err != nil {
		return ""
	}
	raw, err := yaml.Marshal(v)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(raw))
	if len(text) > 80 {
		text = text[:77] + "..."
	}
	return strings.ReplaceAll(text, "\n", "; ")
}

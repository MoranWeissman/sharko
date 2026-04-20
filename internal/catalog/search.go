package catalog

import (
	"sort"
	"strconv"
	"strings"
)

// Query captures every filter the API layer can apply to a List request.
// All fields are optional — the zero value is "no filters; return everything".
//
// Semantics:
//   - Q: case-insensitive substring match on name OR description OR any
//     maintainer. Empty means "no text filter."
//   - Category: exact match on entry.Category when non-empty.
//   - CuratedBy: subset match — an entry passes when it carries every tag
//     listed here (AND). Empty means "no curated_by filter."
//   - License: exact match (SPDX identifier) when non-empty.
//   - MinScore: when > 0, entries with unknown score are excluded; entries
//     with a known score are kept when score >= MinScore. MinScore == 0
//     (the zero value) disables the filter and includes unknown scores.
//   - MinK8sVersion: when non-empty, entries whose min_kubernetes_version
//     is <= MinK8sVersion are included (caller's cluster is newer-or-equal).
//     Entries with an empty min_kubernetes_version are always included.
//   - IncludeDeprecated: false by default; deprecated entries are hidden.
type Query struct {
	Q                 string
	Category          string
	CuratedBy         []string
	License           string
	MinScore          float64
	MinK8sVersion     string
	IncludeDeprecated bool
}

// List returns the subset of entries matching q, sorted by name ascending.
// The returned slice is a fresh copy — safe to mutate.
func (c *Catalog) List(q Query) []CatalogEntry {
	if c == nil {
		return nil
	}
	needle := strings.ToLower(strings.TrimSpace(q.Q))
	out := make([]CatalogEntry, 0, len(c.entries))
	for _, e := range c.entries {
		if !q.IncludeDeprecated && e.Deprecated {
			continue
		}
		if q.Category != "" && e.Category != q.Category {
			continue
		}
		if !containsAll(e.CuratedBy, q.CuratedBy) {
			continue
		}
		if q.License != "" && !strings.EqualFold(e.License, q.License) {
			continue
		}
		if q.MinScore > 0 {
			if !e.SecurityScore.Known || e.SecurityScore.Value < q.MinScore {
				continue
			}
		}
		if q.MinK8sVersion != "" && e.MinKubernetesVersion != "" {
			if compareK8sVersion(e.MinKubernetesVersion, q.MinK8sVersion) > 0 {
				// Entry requires a NEWER cluster than the caller's.
				continue
			}
		}
		if needle != "" && !matchesText(&e, needle) {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// matchesText returns true when needle appears in the entry's name,
// description, or any maintainer (case-insensitive substring).
func matchesText(e *CatalogEntry, needle string) bool {
	if strings.Contains(strings.ToLower(e.Name), needle) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Description), needle) {
		return true
	}
	for _, m := range e.Maintainers {
		if strings.Contains(strings.ToLower(m), needle) {
			return true
		}
	}
	return false
}

// containsAll returns true when `have` includes every tag in `want`. Case
// insensitive. Empty `want` matches everything.
func containsAll(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(have))
	for _, h := range have {
		set[strings.ToLower(h)] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[strings.ToLower(w)]; !ok {
			return false
		}
	}
	return true
}

// compareK8sVersion compares two Kubernetes version strings ("MAJOR.MINOR" or
// "MAJOR.MINOR.PATCH"). Returns -1 if a < b, 0 if equal, 1 if a > b. Invalid
// inputs (non-numeric segments) compare as equal so a malformed field in the
// catalog never silently drops an entry.
func compareK8sVersion(a, b string) int {
	pa := splitVersion(a)
	pb := splitVersion(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(pa) {
			av = pa[i]
		}
		if i < len(pb) {
			bv = pb[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// splitVersion breaks "1.23.4" into [1, 23, 4]. Non-numeric segments map to
// 0 so comparison degrades gracefully rather than failing.
func splitVersion(v string) []int {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			out[i] = n
		}
	}
	return out
}

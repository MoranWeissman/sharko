// Package orchestrator — enumerating the v3 scaffold the migration removes
// (v4 Wave 2 Story 5.2).
//
// The migration PR has to delete "the template forest the v3 bootstrap
// wrote". Hard-coding that list would rot the moment somebody edits
// templates/bootstrap/, so it is DERIVED: walk the very same embedded
// template tree the v3 InitRepo walked, apply the very same repo-path
// mapping it applied, and you get exactly the set of paths a v3 bootstrap
// PR created. Subtract the data files (which are converted, not deleted)
// and what remains is the scaffold.
package orchestrator

import (
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/templates"
)

// v3TemplateRoot is the directory inside templates.TemplateFS that the v3
// bootstrap walked.
const v3TemplateRoot = "bootstrap"

// v3TemplateRepoPath maps one path inside the embedded template tree to the
// repo path the v3 bootstrap committed it at.
//
// This is a VERBATIM copy of the rule the pre-v4 InitRepo used (see the
// history of internal/orchestrator/init.go): Chart.yaml and templates/
// keep their "bootstrap/" prefix because they form a Helm chart; every
// other file was committed at the repo root with the prefix stripped.
// Copied rather than shared because the original writer is gone — this is
// now the only place that knows the mapping, and it must keep answering
// for repos that were bootstrapped by that writer.
func v3TemplateRepoPath(templatePath string) string {
	if strings.HasPrefix(templatePath, v3TemplateRoot+"/Chart.yaml") ||
		strings.HasPrefix(templatePath, v3TemplateRoot+"/templates/") {
		return templatePath
	}
	return strings.TrimPrefix(templatePath, v3TemplateRoot+"/")
}

// v3ScaffoldRepoPaths returns every repo path a v3 bootstrap PR wrote,
// sorted. Includes the data files; callers subtract the ones they convert.
//
// The walk cannot fail in practice (the tree is compiled into the binary),
// so a walk error yields the partial list rather than an error return —
// there is no caller that could do anything useful with the failure, and a
// short list only means the migration deletes fewer files, never the wrong
// ones.
func v3ScaffoldRepoPaths() []string {
	var out []string
	_ = fs.WalkDir(templates.TemplateFS, v3TemplateRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a partial list is the safe degradation
		}
		out = append(out, v3TemplateRepoPath(p))
		return nil
	})
	sort.Strings(out)
	return out
}

// v3DataPathSet describes which repo paths hold USER DATA (converted by the
// migration) rather than scaffold (deleted by it). Built from the
// connection's configured v3 layout so a repo with non-default paths is
// classified against its own layout, not the template's defaults.
type v3DataPathSet struct {
	managedClusters string
	catalog         string
	clusterValues   string
	globalValues    string
}

// newV3DataPathSet resolves the v3 data paths, defaulting each one to the
// value the bootstrap template shipped when the connection leaves it unset
// (the same defaults RegisterCluster falls back to).
func newV3DataPathSet(paths RepoPathsConfig) v3DataPathSet {
	s := v3DataPathSet{
		managedClusters: paths.ManagedClusters,
		catalog:         paths.Catalog,
		clusterValues:   strings.TrimSuffix(paths.ClusterValues, "/"),
		globalValues:    strings.TrimSuffix(paths.GlobalValues, "/"),
	}
	if s.managedClusters == "" {
		s.managedClusters = V3SecondaryMarkerPath
	}
	if s.catalog == "" {
		s.catalog = "configuration/addons-catalog.yaml"
	}
	if s.clusterValues == "" {
		s.clusterValues = "configuration/addons-clusters-values"
	}
	if s.globalValues == "" {
		s.globalValues = "configuration/addons-global-values"
	}
	return s
}

// isData reports whether a repo path holds user data the migration
// converts. Everything else a v3 bootstrap wrote is scaffold.
func (s v3DataPathSet) isData(repoPath string) bool {
	if repoPath == s.managedClusters || repoPath == s.catalog {
		return true
	}
	return underDir(repoPath, s.clusterValues) || underDir(repoPath, s.globalValues)
}

// underDir reports whether repoPath sits inside dir. Pure string work on
// already-clean repo-relative paths — no filepath.Rel, which answers
// against the host OS's separator and would say "yes" for a sibling
// directory whose name merely starts with dir.
func underDir(repoPath, dir string) bool {
	if dir == "" {
		return false
	}
	return strings.HasPrefix(path.Clean(repoPath), path.Clean(dir)+"/")
}

// v3ScaffoldFilesToRemove returns the scaffold paths the migration deletes:
// every path a v3 bootstrap wrote, minus the data files (converted
// separately), minus keepPaths — the paths the migration WRITES, so a file
// that exists in both trees (README.md) is rewritten rather than deleted.
func v3ScaffoldFilesToRemove(paths RepoPathsConfig, keepPaths map[string]bool) []string {
	data := newV3DataPathSet(paths)
	var out []string
	for _, p := range v3ScaffoldRepoPaths() {
		if data.isData(p) || keepPaths[p] {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

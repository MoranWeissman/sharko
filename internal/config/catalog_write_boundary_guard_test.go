// catalog_write_boundary_guard_test.go — the guard that keeps the promise
// structural rather than a habit.
//
// The promise is: a credential-bearing repository address cannot be written
// into a catalog file. That holds only while every catalog write really does
// go through MarshalAddonCatalog or SaveAddonCatalog, which are the two
// functions that check. A new caller that marshals a catalog entry itself
// would walk straight around the check, and nothing would fail.
//
// So this guard walks the tree — it does not read a hand-written list of files
// — and reports what it finds against an expected LIST. A list fails two ways:
// on something new that nobody looked at, and on an entry that has gone stale.
// A count would only fail one way, and a floor would fail neither.
package config

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// repoRoot walks up from this package to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving the current directory: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find go.mod above this package — the guard cannot walk the tree")
	return ""
}

// walkGoFiles calls fn for every non-test .go file in the repository.
func walkGoFiles(t *testing.T, fn func(rel string, body string)) int {
	t.Helper()
	root := repoRoot(t)
	seen := 0
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "ui", "_dist", ".worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		seen++
		fn(filepath.ToSlash(rel), string(body))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
	if seen == 0 {
		t.Fatal("the walk found no Go files at all — this guard would pass without checking anything")
	}
	return seen
}

// TestEveryCatalogWriteGoesThroughTheTwoCheckedWriters lists every file that
// calls one of the two canonical writers.
//
// Adding a caller is fine — it just has to be looked at and added here, which
// is the whole point: somebody reads the new call and confirms it is a real
// catalog write rather than a way around the check.
func TestEveryCatalogWriteGoesThroughTheTwoCheckedWriters(t *testing.T) {
	want := []string{
		"internal/config/addon_catalog.go",        // SaveAddonCatalog itself
		"internal/config/parser.go",               // MarshalAddonCatalog itself
		"internal/demo/generator_files.go",        // demo fixtures, both shapes
		"internal/demo/v4_fixtures.go",            // demo fixtures, v4
		"internal/gitops/yaml_mutator_catalog.go", // the v3 mutators
		"internal/orchestrator/addon_configure.go",
		"internal/orchestrator/catalog_yaml_edit.go",
		"internal/orchestrator/migration_v3v4.go",
	}

	call := regexp.MustCompile(`(?:config\.)?(?:MarshalAddonCatalog|SaveAddonCatalog)\(`)
	var got []string
	walkGoFiles(t, func(rel, body string) {
		if call.MatchString(body) {
			got = append(got, rel)
		}
	})
	sort.Strings(got)
	sort.Strings(want)

	if len(got) == 0 {
		t.Fatal("the walk found nobody writing a catalog file at all — the pattern is wrong and this guard is asleep")
	}
	compareLists(t, "files that write a catalog file", want, got,
		"A new one means a new catalog writer. Read it: does it go through MarshalAddonCatalog or SaveAddonCatalog? If yes, add it here. If it marshals the entry itself, it has walked around the check that keeps sign-in details out of Git — fix that instead of adding it here.")
}

// TestNobodyMarshalsACatalogEntryThemselves is the other half: a file that
// hand-marshals the catalog types would never reach either writer.
func TestNobodyMarshalsACatalogEntryThemselves(t *testing.T) {
	// The walk is file-level on purpose: it flags a file that both marshals
	// YAML and mentions a catalog type, without trying to prove the two
	// lines are connected. Coarse is the right setting for a guard — it errs
	// toward asking a person to look, and each entry below is a person
	// having looked.
	//
	// Every one of these has been read, and none of them writes catalog
	// bytes to an operator's Git repository:
	//
	//   internal/catalog/loader.go — canonicalBytes, which serialises a
	//     MARKETPLACE entry to reproduce the exact bytes a cosign signature
	//     was made over. The result is compared, never written.
	//   internal/demo/generator_files.go — its yaml.Marshal renders the
	//     generated managed-clusters.yaml, a different file with a different
	//     type. Its catalog writes go through MarshalAddonCatalog and
	//     SaveAddonCatalog, which the other guard above confirms.
	//   cmd/catalog-sign/main.go — a release-time build tool that stamps
	//     signature URLs into the Marketplace list Sharko itself ships. It
	//     writes into a build output folder, never into a user's repo.
	want := []string{
		"cmd/catalog-sign/main.go",
		"internal/catalog/loader.go",
		"internal/demo/generator_files.go",
	}

	// Any yaml.Marshal in the same file as one of the catalog entry type
	// names, outside internal/config where the two checked writers live.
	marshal := regexp.MustCompile(`yaml\.Marshal\(`)
	entryType := regexp.MustCompile(`AddonCatalogSpec|AddonCatalogEntry|CatalogEntry\b`)

	var got []string
	walkGoFiles(t, func(rel, body string) {
		if strings.HasPrefix(rel, "internal/config/") {
			return
		}
		if marshal.MatchString(body) && entryType.MatchString(body) {
			got = append(got, rel)
		}
	})
	sort.Strings(got)
	sort.Strings(want)

	compareLists(t, "files that marshal a catalog entry type by hand", want, got,
		"A new one is a way around MarshalAddonCatalog and SaveAddonCatalog. Route it through them, or — if the bytes genuinely never reach Git, the way internal/catalog/loader.go's canonicalBytes never does — say so here in a comment and add it.")
}

// TestTheRuleIsAskedForNotCopied lists every file that asks the shared rule.
// Nobody outside internal/credsafe may hold a rule of their own about what
// characters make an address unsafe — one rule written twice is a rule that
// will disagree with itself, which is exactly how a carrier slipped past the
// log sink before.
func TestTheRuleIsAskedForNotCopied(t *testing.T) {
	want := []string{
		"cmd/sharko/addon.go",
		"internal/api/addons_write.go",
		"internal/api/catalog_validate.go",
		"internal/catalog/catalog_view.go",
		"internal/config/catalog_repo_url.go",
		"internal/credsafe/repourl_supported.go",
		"internal/helm/fetcher.go",
		"internal/orchestrator/catalog_repo_url_door.go",
	}

	asks := regexp.MustCompile(`ValidateSupportedRepoURL(At)?\(`)
	var got []string
	walkGoFiles(t, func(rel, body string) {
		if asks.MatchString(body) {
			got = append(got, rel)
		}
	})
	sort.Strings(got)
	sort.Strings(want)

	if len(got) == 0 {
		t.Fatal("nothing in the tree asks the shared rule — the pattern is wrong and this guard is asleep")
	}
	compareLists(t, "files that ask the shared repository-address rule", want, got,
		"A new one is fine and expected as doors are added — add it here. What is NOT fine is a file that decides for itself: if you see a new place testing for \"@\", \"?\" or \"#\" in an address, that is a second copy of the rule and it must call credsafe instead.")
}

// compareLists reports what is new and what has gone stale, separately, so a
// failure says which of the two happened.
func compareLists(t *testing.T, what string, want, got []string, advice string) {
	t.Helper()

	inWant := make(map[string]bool, len(want))
	for _, w := range want {
		inWant[w] = true
	}
	inGot := make(map[string]bool, len(got))
	for _, g := range got {
		inGot[g] = true
	}

	var added, stale []string
	for _, g := range got {
		if !inWant[g] {
			added = append(added, g)
		}
	}
	for _, w := range want {
		if !inGot[w] {
			stale = append(stale, w)
		}
	}

	if len(added) > 0 {
		t.Errorf("new %s that this guard has never been shown:\n  %s\n\n%s", what, strings.Join(added, "\n  "), advice)
	}
	if len(stale) > 0 {
		t.Errorf("this guard expects %s that no longer exist:\n  %s\n\nEither the file moved or the call was removed. A stale entry means the guard is checking something that is not there any more, so remove it — do not leave it.", what, strings.Join(stale, "\n  "))
	}
}

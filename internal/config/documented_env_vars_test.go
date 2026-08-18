package config

// documented_env_vars_test.go — the guard against documenting configuration
// the product does not implement.
//
// # Why this exists
//
// Ruling (c), 2026-08-19. `SHARKO_AUDIT_BUFFER_SIZE` was documented in two
// places, cross-referenced from a third, and read by nothing: the audit ring
// is built as `audit.NewLog(1000)` in internal/api/router.go, a hardcoded
// literal. Tracing that one variable turned up FIFTY more documented names
// with no read site — most of them one word away from a name the code really
// does read (`SHARKO_GITOPS_BASE_BRANCH` documented,
// `SHARKO_CONN_GITOPS_BASE_BRANCH` read), and several of them inside operator
// runbooks that told an operator to inspect an environment name the
// deployment never sets.
//
// The worst was `SHARKO_TRUSTED_PROXIES`, on the SECURITY page, with a
// copy-pasteable YAML env block, read by no Go file at all. An operator
// following that page believed they had configured proxy trust for the write
// rate limiter and had not.
//
// No test anywhere guarded this class of bug. This is that test.
//
// # How it decides
//
// DOCUMENTED = every `SHARKO_*` token under docs/ and charts/, plus README.md.
// READ       = every `SHARKO_*` token that is either a double-quoted string
//              literal in any .go file, or appears anywhere under scripts/.
//
// The Go side must accept BOTH read shapes. A naive regex over
// `os.Getenv("…")` would miss the twenty-two `SHARKO_CONN_*` names declared
// as named constants in connection_gitnative.go and then read through those
// constants — and the test would be quietly useless for exactly the family
// that carries most of the real defects. Matching any quoted literal catches
// the constant declaration and the direct call with one rule.
//
// scripts/ counts as a read site because a name a shipped script reads IS
// implemented — just not by the server binary. That is a deliberately
// generous rule and it has one known hole: a doc could claim `SHARKO_FOO` is
// a server setting while some unrelated script uses `SHARKO_FOO` as its own
// shell variable, and this test would not notice. Narrow enough to accept;
// written down so nobody has to rediscover it.
//
// # What it does NOT scan
//
// docs/design/ — dated design records and implementation plans. They describe
// what was planned on a date, not what an operator can configure today, and
// they are not published to the docs site (mkdocs docs_dir is docs/site).
// Making every historical plan pay this tax would push people to stop writing
// them. This is the one path exemption and it is deliberately a single
// directory; a reviewer should push back on any attempt to add another.
//
// docs/swagger/ — generated output, regenerated from Go annotations.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// envTokenRe matches a SHARKO_ name that is not the tail of a longer
// underscore word. Without the leading guard, `E2E_SHARKO_SERVER` reads as a
// mention of `SHARKO_SERVER` and the test invents a defect that is not there.
var envTokenRe = regexp.MustCompile(`(^|[^A-Z0-9_])(SHARKO_[A-Z0-9_]*[A-Z0-9])`)

// goLiteralEnvRe matches a SHARKO_ name written as a Go string literal. That
// covers `os.Getenv("SHARKO_X")` and `const envX = "SHARKO_X"` alike.
var goLiteralEnvRe = regexp.MustCompile(`"(SHARKO_[A-Z0-9_]*[A-Z0-9])"`)

// docRoots are the trees and files whose SHARKO_* mentions are treated as
// claims. README.md is in the list because docs/site/operator/configuration.md
// sends the reader there for "the full list of supported env vars" — a page
// that is the named destination for configuration questions is documentation
// whatever its filename.
var docRoots = []string{"docs", "charts", "README.md"}

// docPathExempt are path prefixes inside docRoots that are not claims about
// today's product. Keep this list at exactly these two entries.
var docPathExempt = []string{
	filepath.Join("docs", "design"),  // dated design records and plans, not operator instructions, not published
	filepath.Join("docs", "swagger"), // generated from Go annotations
}

// allowedKind says why a documented-but-unread name is allowed to stay.
type allowedKind int

const (
	// shellLocal — the docs define this variable themselves, in the shell
	// snippet the reader is about to run. It was never a Sharko setting, so
	// there is nothing to read and nothing to fix. Allowed in any file.
	shellLocal allowedKind = iota

	// declaredAbsent — the surrounding prose says, on purpose, that Sharko
	// does NOT implement this. Removing the name would remove the warning.
	// Scoped to named files so the same name cannot quietly come back as a
	// live claim on a different page.
	declaredAbsent

	// testHarnessOnly — the variable IS read, but only from test code, so
	// the developer guide documents it honestly and no production file
	// mentions it. Every entry must name the test file that reads it, and
	// the test verifies that file really does — so the entry cannot rot
	// into a lie if the harness is deleted or renamed.
	testHarnessOnly
)

type allowedVar struct {
	kind    allowedKind
	why     string   // one line, mandatory — an allowlist without reasons is how this rots
	files   []string // declaredAbsent only: the exact files the name may appear in
	readers []string // testHarnessOnly only: the test files that really read it
}

// allowedDocumentedEnvVars is the complete list of SHARKO_* names that may
// appear in docs/ or charts/ without a read site.
//
// Adding an entry here to make the test green is the same dishonesty this
// test exists to remove. The only honest reasons are the three kinds above.
var allowedDocumentedEnvVars = map[string]allowedVar{
	// --- shell-local: the runbook assigns it, Sharko never reads it ---
	"SHARKO_POD":         {kind: shellLocal, why: "runbook shell var: SHARKO_POD=$(kubectl get pod ...) before the next command uses it"},
	"SHARKO_NS":          {kind: shellLocal, why: "runbook shell var for the namespace the operator types once and reuses"},
	"SHARKO_URL":         {kind: shellLocal, why: "runbook/smoke shell var for the Sharko base URL"},
	"SHARKO_TOKEN":       {kind: shellLocal, why: "runbook shell var holding the caller's own API token for curl"},
	"SHARKO_ADMIN_TOKEN": {kind: shellLocal, why: "runbook shell var holding an admin API token for curl"},
	"SHARKO_PREFIX":      {kind: shellLocal, why: "runbook shell var holding the value read out of SHARKO_CONN_PROVIDER_PREFIX"},
	"SHARKO_SECRETS_NS":  {kind: shellLocal, why: "runbook shell var with a ${VAR:-default} fallback for the secrets namespace"},
	"SHARKO_VERSION":     {kind: shellLocal, why: "runbook shell var holding the image tag parsed off the deployment"},
	"SHARKO_POLICY":      {kind: shellLocal, why: "shell var inside the argocd-rbac-patch Helm hook script, holding policy.csv lines"},

	// --- testHarnessOnly: read only from test code, documented for contributors ---
	//
	// These five are the reason the read scan cannot simply ignore every
	// _test.go file and stop there. They are genuinely implemented — a
	// contributor exports them to point the live Gitea suite at a real
	// server — but the only reader is a build-tagged test. Naming the
	// reader here is what keeps that claim checkable.
	"SHARKO_E2E_GITEA_URL": {
		kind:    testHarnessOnly,
		why:     "live Gitea e2e suite: the server URL a contributor exports before running it",
		readers: []string{filepath.Join("tests", "e2e", "lifecycle", "gitea_live_test.go")},
	},
	"SHARKO_E2E_GITEA_TOKEN": {
		kind:    testHarnessOnly,
		why:     "live Gitea e2e suite: the API token for that server",
		readers: []string{filepath.Join("tests", "e2e", "lifecycle", "gitea_live_test.go")},
	},
	"SHARKO_E2E_GITEA_OWNER": {
		kind:    testHarnessOnly,
		why:     "live Gitea e2e suite: the repo owner to run against",
		readers: []string{filepath.Join("tests", "e2e", "lifecycle", "gitea_live_test.go")},
	},
	"SHARKO_E2E_GITEA_REPO": {
		kind:    testHarnessOnly,
		why:     "live Gitea e2e suite: the repo name to run against",
		readers: []string{filepath.Join("tests", "e2e", "lifecycle", "gitea_live_test.go")},
	},
	"SHARKO_E2E_GITEA_BASE": {
		kind:    testHarnessOnly,
		why:     "live Gitea e2e suite: the API base path when it is not the default",
		readers: []string{filepath.Join("tests", "e2e", "lifecycle", "gitea_live_test.go")},
	},

	// --- declaredAbsent: the page says this is not implemented ---
	"SHARKO_AUDIT_BUFFER_SIZE": {
		kind:  declaredAbsent,
		why:   "ruling (c): the ring is audit.NewLog(1000), a literal; the page now says the cap is fixed",
		files: []string{},
	},
	"SHARKO_TRUSTED_PROXIES": {
		kind:  declaredAbsent,
		why:   "no trusted-proxy list exists; the security page names it to say clientIP() trusts X-Forwarded-For unconditionally",
		files: []string{filepath.Join("docs", "site", "operator", "security.md")},
	},
	"SHARKO_ARGOCD_TOKEN": {
		kind:  declaredAbsent,
		why:   "the ArgoCD token lives encrypted on the connection; the page names this to say no build reads it",
		files: []string{filepath.Join("docs", "site", "operator", "argocd-account-token-expired.md")},
	},
	"SHARKO_RECONCILER_ENABLED": {
		kind:  declaredAbsent,
		why:   "there is no reconciler kill switch; the runbook names it to say so and offers scaling to zero",
		files: []string{filepath.Join("docs", "site", "operator", "reconciler-crash-loop.md")},
	},
	"SHARKO_TRUSTED_ROOT_PATH": {
		kind:  declaredAbsent,
		why:   "Sharko cannot read a trusted root from disk; the runbook names it to say so and points at SHARKO_SIGSTORE_TUF_CACHE",
		files: []string{filepath.Join("docs", "site", "operator", "catalog-trust-root-unavailable.md")},
	},
	"SHARKO_PROVIDER": {
		kind:  declaredAbsent,
		why:   "not read; the runbook names it to point at SHARKO_CONN_PROVIDER_TYPE and friends",
		files: []string{filepath.Join("docs", "site", "operator", "azure-gcp-provider-unimplemented.md")},
	},
	"SHARKO_CLUSTER_TEST_PROVIDER": {
		kind:  declaredAbsent,
		why:   "not read; same runbook, same sentence",
		files: []string{filepath.Join("docs", "site", "operator", "azure-gcp-provider-unimplemented.md")},
	},
	"SHARKO_REPO_PATH_CLUSTER_VALUES": {
		kind:  declaredAbsent,
		why:   "only SHARKO_REPO_PATH_MANAGED_CLUSTERS is read; the page names this one as the example of a path that is fixed",
		files: []string{filepath.Join("docs", "architecture.md")},
	},
	"SHARKO_ADDON_SECRETS": {
		kind:  declaredAbsent,
		why:   "task #152 removed it; the page names it to say the env var and the endpoints are both gone",
		files: []string{filepath.Join("docs", "architecture.md")},
	},
	"SHARKO_ARGOCD_RECONCILE_INTERVAL": {
		kind:  declaredAbsent,
		why:   "retired in v3.0.0; the page names it to say it was retired",
		files: []string{filepath.Join("docs", "user-guide.md")},
	},
	"SHARKO_INIT_AUTO_BOOTSTRAP": {
		kind:  declaredAbsent,
		why:   "never implemented; the env var table says 'not yet implemented, post-v1' in the same row",
		files: []string{filepath.Join("docs", "user-guide.md")},
	},
	"SHARKO_HOST_CLUSTER_NAME": {
		kind: declaredAbsent,
		why:  "v3 name, replaced by the hostCluster.name Helm parameter; the engine chart names it as history in comments",
		files: []string{
			filepath.Join("charts", "sharko-engine", "values.yaml"),
			filepath.Join("charts", "sharko-engine", "templates", "appset.yaml"),
			filepath.Join("charts", "sharko-engine", "templates", "connectivity-check.yaml"),
		},
	},
}

// repoRootForEnvSweep walks up from the working directory until it finds go.mod.
func repoRootForEnvSweep(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not find go.mod walking up from the working directory")
	return ""
}

// mention is one documented occurrence of a name.
type mention struct {
	relPath string
	line    int
}

// collectDocumentedEnvVars returns every SHARKO_* name mentioned under
// docs/ and charts/, with where it was mentioned.
func collectDocumentedEnvVars(t *testing.T, root string) map[string][]mention {
	t.Helper()
	out := map[string][]mention{}
	for _, docRoot := range docRoots {
		base := filepath.Join(root, docRoot)
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			for _, exempt := range docPathExempt {
				if strings.HasPrefix(rel, exempt+string(filepath.Separator)) || rel == exempt {
					return nil
				}
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for i, line := range strings.Split(string(body), "\n") {
				for _, m := range envTokenRe.FindAllStringSubmatch(line, -1) {
					out[m[2]] = append(out[m[2]], mention{relPath: rel, line: i + 1})
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", base, err)
		}
	}
	return out
}

// collectReadEnvVars returns every SHARKO_* name the code or the shipped
// scripts actually read.
func collectReadEnvVars(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, ".bmad": true, "site": true,
		"dist": true, "docs": true, "charts": true, ".claude": true, ".worktrees": true,
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// An unreadable path is not this test's business to fail on.
			return nil //nolint:nilerr
		}
		if info.IsDir() {
			if path != root && skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		// NO TEST FILE IS A READ SITE.
		//
		// A _test.go file can name any string it likes, and several do —
		// ban lists, fixtures, and this file's own allowlist. Counting them
		// meant a documented setting could be "proved implemented" by a
		// single `var _ = "SHARKO_WHATEVER"` in any test in the tree, which
		// is no proof at all.
		//
		// This started as a one-file exclusion for THIS file, after a break
		// test showed the allowlist was counting itself and was therefore
		// decoration. The reviewer then found the same hole one step out:
		// the rule has to be about test files as a class, not about one
		// file that happened to be caught.
		//
		// A variable that really is read only from test code is still
		// honest to document — but it has to say so, in the allowlist,
		// naming the test that reads it. See testHarnessOnly.
		if strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		inScripts := rel == "scripts" || strings.HasPrefix(rel, "scripts"+string(filepath.Separator))
		isGo := strings.HasSuffix(path, ".go")
		if !isGo && !inScripts {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if isGo {
			// Both read shapes at once: os.Getenv("SHARKO_X") and the
			// constant declaration const envX = "SHARKO_X".
			for _, m := range goLiteralEnvRe.FindAllStringSubmatch(string(body), -1) {
				out[m[1]] = true
			}
			return nil
		}
		for _, m := range envTokenRe.FindAllStringSubmatch(string(body), -1) {
			out[m[2]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}

// TestDocumentedEnvVars_AllHaveReadSites is the guard the product owner asked
// for by name: a test preventing unsupported configuration claims from
// returning unnoticed.
func TestDocumentedEnvVars_AllHaveReadSites(t *testing.T) {
	root := repoRootForEnvSweep(t)
	documented := collectDocumentedEnvVars(t, root)
	read := collectReadEnvVars(t, root)

	// Sanity: if the read scan finds nothing, the walk is broken and every
	// assertion below would be a false alarm.
	if len(read) < 40 {
		t.Fatalf("only %d SHARKO_* names found as read sites — the scan is broken, not the docs", len(read))
	}

	var offenders []string
	for name, mentions := range documented {
		if read[name] {
			continue
		}
		allowed, ok := allowedDocumentedEnvVars[name]
		if !ok {
			sort.Slice(mentions, func(i, j int) bool { return mentions[i].relPath < mentions[j].relPath })
			first := mentions[0]
			offenders = append(offenders, name+" — documented at "+first.relPath+":"+itoa(first.line)+
				" and "+itoa(len(mentions))+" place(s) in total, but no Go file and no script reads it")
			continue
		}
		if allowed.kind != declaredAbsent {
			continue
		}
		// A declaredAbsent name may only appear where the prose says it is
		// absent. Anywhere else it is a fresh live claim.
		permitted := map[string]bool{}
		for _, f := range allowed.files {
			permitted[f] = true
		}
		for _, m := range mentions {
			if !permitted[m.relPath] {
				offenders = append(offenders, name+" — allowlisted as not-implemented for "+
					strings.Join(allowed.files, ", ")+" but it now also appears at "+
					m.relPath+":"+itoa(m.line))
			}
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("documentation claims configuration Sharko does not implement:\n\n  %s\n\n"+
			"Fix the documentation — name the variable the code really reads, or say plainly "+
			"that the setting does not exist. Adding an allowlist entry to silence this is the "+
			"same dishonesty the test exists to remove; the only three honest reasons are "+
			"shellLocal (the doc assigns the variable itself), declaredAbsent (the prose says "+
			"Sharko does not implement it) and testHarnessOnly (a named test file really reads "+
			"it). A string literal sitting in some test file is NOT a read site.",
			strings.Join(offenders, "\n  "))
	}
}

// TestDocumentedEnvVars_AllowlistEntriesAreJustified keeps the allowlist from
// rotting into a silent dumping ground. Every entry carries a reason, and a
// declaredAbsent entry names at least the files it lives in — or claims none
// at all, which means the name is fully gone from the docs.
func TestDocumentedEnvVars_AllowlistEntriesAreJustified(t *testing.T) {
	root := repoRootForEnvSweep(t)
	documented := collectDocumentedEnvVars(t, root)

	for name, entry := range allowedDocumentedEnvVars {
		if strings.TrimSpace(entry.why) == "" {
			t.Errorf("allowlist entry %s has no reason — every entry needs one line saying why", name)
		}
		if entry.kind == declaredAbsent {
			for _, f := range entry.files {
				if _, statErr := os.Stat(filepath.Join(root, f)); statErr != nil {
					t.Errorf("allowlist entry %s names %s, which does not exist", name, f)
				}
			}
			if len(entry.files) == 0 && len(documented[name]) > 0 {
				t.Errorf("allowlist entry %s claims no files but is still documented at %s:%d — "+
					"either remove the mention or record the file",
					name, documented[name][0].relPath, documented[name][0].line)
			}
		}
	}
}

// TestDocumentedEnvVars_TestHarnessReadersReallyReadThem is the guard on the
// testHarnessOnly kind. The entry claims "this IS read, just only from a
// test" — so the named test file must exist and must really contain the
// quoted name. Without this, testHarnessOnly would be a free pass: anyone
// could park a name there with a plausible sentence and no reader at all,
// which is exactly the shape of the hole this kind was created to close.
func TestDocumentedEnvVars_TestHarnessReadersReallyReadThem(t *testing.T) {
	root := repoRootForEnvSweep(t)

	for name, entry := range allowedDocumentedEnvVars {
		if entry.kind != testHarnessOnly {
			continue
		}
		if len(entry.readers) == 0 {
			t.Errorf("allowlist entry %s is testHarnessOnly but names no reader — "+
				"name the test file that reads it, or it is not test-harness-only, it is unread",
				name)
			continue
		}
		found := false
		for _, reader := range entry.readers {
			body, err := os.ReadFile(filepath.Join(root, reader))
			if err != nil {
				t.Errorf("allowlist entry %s names reader %s, which cannot be read: %v", name, reader, err)
				continue
			}
			if !strings.HasSuffix(reader, "_test.go") {
				t.Errorf("allowlist entry %s names reader %s, which is not a test file — "+
					"if a production file reads it, the name belongs in neither this kind nor this list",
					name, reader)
			}
			if strings.Contains(string(body), `"`+name+`"`) {
				found = true
			}
		}
		if !found {
			t.Errorf("allowlist entry %s says %s reads it, but that file does not contain %q — "+
				"the harness was renamed or deleted and the entry is now a lie",
				name, strings.Join(entry.readers, ", "), name)
		}
	}
}

// TestDocumentedEnvVars_DeclaredAbsentNamesAreGenuinelyUnread is the guard on
// the guard. A declaredAbsent entry says "Sharko does not implement this", and
// that claim is only true if the name really has no read site. If it IS read,
// either the documentation is now wrong in the other direction, or the read
// scan is counting something that is not a read site — which is exactly what
// happened when this file's own string literals were being scanned, and a
// break test caught it only because it was run and watched.
//
// shellLocal entries are deliberately NOT checked here: a name the runbooks
// assign themselves is very often also a shell variable inside scripts/, and
// that is expected, not a defect.
func TestDocumentedEnvVars_DeclaredAbsentNamesAreGenuinelyUnread(t *testing.T) {
	root := repoRootForEnvSweep(t)
	read := collectReadEnvVars(t, root)

	for name, entry := range allowedDocumentedEnvVars {
		if entry.kind != declaredAbsent {
			continue
		}
		if read[name] {
			t.Errorf("allowlist entry %s says Sharko does not implement it (%s), but it IS "+
				"read by the code or a script — either the documentation is now wrong the "+
				"other way round, or the read scan is counting something that is not a read site",
				name, entry.why)
		}
	}
}

// itoa keeps the message building free of strconv noise at the call sites.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

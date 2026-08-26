package envreg

// documented_env_vars_test.go — the guard against documenting
// configuration the product does not implement.
//
// This is the rewrite of internal/config/documented_env_vars_test.go
// against the registry. The parallel allowedDocumentedEnvVars allowlist
// is gone: a name is now justified by being registered, with a kind and
// a reader the guards check, not by an entry in a second list that only
// this test ever read.
//
// # What changed, and why each change matters
//
// RULE 4 — scripts/ is no longer a read site. The old rule said "a name
// a shipped script reads IS implemented, just not by the server binary",
// and wrote down the hole it left. The hole was live:
// SHARKO_GITOPS_REPO_URL is named in an operator runbook and passed only
// because a script under scripts/ uses a shell variable of that name. A
// shell-local cannot prove a server setting exists.
//
// RULE 5 — charts/ is no longer documentation. A chart template SETS a
// value. Setting one is not the same as telling an operator the setting
// exists, and treating it as documentation made a name with no reader
// look accounted for.
//
// # The baseline, and why it is a list
//
// Not every SHARKO_ string in the documentation is a claim that a
// setting exists. A runbook assigns its own shell variables; Kubernetes
// injects names into Sharko's Pod; and some prose names a setting
// precisely in order to say Sharko does not have one. Those are written
// down here by name, with a line each saying which they are.
//
// The list only ever gets shorter: the test fails if a name is added to
// the docs that is not registered, AND if a listed name has been fixed
// and the entry left behind. Both directions are break-tested.
//
// It used to carry a fourth class — names the docs really did claim and
// Sharko really did not read — on the reasoning that the corrections
// would land on the docs branch. Those corrections are now in the same
// tree as this guard, so that class is gone; see the note above
// knownUndocumentedClaims for why waiting stopped being an option.

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// docRoots are the trees and files whose SHARKO_* mentions are treated as
// claims about what an operator can configure.
//
// charts/ is NOT here. See RULE 5 above and
// TestRule5_ChartsAreASetterNotDocumentation.
//
// README.md is here because docs/site/operator/configuration.md sends the
// reader there for the full list of supported settings, and a page that
// is the named destination for configuration questions is documentation
// whatever its filename.
var docRoots = []string{"docs", "README.md"}

// docPathExempt are paths inside docRoots that are not claims about
// today's product. Keep this at exactly these two entries; a reviewer
// should push back on any attempt to add a third.
var docPathExempt = []string{
	filepath.Join("docs", "design"),  // dated design records and plans, not operator instructions, not published
	filepath.Join("docs", "swagger"), // generated from Go annotations
}

// knownUndocumentedClaims are the SHARKO_* names the documentation still
// mentions without a registered setting behind them.
//
// Each entry says what the name really is, using the classes story
// B8-finish established:
//
//	shell-local   the runbook assigns the variable itself in the snippet
//	              the reader is about to run; it was never a setting
//	declared-absent  the prose names it in order to say Sharko does not
//	              implement it; deleting the name deletes the warning
//	kubernetes-injected  Kubernetes writes it into Sharko's own Pod. The
//	              page names it so an operator recognises it in `env`
//	              output; Sharko never reads it
//	no-reader     a live claim with nothing behind it — a real defect.
//	              This class is EMPTY and must stay empty
//
// THE FIRST THREE CLASSES ONLY EVER GET SHORTER BY BEING FIXED; the
// no-reader class only ever gets shorter, full stop.
//
// The one class that can legitimately grow is kubernetes-injected, and
// only for a name Kubernetes really does write. It is not an escape
// hatch: TestKnownUndocumentedClaims_AreJustified still refuses an entry
// for a name that IS registered, so it can never be used to silence the
// registry, and every entry still has to say what the name really is.
//
// # The no-reader class is empty now, and why that mattered urgently
//
// It held fifteen names when this file was written, on the reasoning
// that the corrections would land on the docs branch. That stopped being
// good enough the moment unknown.go shipped: a SHARKO_ name Sharko does
// not recognise no longer gets ignored, it stops the server. Every one
// of those fifteen was a page telling an operator to set a name Sharko
// has never read — SHARKO_GITOPS_PR_AUTO_MERGE was a copy-and-paste
// extraEnv block under a heading explaining how to turn auto-merge on.
// Following Sharko's own published instructions produced a server that
// would not boot. The corrections and the rule that made them urgent are
// now in the same tree, and the class is empty.
//
// # files, and why declared-absent entries must carry them
//
// Carried over at compose time from the guard this file replaced
// (internal/config/documented_env_vars_test.go, now deleted). That guard
// scoped every declared-absent name to the exact files it was allowed to
// appear in, and treated a mention anywhere else as a fresh live claim.
// Without that, "the page names it in order to say Sharko does not
// implement it" stops being checkable the moment a second page starts
// telling an operator to set it: the name is in this list, so the guard
// waves it through.
//
// So: a declared-absent entry MUST name its files, and the name may
// appear nowhere else. Shell-local entries are deliberately NOT scoped —
// a variable the runbooks assign themselves shows up in many runbooks,
// and that is expected rather than a defect. That is exactly the split
// the old guard made.
type documentedClaim struct {
	// why is mandatory. A baseline without reasons rots into a dumping
	// ground.
	why string

	// files are the repo-relative paths this name may appear in.
	// Mandatory for declared-absent entries, empty for shell-local ones.
	files []string
}

var knownUndocumentedClaims = map[string]documentedClaim{
	// --- kubernetes-injected: kubelet writes these into every Pod, in
	// --- the old Docker-link format, named after each Service in the
	// --- namespace. Sharko's Service is called sharko, so they land in
	// --- Sharko's own Pod. The operator page lists them verbatim to
	// --- explain why the chart sets enableServiceLinks: false — an
	// --- operator comparing the page against `kubectl exec … env` needs
	// --- to see the same strings. SHARKO_PORT is the fourth of them and
	// --- is NOT here: it is a registered deprecated alias, because it
	// --- collides with a name Sharko really did publish ---
	"SHARKO_SERVICE_HOST": {why: "kubernetes-injected: kubelet writes it from the Service name; Sharko never reads it"},
	"SHARKO_SERVICE_PORT": {why: "kubernetes-injected: kubelet writes it from the Service name; Sharko never reads it"},
	"SHARKO_PORT_80_TCP":  {why: "kubernetes-injected: kubelet writes it from the Service name and port; Sharko never reads it"},

	// --- shell-local: the runbook assigns the variable itself, in the
	// --- snippet the reader is about to run. Never a Sharko setting ---
	"SHARKO_NS":                  {why: "shell-local: the namespace the operator types once and reuses across a runbook's commands"},
	"SHARKO_POD":                 {why: "shell-local: SHARKO_POD=$(kubectl get pod …) before the next command uses it"},
	"SHARKO_URL":                 {why: "shell-local: the Sharko base URL in runbook and smoke-test curl commands"},
	"SHARKO_TOKEN":               {why: "shell-local: the caller's own API token, held for curl"},
	"SHARKO_ADMIN_TOKEN":         {why: "shell-local: an admin API token, held for curl"},
	"SHARKO_PREFIX":              {why: "shell-local: holds the value read out of SHARKO_CONN_PROVIDER_PREFIX"},
	"SHARKO_SECRETS_NS":          {why: "shell-local: namespace with a ${VAR:-default} fallback"},
	"SHARKO_VERSION":             {why: "shell-local: the image tag parsed off the deployment"},
	"SHARKO_EKS_TEST_ACCOUNT_ID": {why: "shell-local: the EKS runbook exports the reader's own AWS account id for scripts/eks-live-test.sh"},
	"SHARKO_GITHUB_TOKEN":        {why: "shell-local: the EKS runbook exports the reader's own GitHub token for the same script"},
	"SHARKO_GITOPS_REPO_URL":     {why: "shell-local: the third export in that same block; scripts/eks-live-test.sh reads all three as shell variables and Sharko reads none of them. It was recorded as a defect when this list was written, because it was the example that proved a shell variable under scripts/ must not count as a read site — but the name itself is honest where it stands"},
	"SHARKO_E2E_URL":             {why: "shell-local: the contributor walk exports it on the go test command line"},
	"SHARKO_E2E_USERNAME":        {why: "shell-local: same command line"},
	"SHARKO_E2E_PASSWORD":        {why: "shell-local: same command line"},
	"SHARKO_THIRDPARTY_URL":      {why: "shell-local: the catalog-sources smoke script exports it for its own run"},
	"SHARKO_DEV_PW_CACHE":        {why: "shell-local: overrides the dev script's password cache file path"},
	"SHARKO_LOCAL_PORT":          {why: "shell-local: the port the contributor walk forwards to"},

	// --- declared-absent: the prose names it in order to say Sharko
	// --- does NOT implement it. Deleting the name deletes the warning.
	// --- Scoped to the page that says it, so the same name cannot come
	// --- back somewhere else as a live instruction ---
	"SHARKO_ARGOCD_TOKEN": {
		why:   "declared-absent: the ArgoCD token lives encrypted on the connection; the page names this to say no build reads it",
		files: []string{"docs/site/operator/argocd-account-token-expired.md"},
	},
	"SHARKO_RECONCILER_ENABLED": {
		why:   "declared-absent: there is no reconciler kill switch; the runbook names it to say so",
		files: []string{"docs/site/operator/reconciler-crash-loop.md"},
	},
	"SHARKO_TRUSTED_ROOT_PATH": {
		why:   "declared-absent: Sharko cannot read a trusted root from disk; the runbook points at SHARKO_SIGSTORE_TUF_CACHE",
		files: []string{"docs/site/operator/catalog-trust-root-unavailable.md"},
	},
	"SHARKO_PROVIDER": {
		why:   "declared-absent: not read; the runbook points at SHARKO_CONN_PROVIDER_TYPE",
		files: []string{"docs/site/operator/azure-gcp-provider-unimplemented.md"},
	},
	"SHARKO_CLUSTER_TEST_PROVIDER": {
		why:   "declared-absent: not read; same runbook, same sentence",
		files: []string{"docs/site/operator/azure-gcp-provider-unimplemented.md"},
	},
	"SHARKO_ADDON_SECRETS": {
		why:   "declared-absent: task #152 removed it; the page names it to say the variable and the endpoints are both gone",
		files: []string{"docs/architecture.md"},
	},
	"SHARKO_ARGOCD_RECONCILE_INTERVAL": {
		why:   "declared-absent: retired in v3.0.0; the page names it to say it was retired",
		files: []string{"docs/user-guide.md"},
	},
	"SHARKO_INIT_AUTO_BOOTSTRAP": {
		why:   "declared-absent: never implemented; the table row says so in the same line",
		files: []string{"docs/user-guide.md"},
	},
	"SHARKO_GITOPS_BASE_BRANCH": {
		why:   "declared-absent: docs/site/operator/configuration.md names it to say Sharko has never read it, and points at SHARKO_CONN_GITOPS_BASE_BRANCH",
		files: []string{"docs/site/operator/configuration.md"},
	},
	"SHARKO_GITOPS_PR_AUTO_MERGE": {
		why:   "declared-absent: docs/site/operator/configuration.md and docs/site/user-guide/upgrades.md both name it to say Sharko has never read it, and that a values file still carrying it will not start",
		files: []string{"docs/site/operator/configuration.md", "docs/site/user-guide/upgrades.md"},
	},
}

type mention struct {
	relPath string
	line    int
}

// collectDocumentedEnvVars returns every SHARKO_* name mentioned under
// the documentation roots, with where it was mentioned.
func collectDocumentedEnvVars(t *testing.T, root string) map[string][]mention {
	t.Helper()
	out := map[string][]mention{}
	for _, docRoot := range docRoots {
		base := filepath.Join(root, docRoot)
		info, err := os.Stat(base)
		if err != nil {
			t.Fatalf("stat %s: %v", docRoot, err)
		}
		if !info.IsDir() {
			scanDocFile(t, root, base, out)
			continue
		}
		err = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			for _, exempt := range docPathExempt {
				if rel == exempt || strings.HasPrefix(rel, exempt+string(filepath.Separator)) {
					if info.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
			}
			if info.IsDir() {
				return nil
			}
			scanDocFile(t, root, path, out)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", base, err)
		}
	}
	return out
}

func scanDocFile(t *testing.T, root, path string, out map[string][]mention) {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	for i, line := range strings.Split(string(body), "\n") {
		for _, m := range envTokenRe.FindAllStringSubmatch(line, -1) {
			out[m[2]] = append(out[m[2]], mention{relPath: filepath.ToSlash(rel), line: i + 1})
		}
	}
}

// TestDocumentedEnvVars_AllAreRegistered is the guard the product owner
// asked for by name: documentation may not claim configuration the
// product does not implement.
func TestDocumentedEnvVars_AllAreRegistered(t *testing.T) {
	root := repoRootForEnvSweep(t)
	documented := collectDocumentedEnvVars(t, root)

	if len(documented) < 30 {
		t.Fatalf("only %d SHARKO_* names found across the documentation — the doc scan is broken, not the docs", len(documented))
	}

	var offenders []string
	for name, mentions := range documented {
		if _, registered := Get(name); registered {
			continue
		}
		sort.Slice(mentions, func(i, j int) bool {
			if mentions[i].relPath != mentions[j].relPath {
				return mentions[i].relPath < mentions[j].relPath
			}
			return mentions[i].line < mentions[j].line
		})
		if claim, known := knownUndocumentedClaims[name]; known {
			// A scoped entry may only appear in the files it names.
			// Anywhere else it is a fresh live claim wearing an old
			// entry's coat — the check carried over from the guard
			// this file replaced.
			if len(claim.files) == 0 {
				continue
			}
			permitted := map[string]bool{}
			for _, f := range claim.files {
				permitted[filepath.ToSlash(f)] = true
			}
			for _, m := range mentions {
				if permitted[m.relPath] {
					continue
				}
				offenders = append(offenders, name+" — recorded as not implemented, allowed only in "+
					strings.Join(claim.files, ", ")+", but it now also appears at "+
					m.relPath+":"+strconv.Itoa(m.line))
			}
			continue
		}
		first := mentions[0]
		offenders = append(offenders, name+" — documented at "+first.relPath+":"+strconv.Itoa(first.line)+
			" and "+strconv.Itoa(len(mentions))+" place(s) in total, with no registered setting behind it")
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("%d documented name(s) with no registered setting:\n\n  %s\n\n"+
			"Either the documentation names the wrong variable — register the name the code really "+
			"reads and correct the page — or the setting does not exist and the page should say so "+
			"plainly. A string in a test file or a shell variable under scripts/ is NOT evidence a "+
			"setting exists; that is exactly what this rewrite removed.",
			len(offenders), strings.Join(offenders, "\n  "))
	}

	// The ratchet. A baseline allowed to stay bigger than reality is a
	// place for a new defect to hide behind an old entry.
	var stale []string
	for name := range knownUndocumentedClaims {
		if _, stillDocumented := documented[name]; stillDocumented {
			if _, registered := Get(name); !registered {
				continue
			}
			stale = append(stale, name+" (now registered — the entry is obsolete)")
			continue
		}
		stale = append(stale, name+" (no longer mentioned anywhere in the docs)")
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%d recorded claim(s) are fixed — delete them from knownUndocumentedClaims:\n\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// TestKnownUndocumentedClaims_AreJustified keeps the baseline from
// rotting into a silent dumping ground, and refuses the one shape that
// would make it dangerous: an entry for a name that IS registered, which
// would silence the registry rather than the documentation.
//
// It also holds the scoping rule up: a declared-absent entry has to name
// the files it lives in, and those files have to exist. Without that
// check somebody could write a new declared-absent entry with no files
// and get an unscoped pass, which is the hole the scoping closes.
func TestKnownUndocumentedClaims_AreJustified(t *testing.T) {
	root := repoRootForEnvSweep(t)

	for name, claim := range knownUndocumentedClaims {
		if strings.TrimSpace(claim.why) == "" {
			t.Errorf("baseline entry %s has no reason — every entry needs one line saying what the name really is", name)
		}
		if !strings.HasPrefix(name, "SHARKO_") {
			t.Errorf("baseline entry %q is not a SHARKO_ name", name)
		}
		if _, registered := Get(name); registered {
			t.Errorf("baseline entry %s IS a registered setting — an entry here would excuse a real "+
				"setting instead of a documentation defect. Delete it.", name)
		}

		declaredAbsent := strings.HasPrefix(strings.TrimSpace(claim.why), "declared-absent:")
		if declaredAbsent && len(claim.files) == 0 {
			t.Errorf("baseline entry %s is declared-absent but names no files. A declared-absent entry "+
				"says the prose mentions the name in order to warn that Sharko does not implement it — "+
				"so it has to say which pages, or the same name can come back on a different page as a "+
				"live instruction and this list waves it through.", name)
		}
		if !declaredAbsent && len(claim.files) > 0 {
			t.Errorf("baseline entry %s names files but is not declared-absent. Shell-local names are "+
				"deliberately unscoped — they turn up in many runbooks and that is expected.", name)
		}
		for _, f := range claim.files {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f))); err != nil {
				t.Errorf("baseline entry %s names %s, which does not exist: %v", name, f, err)
			}
		}
	}
}

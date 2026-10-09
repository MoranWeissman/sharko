package api

// connection_gate_guard_test.go — the proof that no site was missed (B1).
//
// # What this guards
//
// B1 replaced sixty-four hand-written copies of the same leak with one shared
// pair of helpers in connection_gate.go. That trade is only safe if something
// can prove, mechanically and forever, which sites go through the helpers. A
// shared fix with no coverage proof is worse than sixty-four hand edits,
// because the hand edits at least had to be read one at a time.
//
// # Why it is a LIST and not a count
//
// A count gets HAPPIER as the bug spreads. "At least N sites route through the
// gate" still passes when somebody adds a sixty-fifth site that does not — and
// a guard of exactly that shape was found in this repository this round. So
// this is a list, every entry named, and it fails in BOTH directions:
//
//   - a function that resolves the active Git or ArgoCD connection and is not
//     in the list at all → a new site nobody classified;
//   - a listed function that no longer exists, or no longer resolves a
//     connection → a stale entry, which is a hole nobody asked for;
//   - a listed function marked `routed` that no longer calls a gate helper →
//     the fix was undone under a name that still claims it is there;
//   - a listed function marked `notRouted` that HAS started calling a gate
//     helper → the list has drifted away from the tree and stopped describing
//     it.
//
// # It refuses to pass vacuously
//
// Before comparing anything it asserts that the walk actually parsed files,
// actually found sites, and actually found BOTH kinds — routed and not. A
// guard whose discovery step silently returned nothing would otherwise agree
// with any list at all, including an empty one.
//
// # What `notRouted` means, and why those sites are not bugs
//
// Forty of the ninety-six do not go through the gate, and they are correct as
// they are. They fall in two kinds:
//
//   - "writeServerError": the failure goes to writeServerError, which is
//     already a sanitizing sink — it writes only a status headline plus a
//     cause drawn from a fixed classification table, never the error's own
//     text. (Its slog line used to carry the raw error. B9 closed that, at
//     the log sink rather than here: internal/logging's RedactHandler now
//     replaces any error handed to slog with credsafe.LogClass. See
//     internal/logging/log_error_guard_test.go.)
//
//   - "swallowed": the failure never becomes words at all — the handler
//     degrades to a partial answer, flips an "unavailable" flag, returns a
//     bool, or returns the error to a caller that only tests it against nil.
//
// If either of those stops being true for a given function, the right move is
// to route it through the gate and change its entry here — not to widen the
// reason.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// connectionLookups are the calls that hand back a client built out of the
// saved connection. Their error is the one B1 is about: on the Git side it can
// be net/url quoting a repository URL with the access token inside it.
var connectionLookups = map[string]string{
	"GetActiveGitProvider":              "git",
	"GitProviderForTier":                "git",
	"GetActiveArgocdClient":             "argocd",
	"GetActiveOrchestratorArgocdClient": "argocd",
}

// connectionGates are the only sanctioned answers to a failed lookup. None of
// them accepts an error or a string.
var connectionGates = map[string]string{
	"writeNoActiveGitConnection":               "git",
	"writeNoActiveGitConnectionUnavailable":    "git",
	"writeNoActiveArgocdConnection":            "argocd",
	"writeNoActiveArgocdConnectionUnavailable": "argocd",
	"ErrNoActiveGitConnection":                 "git",
	"ErrNoActiveArgocdConnection":              "argocd",
}

// connectionGateSweepDirs are the Go directories walked. internal/remediation
// is here because its LazyArgoClient resolves the same connection and used to
// wrap the same raw error.
var connectionGateSweepDirs = []string{"internal/api", "internal/remediation"}

// connectionGateSite is one entry in the list. Exactly one of routed /
// notRouted is set.
type connectionGateSite struct {
	file      string
	fn        string
	lookups   string // "git", "argocd" or "argocd,git" — what it resolves
	routed    string // which gate halves it calls
	notRouted string // why it does not need one
}

// connectionGateSites is THE LIST. Every function in the swept directories
// that resolves the active Git or ArgoCD connection appears here exactly once.
var connectionGateSites = []connectionGateSite{
	{file: "internal/api/addon_ops.go", fn: "handleDisableAddon", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addon_ops.go", fn: "handleEnableAddon", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addon_ops_v4.go", fn: "handleDisableAddonV4", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addon_ops_v4.go", fn: "handleEnableAddonV4", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addons.go", fn: "handleGetAddonCatalog", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/addons.go", fn: "handleGetAddonDetail", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/addons.go", fn: "handleGetAddonValues", lookups: "git", notRouted: "writeServerError"},
	{file: "internal/api/addons.go", fn: "handleGetVersionMatrix", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/addons.go", fn: "handleListAddons", lookups: "git", notRouted: "writeServerError"},
	{file: "internal/api/addons_changelog.go", fn: "handleGetAddonChangelog", lookups: "git", routed: "git"},
	{file: "internal/api/addons_upgrade.go", fn: "handleUpgradeAddon", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addons_upgrade.go", fn: "handleUpgradeAddonClustersV4", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addons_upgrade.go", fn: "handleUpgradeAddonsBatch", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addons_write.go", fn: "handleAddAddon", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addons_write.go", fn: "handleConfigureAddon", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/addons_write.go", fn: "handleRemoveAddon", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/agent.go", fn: "handleAgentChat", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/ai_annotate.go", fn: "handleAnnotateAddonValues", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/ai_annotate.go", fn: "handleSetAddonAIOptOut", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/catalog_org.go", fn: "ApprovedAddonsForFreshness", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/catalog_org.go", fn: "handleAddToCatalog", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/catalog_org.go", fn: "handleDeleteOrgCatalogAddon", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/catalog_org.go", fn: "handleEditOrgCatalogAddon", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/catalog_org.go", fn: "handleGetOrgCatalogAddon", lookups: "git", notRouted: "writeServerError"},
	{file: "internal/api/catalog_org.go", fn: "handleListOrgCatalog", lookups: "git", notRouted: "writeServerError"},
	{file: "internal/api/cluster_changes.go", fn: "handleGetClusterChanges", lookups: "argocd,git", notRouted: "swallowed"},
	{file: "internal/api/cluster_history.go", fn: "handleGetClusterHistory", lookups: "argocd,git", routed: "argocd"},
	{file: "internal/api/clusters.go", fn: "handleGetCluster", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/clusters.go", fn: "handleGetClusterComparison", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/clusters.go", fn: "handleGetClusterValues", lookups: "git", notRouted: "writeServerError"},
	{file: "internal/api/clusters.go", fn: "handleGetConfigDiff", lookups: "git", notRouted: "writeServerError"},
	{file: "internal/api/clusters.go", fn: "handleListClusters", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/clusters_adopt.go", fn: "handleAdoptClusters", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/clusters_adopt.go", fn: "handleUnadoptCluster", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/clusters_batch.go", fn: "handleBatchRegisterClusters", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/clusters_discover.go", fn: "handleDiscoverClusters", lookups: "argocd", routed: "argocd"},
	{file: "internal/api/clusters_doctor.go", fn: "connectivityAppDriftFix", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/clusters_doctor.go", fn: "doctorCheckAddonSecretPaths", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/clusters_doctor.go", fn: "doctorCheckConnectivityApp", lookups: "argocd", notRouted: "swallowed"},
	{file: "internal/api/clusters_orphan_delete.go", fn: "handleDeleteOrphanCluster", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/clusters_reconcile.go", fn: "handleReconcileCluster", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/clusters_restart_sync.go", fn: "handleRestartAddonSync", lookups: "argocd", routed: "argocd"},
	{file: "internal/api/clusters_resync.go", fn: "handleResyncCluster", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/clusters_takeover.go", fn: "gatherPreflightInputs", lookups: "argocd", notRouted: "swallowed"},
	{file: "internal/api/clusters_takeover.go", fn: "handleClusterTakeover", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/clusters_takeover.go", fn: "handleClusterUnregisterConsequences", lookups: "argocd,git", notRouted: "swallowed"},
	{file: "internal/api/clusters_takeover.go", fn: "registeredClusterNames", lookups: "git", routed: "git"},
	{file: "internal/api/clusters_write.go", fn: "handleDeregisterCluster", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/clusters_write.go", fn: "handleRefreshClusterCredentials", lookups: "argocd", routed: "argocd"},
	{file: "internal/api/clusters_write.go", fn: "handleRegisterCluster", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/clusters_write.go", fn: "handleUpdateClusterAddons", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/connection_credential_check.go", fn: "runOnce", lookups: "argocd,git", notRouted: "swallowed"},
	{file: "internal/api/connection_reconciliation.go", fn: "argoConnectionHealth", lookups: "argocd", notRouted: "swallowed"},
	{file: "internal/api/cred_lookup.go", fn: "credentialRouting", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/dashboard.go", fn: "handleGetAttentionItems", lookups: "argocd", notRouted: "writeServerError"},
	{file: "internal/api/dashboard.go", fn: "handleGetDashboardStats", lookups: "argocd,git", notRouted: "writeServerError"},
	{file: "internal/api/dashboard.go", fn: "handleGetPullRequests", lookups: "git", notRouted: "writeServerError"},
	{file: "internal/api/default_addons.go", fn: "ReadDefaultAddons", lookups: "git", routed: "git"},
	{file: "internal/api/default_addons.go", fn: "handlePutDefaultAddons", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/engine_pin.go", fn: "CheckEnginePinLive", lookups: "git", routed: "git"},
	{file: "internal/api/engine_pin.go", fn: "handleUpgradeEnginePin", lookups: "git", routed: "git"},
	{file: "internal/api/fleet.go", fn: "computeFleetStatus", lookups: "argocd,git", notRouted: "swallowed"},
	{file: "internal/api/init.go", fn: "handleInit", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/init_status.go", fn: "handleInitStatus", lookups: "argocd,git", routed: "git"},
	{file: "internal/api/migration.go", fn: "CompleteMigrationHandoffOnMerge", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/migration.go", fn: "handleMigrateRepo", lookups: "git", routed: "git"},
	{file: "internal/api/migration.go", fn: "handleMigrationComplete", lookups: "git", routed: "git"},
	{file: "internal/api/migration.go", fn: "handleMigrationPreview", lookups: "git", routed: "git"},
	{file: "internal/api/migration.go", fn: "handleMigrationStatus", lookups: "git", routed: "git"},
	{file: "internal/api/migration.go", fn: "migrationOrchestrator", lookups: "argocd", notRouted: "swallowed"},
	{file: "internal/api/observability.go", fn: "handleGetObservabilityOverview", lookups: "argocd,git", routed: "argocd"},
	{file: "internal/api/prs.go", fn: "handleListMergedPRs", lookups: "git", routed: "git"},
	{file: "internal/api/repo_status.go", fn: "handleRepoStatus", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/repo_status.go", fn: "probeBootstrapSynced", lookups: "argocd", notRouted: "swallowed"},
	{file: "internal/api/system.go", fn: "handleGetConfig", lookups: "argocd", notRouted: "swallowed"},
	{file: "internal/api/system_managed_secrets.go", fn: "handleGetManagedSecrets", lookups: "argocd,git", notRouted: "swallowed"},
	{file: "internal/api/tiered_git.go", fn: "providerFromConnectionWithToken", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/upgrade.go", fn: "handleCheckUpgrade", lookups: "git", routed: "git"},
	{file: "internal/api/upgrade.go", fn: "handleGetAISummary", lookups: "git", routed: "git"},
	{file: "internal/api/upgrade.go", fn: "handleGetRecommendations", lookups: "git", routed: "git"},
	{file: "internal/api/upgrade.go", fn: "handleListUpgradeVersions", lookups: "git", routed: "git"},
	{file: "internal/api/v3_migration_gate.go", fn: "refuseV3WriteOnActiveRepo", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/v4_editor_gate.go", fn: "refuseV3ValuesSurfaceOnActiveRepo", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/values_editor.go", fn: "handleGetAddonValuesSchema", lookups: "argocd,git", routed: "git"},
	{file: "internal/api/values_editor.go", fn: "handleGetClusterAddonValues", lookups: "git", routed: "git"},
	{file: "internal/api/values_editor.go", fn: "handleSetAddonValues", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/values_editor.go", fn: "handleSetClusterAddonValues", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/values_extra.go", fn: "fetchRecentPRs", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/values_extra.go", fn: "handleGetAddonValuesRecentPRs", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/values_extra.go", fn: "handleGetClusterAddonValuesRecentPRs", lookups: "git", notRouted: "swallowed"},
	{file: "internal/api/values_preview_merge.go", fn: "handlePreviewMergeAddonValues", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/api/values_unwrap_migration.go", fn: "handleUnwrapGlobalValues", lookups: "argocd,git", routed: "argocd,git"},
	{file: "internal/remediation/remediation.go", fn: "CanSyncApplication", lookups: "argocd", notRouted: "swallowed"},
	{file: "internal/remediation/remediation.go", fn: "ListApplications", lookups: "argocd", routed: "argocd"},
	{file: "internal/remediation/remediation.go", fn: "RefreshApplication", lookups: "argocd", routed: "argocd"},
	{file: "internal/remediation/remediation.go", fn: "SyncApplication", lookups: "argocd", routed: "argocd"},
	{file: "internal/remediation/remediation.go", fn: "TerminateOperation", lookups: "argocd", routed: "argocd"}}

// discoveredGateSite is what the walk finds for one function.
type discoveredGateSite struct {
	lookups map[string]bool
	gates   map[string]bool
}

// discoverConnectionGateSites walks the swept directories and reports, per
// function, which connection lookups it performs and which gate helpers it
// calls.
func discoverConnectionGateSites(t *testing.T) map[string]discoveredGateSite {
	t.Helper()
	root := repoRootForSweep(t)
	found := map[string]discoveredGateSite{}
	filesParsed := 0

	for _, dir := range connectionGateSweepDirs {
		abs := filepath.Join(root, dir)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("swept directory %q does not exist — the guard silently covers less than it claims: %v", dir, err)
		}
		walkErr := filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				t.Fatalf("parsing %s: %v", path, parseErr)
			}
			filesParsed++
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				t.Fatalf("relpath %s: %v", path, relErr)
			}
			rel = filepath.ToSlash(rel)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				site := discoveredGateSite{lookups: map[string]bool{}, gates: map[string]bool{}}
				ast.Inspect(fn, func(n ast.Node) bool {
					switch node := n.(type) {
					case *ast.SelectorExpr:
						if half, isLookup := connectionLookups[node.Sel.Name]; isLookup {
							site.lookups[half] = true
						}
						if half, isGate := connectionGates[node.Sel.Name]; isGate {
							site.gates[half] = true
						}
					case *ast.Ident:
						if half, isGate := connectionGates[node.Name]; isGate {
							site.gates[half] = true
						}
					}
					return true
				})
				if len(site.lookups) > 0 {
					found[rel+"::"+fn.Name.Name] = site
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walking %s: %v", dir, walkErr)
		}
	}

	// Non-vacuity. A discovery step that quietly returned nothing would agree
	// with any list, including an empty one.
	if filesParsed < 50 {
		t.Fatalf("the walk parsed only %d Go files across %v — it is not reaching the tree, so nothing it reports can be trusted", filesParsed, connectionGateSweepDirs)
	}
	if len(found) == 0 {
		t.Fatal("the walk found NO function that resolves the active Git or ArgoCD connection. That cannot be true of this codebase — the detector is broken, and a broken detector makes this whole guard pass for free.")
	}
	return found
}

func joinHalves(set map[string]bool) string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// TestConnectionGate_TheListIsWellFormed pins the list itself before it is
// used to judge anything: no duplicates, no entry that is both routed and not,
// no entry that is neither, and both kinds actually present.
func TestConnectionGate_TheListIsWellFormed(t *testing.T) {
	if len(connectionGateSites) == 0 {
		t.Fatal("connectionGateSites is empty — an empty list agrees with everything and proves nothing")
	}
	seen := map[string]bool{}
	routed, notRouted := 0, 0
	for _, s := range connectionGateSites {
		key := s.file + "::" + s.fn
		if seen[key] {
			t.Errorf("%s is listed twice — a duplicate hides whichever entry is wrong", key)
		}
		seen[key] = true
		switch {
		case s.routed != "" && s.notRouted != "":
			t.Errorf("%s is marked BOTH routed (%q) and not routed (%q) — pick one", key, s.routed, s.notRouted)
		case s.routed == "" && s.notRouted == "":
			t.Errorf("%s is marked neither routed nor not-routed — an unclassified entry is a hole", key)
		case s.routed != "":
			routed++
		default:
			notRouted++
		}
		if s.lookups == "" {
			t.Errorf("%s does not say which connection it resolves", key)
		}
	}
	if routed == 0 {
		t.Error("not one entry claims to route through the gate — the list cannot be describing a tree in which B1 landed")
	}
	if notRouted == 0 {
		t.Error("every entry claims to route through the gate — that is not this codebase, so the list has stopped describing it")
	}
}

// TestConnectionGate_ListMatchesTheTree is the guard. It fails in both
// directions and names every disagreement rather than the first.
func TestConnectionGate_ListMatchesTheTree(t *testing.T) {
	found := discoverConnectionGateSites(t)

	listed := map[string]connectionGateSite{}
	for _, s := range connectionGateSites {
		listed[s.file+"::"+s.fn] = s
	}

	var unlisted, stale, undone, drifted, wrongLookups []string

	for key, site := range found {
		entry, ok := listed[key]
		if !ok {
			unlisted = append(unlisted, key+" (resolves "+joinHalves(site.lookups)+")")
			continue
		}
		if got := joinHalves(site.lookups); got != entry.lookups {
			wrongLookups = append(wrongLookups, key+": tree says it resolves "+got+", the list says "+entry.lookups)
		}
		gates := joinHalves(site.gates)
		if entry.routed != "" && gates != entry.routed {
			undone = append(undone, key+": listed as routed through "+entry.routed+", but the tree shows "+quoteOrNone(gates))
		}
		if entry.notRouted != "" && gates != "" {
			drifted = append(drifted, key+": listed as not needing the gate ("+entry.notRouted+"), but the tree shows it calling "+gates)
		}
	}
	for key := range listed {
		if _, ok := found[key]; !ok {
			stale = append(stale, key)
		}
	}

	report := func(label string, items []string, why string) {
		if len(items) == 0 {
			return
		}
		sort.Strings(items)
		t.Errorf("%s (%d):\n  %s\n\n%s", label, len(items), strings.Join(items, "\n  "), why)
	}

	report("functions that resolve the active connection but are NOT in connectionGateSites", unlisted,
		"Every one of these has to be classified. If the failure becomes words a person or a log can see, "+
			"route it through writeNoActiveGitConnection / writeNoActiveArgocdConnection (or the Unavailable "+
			"pair, or credsafe.ErrNoActiveGitConnection where the failure is returned) and add it as routed. "+
			"If the failure is swallowed or goes to writeServerError, add it as notRouted with that reason. "+
			"Do not delete the entry to make this pass.")

	report("entries in connectionGateSites that no longer exist in the tree", stale,
		"A stale entry is a hole: the list stops describing the tree, and the next real site can hide behind "+
			"the mismatch. Remove the entry, or fix the name if the function was renamed.")

	report("entries listed as routed whose function no longer calls a gate helper", undone,
		"The fix was removed under a name that still claims it is there. This is the exact regression B1 exists "+
			"to prevent: the raw error is back in a response body or a log line.")

	report("entries listed as NOT needing the gate that now call one", drifted,
		"The list has drifted away from the tree. Change the entry to routed with the halves it now covers.")

	report("entries whose recorded lookups disagree with the tree", wrongLookups,
		"A handler that started resolving the other half of the connection has an unclassified new failure path. "+
			"Update the entry after checking what the new path says when it fails.")
}

func quoteOrNone(s string) string {
	if s == "" {
		return "no gate call at all"
	}
	return s
}

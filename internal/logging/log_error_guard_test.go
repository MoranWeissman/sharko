package logging

// log_error_guard_test.go — the proof that no log line was missed (B9, B16).
//
// # What this guards
//
// B9 stopped errors' own words reaching the log, and it did it in ONE place:
// RedactHandler replaces any attribute whose VALUE is a Go error with
// credsafe.LogClass(err). That trade is only safe if something can prove,
// mechanically and forever, which log lines the sink can actually protect.
//
// It cannot protect all of them. The sink classifies by TYPE, so it needs a
// type to be left. A raw payload that has already been flattened to a string
// before it reaches slog —
//
//	slog.Error("argocd call failed", "body", string(respBody))
//
// — is just a string by then, and no handler can tell it apart from a cluster
// name. Those have to be removed at the call site, and this list is what makes
// sure a new one cannot quietly appear.
//
// # Why it is a LIST and not a count
//
// A count gets HAPPIER as the bug spreads: "at least N log lines are safe"
// still passes when somebody adds the N+1st that is not. So this is a list,
// every entry named by file, function, log message and the exact carriers the
// tree shows, and it fails in BOTH directions:
//
//   - a slog call that touches an error or an opaque payload and is not in the
//     list at all → a new line nobody classified;
//   - a listed entry that no longer exists in the tree → a stale entry, which
//     is a hole nobody asked for;
//   - a listed entry whose carriers have changed → the line is doing something
//     different from what the list says it does;
//   - an entry marked `sink` that carries something the sink cannot see → the
//     entry claims a protection that does not apply to it.
//
// # It refuses to pass vacuously
//
// Before comparing anything it asserts that the walk read the EXACT number of
// shipping Go files the tree has. Not a floor with room in it — a floor with
// room in it is a hole, and a discovery step that silently returned nothing
// would otherwise agree with any list at all, including an empty one.
//
// # How it finds a log line and a carrier (B16)
//
// By type, not by spelling. See log_guard_walk_test.go, which also says
// plainly what the walk still cannot see and where that class is closed
// instead.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// wantSweptGoFiles is the EXACT number of non-test .go files the type-checked
// walk reads across internal/, cmd/ and tests/.
//
// Exact, deliberately. The number's whole job is to prove the walk still
// reaches the tree; a "greater than 200" version of it would still pass while
// half the tree went unswept. Adding or deleting a Go file anywhere in those
// directories changes this number, and changing it by hand is a two-second
// edit that puts one person's eyes on the fact that the sweep grew.
//
// It is NOT the whole tree, and that difference matters. Those directories
// hold more non-test .go files than this — the walk type-checks the default
// build, so a file behind a //go:build tag the default build does not set is
// never loaded and no log line inside it can ever be judged by the list
// below. This constant says nothing at all about those files, and on its own
// it never would: a file the walk cannot reach was never in the count, so its
// arrival changes nothing here.
//
// They are accounted for instead in log_guard_unswept_test.go, which names
// every one of them, records the build constraint that keeps it out, writes
// down why it cannot reach a released artifact, and asserts that this number
// plus that list equals the number of non-test .go files the tree actually
// holds. A new file is therefore either read by the walk and classified in
// logGuardSites, or named there with a reason. There is no third place.
// 428 since BF12-2 added internal/credsafe/failurereason.go, the plain-English
// sibling of LogClass. That file logs nothing — it builds a sentence out of
// error types and sentinels and hands it back to its caller.
//
// 429 since BF13-0 added internal/addresscorpus/corpus.go, the reader for the
// written-down list of addresses and what the address rule says about each
// one. That file logs nothing either — it reads one file, checks it holds
// what it claims to hold, and returns either the rows or an error.
//
// 430 since BF14 added internal/credsafe/sourcelabel.go, which owns the one
// word Sharko publishes in place of a catalog source address. That file
// logs nothing either — it returns the fixed word, and nothing else, for
// every address.
const wantSweptGoFiles = 430

// logGuardSites is THE LIST. Every slog call in the swept directories that
// touches an error or an opaque payload appears here exactly once, keyed by
// file, function, log message and the exact carriers the tree shows.
//
// The carriers are part of the key on purpose. A line that starts carrying
// something new shows up as one stale entry plus one unlisted site — two loud
// failures — rather than as a silently-widened entry that still matches.
//
// verdict "sink": the line hands slog an error VALUE. RedactHandler replaces
// its words with credsafe.LogClass, which is built from types and sentinels
// only. Nothing at the call site has to remember anything.
//
// verdict "safe": the line hands slog something the sink cannot type-check —
// a flattened string, or an opaque []byte/[]string — and there is a written
// reason why that particular value is not provider text. Every one had to be
// read.
var logGuardSites = []logGuardSite{
	{file: "cmd/schema-gen/main.go", fn: "main", msg: "schema generation failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/ai/memory.go", fn: "load", msg: "failed to load agent memory", carriers: "ident:err", verdict: "sink"},
	{file: "internal/ai/memory.go", fn: "load", msg: "failed to parse agent memory", carriers: "ident:err", verdict: "sink"},
	{file: "internal/ai/memory.go", fn: "save", msg: "failed to marshal agent memory", carriers: "ident:err", verdict: "sink"},
	{file: "internal/ai/memory.go", fn: "save", msg: "failed to save agent memory", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/addon_secret_single.go", fn: "handleRefreshAddonValuesSecret", msg: "[secrets] could not check one addon-values secret", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/addon_secret_single.go", fn: "handleSyncAddonValuesSecret", msg: "[secrets] could not sync one addon-values secret", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/addons_write.go", fn: "handleAddAddon", msg: "smart-values pre-fetch failed; falling back to minimal stub", carriers: "ident:ferr", verdict: "sink"},
	{file: "internal/api/chart_repo_gate.go", fn: "writeChartRepoError", msg: "outbound fetch failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/cluster_secrets.go", fn: "handleListClusterSecrets", msg: "listing secrets in namespace", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_discover.go", fn: "handleTestCluster", msg: "[cluster-test] SearchSecrets failed", carriers: "call:credsafe.Sentence", verdict: "safe", reason: "internal/credsafe already reduced this to one of Sharko’s own fixed sentences before it reached slog, so there are no provider words left in it."},
	{file: "internal/api/clusters_discover.go", fn: "handleTestCluster", msg: "[cluster-test] failed", carriers: "call:credsafe.Sentence", verdict: "safe", reason: "internal/credsafe already reduced this to one of Sharko’s own fixed sentences before it reached slog, so there are no provider words left in it."},
	{file: "internal/api/clusters_discover.go", fn: "handleTestCluster", msg: "[cluster-test] failed to record observation", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_doctor.go", fn: "doctorCheckAddonSecretPaths", msg: "[doctor] building the addon-secret provider failed", carriers: "call:credsafe.Sentence", verdict: "safe", reason: "internal/credsafe already reduced this to one of Sharko’s own fixed sentences before it reached slog, so there are no provider words left in it."},
	{file: "internal/api/clusters_doctor.go", fn: "doctorCheckAddonSecretPaths", msg: "[doctor] parsing the addon catalog failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_doctor.go", fn: "doctorCheckAddonSecretPaths", msg: "[doctor] parsing the managed-clusters file failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_doctor.go", fn: "doctorCheckAddonSecretPaths", msg: "[doctor] reading the addon catalog from Git failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_doctor.go", fn: "doctorCheckAddonSecretPaths", msg: "[doctor] reading the managed-clusters file from Git failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_orphan_ownership.go", fn: "listSharkoOwnedSecretNames", msg: "[orphan] listing sharko-owned Secrets failed — degrading to empty", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_orphans.go", fn: "resolveOrphanRegistrations", msg: "list_argocd_clusters_for_orphan_registrations: degrading to empty", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_pending.go", fn: "resolvePendingRegistrations", msg: "list_open_prs_for_pending_registrations: degrading to empty", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_restart_sync.go", fn: "handleRestartAddonSync", msg: "restart-sync: could not read the application from ArgoCD", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_restart_sync.go", fn: "handleRestartAddonSync", msg: "restart-sync: terminate returned benign 'no operation' error; continuing to sync", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/clusters_write.go", fn: "handleRegisterCluster", msg: "failed to record verification observation during registration", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/connection_repair.go", fn: "repairAddonLabelsOnly", msg: "[connection-repair] the labels-only repair did not complete", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/init.go", fn: "runBootstrapSteps", msg: "failed to add repository to ArgoCD", carriers: "ident:addRepoErr", verdict: "sink"},
	{file: "internal/api/init.go", fn: "runInitOperation", msg: "failed to delete branch after merge", carriers: "ident:delErr", verdict: "sink"},
	{file: "internal/api/migration.go", fn: "CompleteMigrationHandoffOnMerge", msg: "error", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/migration.go", fn: "CompleteMigrationHandoffOnMerge", msg: "migration handoff", carriers: "ident:report.ApplicationSets", verdict: "safe", reason: "the slice holds ArgoCD ApplicationSet NAMES that the handoff retired. A resource name is the operator’s own, it is already visible in ArgoCD, and none of it comes from a credentials backend or a chart repository."},
	{file: "internal/api/router.go", fn: "NewServer", msg: "could not apply operator-supplied bootstrap admin password", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/router.go", fn: "ReinitializeFromConnection", msg: "[startup] no active connection", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/router.go", fn: "ReinitializeFromConnection", msg: "[startup] no credentials provider configured", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/router.go", fn: "ReinitializeFromConnection", msg: "failed to read default addons during hot-reload", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/router.go", fn: "writeJSON", msg: "error encoding response", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/router.go", fn: "writeServerError", msg: "server error", carriers: "ident:err", verdict: "sink"},
	{file: "internal/api/secrets_reconcile.go", fn: "handleCheckSecrets", msg: "[secrets] check-all could not run", carriers: "ident:err", verdict: "sink"},
	// BF8 folded the four per-verb transport lines (doGet, doPost, doPut,
	// doDelete) into one shared helper, so there is one entry here now
	// instead of four.
	{file: "internal/argocd/unreachable.go", fn: "unreachableCallError", msg: "argocd call got no answer", carriers: "ident:cause", verdict: "sink"},
	{file: "internal/auth/initial_admin.go", fn: "ensureInitialAdminK8s", msg: "namespace", carriers: "ident:err", verdict: "sink"},
	{file: "internal/auth/store.go", fn: "MaybeLogBootstrapCredential", msg: "bootstrap credential check skipped: cannot read secret", carriers: "ident:err", verdict: "sink"},
	{file: "internal/auth/store.go", fn: "MaybeLogBootstrapCredential", msg: "could not write sharko-initial-admin-secret; fall back to logs", carriers: "ident:err", verdict: "sink"},
	{file: "internal/auth/store.go", fn: "MaybeLogBootstrapCredential", msg: "failed to remove admin.initialPassword from secret after bootstrap log", carriers: "ident:updateErr", verdict: "sink"},
	{file: "internal/auth/store.go", fn: "NewStore", msg: "failed to load auth data from K8s, will retry on requests", carriers: "ident:err", verdict: "sink"},
	{file: "internal/auth/store.go", fn: "ValidateCredentials", msg: "failed to reload auth data", carriers: "ident:err", verdict: "sink"},
	{file: "internal/auth/user_tokens.go", fn: "GetUserGitHubToken", msg: "failed to reload auth data for token lookup", carriers: "ident:err", verdict: "sink"},
	{file: "internal/auth/user_tokens.go", fn: "HasUserGitHubToken", msg: "failed to reload auth data for has-token check", carriers: "ident:err", verdict: "sink"},
	{file: "internal/capabilities/aws.go", fn: "detect", msg: "[capabilities] AWS identity markers present but sts:GetCallerIdentity failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/capabilities/aws.go", fn: "detect", msg: "[capabilities] no AWS identity detected", carriers: "ident:err", verdict: "sink"},
	{file: "internal/capabilities/hub.go", fn: "detect", msg: "[capabilities] hub platform detection unavailable", carriers: "ident:err", verdict: "sink"},
	{file: "internal/catalog/freshness.go", fn: "fetchOne", msg: "[freshness] version fetch failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/catalog/freshness.go", fn: "refresh", msg: "[freshness] could not read the org's catalog", carriers: "ident:err", verdict: "sink"},
	{file: "internal/catalog/freshness.go", fn: "refresh", msg: "[freshness] engine pin check failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/catalog/loader.go", fn: "LoadBytesWithVerifierAndSource", msg: "catalog entry canonical serialization failed", carriers: "ident:cerr", verdict: "sink"},
	{file: "internal/catalog/loader.go", fn: "LoadBytesWithVerifierAndSource", msg: "catalog entry signature verification errored", carriers: "ident:verr", verdict: "sink"},
	{file: "internal/catalog/scorecard.go", fn: "Refresh", msg: "catalog: scorecard refresh failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/catalog/signing/verify.go", fn: "logFailure", msg: "catalog signature verification failed", carriers: "call:credsafe.PublicSourceLabel", verdict: "safe", reason: "internal/credsafe owns what may be said about a catalog source address, and the answer is always the fixed word “redacted” — the address is sensitive by type, PublicSourceLabel does not even take it any more, and nothing derived from it (no hash, no length, no partial) can come out of a function that never sees it."},
	{file: "internal/catalog/signing/verify.go", fn: "verifyEntity", msg: "catalog signature verified", carriers: "call:credsafe.PublicSourceLabel", verdict: "safe", reason: "internal/credsafe owns what may be said about a catalog source address, and the answer is always the fixed word “redacted” — the address is sensitive by type, PublicSourceLabel does not even take it any more, and nothing derived from it (no hash, no length, no partial) can come out of a function that never sees it."},
	{file: "internal/catalog/sources/fetcher.go", fn: "fetchOne", msg: "catalog source blocked by runtime SSRF guard", carriers: "ident:err", verdict: "sink"},
	{file: "internal/catalog/sources/fetcher.go", fn: "fetchOne", msg: "catalog source fetch failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/catalog/sources/fetcher.go", fn: "fetchOne", msg: "catalog source schema validation failed", carriers: "ident:validationErr", verdict: "sink"},
	{file: "internal/catalog/sources/fetcher.go", fn: "fetchOne", msg: "catalog source sidecar verification errored", carriers: "ident:verr", verdict: "sink"},
	{file: "internal/changelog/store.go", fn: "NewStore", msg: "could not load persisted change log", carriers: "ident:err", verdict: "sink"},
	{file: "internal/changelog/store.go", fn: "Record", msg: "could not persist after record", carriers: "ident:err", verdict: "sink"},
	{file: "internal/changelog/store.go", fn: "extractEntries", msg: "failed to unmarshal change log from state", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/check_pass.go", fn: "checkOnce", msg: "[clusterreconciler] check pass could not list the ArgoCD cluster secrets — nothing was compared", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/check_pass.go", fn: "checkOnce", msg: "[clusterreconciler] check pass could not read the desired state from git — nothing was compared", carriers: "ident:rdErr.Err", verdict: "sink"},
	{file: "internal/clusterreconciler/connection_drift_notice.go", fn: "noticeConnectionShapeDrift", msg: "[clusterreconciler] this cluster's ArgoCD connection has drifted from the shape Sharko writes — nothing was changed; a repair is a separate, asked-for action", carriers: "ident:problems", verdict: "safe", reason: "problems is a slice of ConnectionShapeProblem, a typed enum this package defines. Every value it can hold is written in source, so nothing on the Secret can reach this line."},
	{file: "internal/clusterreconciler/reconciler.go", fn: "applyV4AddonLabels", msg: "[clusterreconciler] applying addon labels from the cluster's assignment file — this is the step that makes enable and disable take effect", carriers: "ident:added ident:changed ident:removed", verdict: "safe", reason: "the three slices hold LABEL KEYS — addons.sharko.dev/<addon> — derived from addon names in the cluster's assignment file. Names, never values, and the operator needs to see which addon is being turned on or off."},
	{file: "internal/clusterreconciler/reconciler.go", fn: "clearRegistrationPending", msg: "[clusterreconciler] clearing registration-pending annotation failed — continuing", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "clearRegistrationPending", msg: "[clusterreconciler] re-reading registration-pending Secret before annotation clear failed — continuing", carriers: "ident:getErr", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "createOne", msg: "[clusterreconciler] Secret Create failed — skipping cluster", carriers: "ident:createErr", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "createOne", msg: "[clusterreconciler] building Secret payload failed — skipping cluster", carriers: "ident:buildErr", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "createOne", msg: "[clusterreconciler] pre-create Get failed — skipping cluster", carriers: "ident:getErr", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "deleteOne", msg: "[clusterreconciler] orphan Secret delete failed — continuing to next", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "pollOnce", msg: "[clusterreconciler] git read failed — aborting tick (no state mutated)", carriers: "ident:rdErr.Err", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "pollOnce", msg: "[clusterreconciler] managed-clusters file rejected — aborting tick (no state mutated)", carriers: "ident:rdErr.Err", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "reconcileDiff", msg: "[clusterreconciler] listing managed cluster Secrets failed — aborting tick", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "selfHealManagedCluster", msg: "[clusterreconciler] managed cluster self-heal failed — continuing to next cluster", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "selfHealManagedCluster", msg: "[clusterreconciler] managed cluster self-heal post-write verify read failed", carriers: "ident:getErr", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "syncConnectivityCheckLabel", msg: "[clusterreconciler] connectivity-check label sync failed — continuing to next cluster", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "syncSelfManaged", msg: "[clusterreconciler] label-fight pre-read failed — skipping this tick's fight check", carriers: "ident:getErr", verdict: "sink"},
	{file: "internal/clusterreconciler/reconciler.go", fn: "syncSelfManaged", msg: "[clusterreconciler] self-managed label sync failed — continuing to next cluster", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/repair.go", fn: "RepairOwnedConnectionSecret", msg: "[clusterreconciler] connection repair failed — nothing was written", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/revision.go", fn: "fetchComparedRevision", msg: "[clusterreconciler] couldn't get the branch head commit for this pass — compared revision will show empty", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/v4_assignments.go", fn: "readV4AddonLabels", msg: "[clusterreconciler] a v4 cluster assignment file was rejected — leaving that cluster's addon labels alone, other clusters still converge", carriers: "ident:parseErr", verdict: "sink"},
	{file: "internal/clusterreconciler/v4_assignments.go", fn: "readV4AddonLabels", msg: "[clusterreconciler] could not list the v4 cluster-addons/ folder — no addon labels derived this tick, and none will be changed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/clusterreconciler/v4_assignments.go", fn: "readV4AddonLabels", msg: "[clusterreconciler] could not read a v4 cluster assignment file — leaving that cluster's addon labels alone, other clusters still converge", carriers: "ident:readErr", verdict: "sink"},
	{file: "internal/config/connection_gitnative.go", fn: "mergeProviderFromEnv", msg: "partial provider block in connection git-native env (type missing), skipping provider merge", carriers: "ident:declaredFields", verdict: "safe", reason: "declaredFields holds FIELD NAMES appended from string literals a few lines above (“region”, “prefix”, “namespace”, “role_arn”). No environment value reaches it — only the fact that one was set — and naming which fields were half-configured is the whole point of the line."},
	{file: "internal/demo/setup.go", fn: "SetupDemoServer", msg: "demo: could not create admin user", carriers: "ident:err", verdict: "sink"},
	{file: "internal/demo/setup.go", fn: "SetupDemoServer", msg: "demo: could not create qa user", carriers: "ident:err", verdict: "sink"},
	{file: "internal/demo/setup.go", fn: "SetupDemoServer", msg: "demo: could not seed observation", carriers: "ident:seedErr", verdict: "sink"},
	{file: "internal/demo/setup.go", fn: "SetupDemoServer", msg: "demo: could not seed tracked PR", carriers: "ident:trackErr", verdict: "sink"},
	{file: "internal/gitprovider/azuredevops_impl.go", fn: "MergePullRequest", msg: "azure devops: auto-approve failed (may need manual approval)", carriers: "ident:voteErr", verdict: "sink"},
	{file: "internal/gitprovider/gitea.go", fn: "MergePullRequest", msg: "gitea merge: get PR failed, retrying", carriers: "ident:err", verdict: "sink"},
	{file: "internal/gitprovider/github.go", fn: "GetFileContent", msg: "github get file content failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/gitprovider/github.go", fn: "ListPullRequests", msg: "github list pull requests failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/gitprovider/github_write.go", fn: "CreateBranch", msg: "github base branch unavailable, initializing empty repo", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/checker.go", fn: "check", msg: "notification check failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/service_provider.go", fn: "GetVersionInfo", msg: "could not fetch latest version", carriers: "ident:fetchErr", verdict: "sink"},
	{file: "internal/notifications/service_provider.go", fn: "GetVersionInfo", msg: "could not load addon catalog, skipping helm version check", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/service_provider.go", fn: "GetVersionInfo", msg: "no active argocd client, skipping check", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/service_provider.go", fn: "GetVersionInfo", msg: "no active git provider, skipping check", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/store.go", fn: "Add", msg: "could not persist after add", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/store.go", fn: "MarkAllRead", msg: "could not persist after mark all read", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/store.go", fn: "MarkRead", msg: "could not persist after mark read", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/store.go", fn: "NewStore", msg: "could not load persisted notifications", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/store.go", fn: "Resolve", msg: "could not persist after resolve", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/store.go", fn: "extractNotifications", msg: "failed to unmarshal notifications from state", carriers: "ident:err", verdict: "sink"},
	{file: "internal/notifications/store.go", fn: "loadFromCMStore", msg: "could not save the notifications that were kept — the dropped ones are still stored and will be dropped again on the next start", carriers: "ident:err", verdict: "sink"},
	{file: "internal/observations/store.go", fn: "ListObservations", msg: "skipping malformed observation", carriers: "ident:err", verdict: "sink"},
	{file: "internal/orchestrator/addon_ops.go", fn: "DisableAddon", msg: "could not fetch credentials for remote secret cleanup", carriers: "ident:credErr", verdict: "sink"},
	{file: "internal/orchestrator/addon_ops.go", fn: "DisableAddon", msg: "failed to disable addon label in managed-clusters.yaml — aborting before PR (no half-write)", carriers: "ident:labelErr", verdict: "sink"},
	{file: "internal/orchestrator/addon_ops.go", fn: "EnableAddon", msg: "could not fetch credentials for remote secret creation", carriers: "ident:credErr", verdict: "sink"},
	{file: "internal/orchestrator/addon_ops.go", fn: "EnableAddon", msg: "failed to enable addon label in managed-clusters.yaml — aborting before PR (no half-write)", carriers: "ident:labelErr", verdict: "sink"},
	{file: "internal/orchestrator/adopt.go", fn: "AdoptClusters", msg: "adopt: no credentials available — skipping connectivity verification", carriers: "ident:credErr", verdict: "sink"},
	{file: "internal/orchestrator/adopt.go", fn: "AdoptClusters", msg: "could not check cluster secret for foreign ArgoCD ownership — proceeding with adoption", carriers: "ident:warnErr", verdict: "sink"},
	{file: "internal/orchestrator/adopt.go", fn: "AdoptClusters", msg: "could not read managed-by label — refusing to adopt this cluster", carriers: "ident:labelErr", verdict: "sink"},
	{file: "internal/orchestrator/adopt.go", fn: "adoptSingleCluster", msg: "failed to add cluster entry", carriers: "ident:addEntryErr", verdict: "sink"},
	{file: "internal/orchestrator/adopt.go", fn: "adoptSingleCluster", msg: "failed to set adopted annotation on ArgoCD secret — reconciler will retry", carriers: "ident:annotErr", verdict: "sink"},
	{file: "internal/orchestrator/adopt_v4.go", fn: "adoptSingleClusterV4", msg: "failed to set adopted annotation on ArgoCD secret — reconciler will retry", carriers: "ident:annotErr", verdict: "sink"},
	{file: "internal/orchestrator/adopt_v4.go", fn: "adoptSingleClusterV4", msg: "failed to take ownership of the ArgoCD cluster secret during v4 adoption", carriers: "ident:swapErr", verdict: "sink"},
	{file: "internal/orchestrator/ai_annotate.go", fn: "AnnotateValues", msg: "ai annotate fell through to heuristic-only", carriers: "ident:err", verdict: "sink"},
	{file: "internal/orchestrator/ai_annotate.go", fn: "AnnotateValues", msg: "ai annotate response parse failed; using heuristic only", carriers: "ident:parseErr", verdict: "sink"},
	{file: "internal/orchestrator/cluster.go", fn: "RegisterCluster", msg: "RegisterCluster: PR opened but auto-merge failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/orchestrator/cluster.go", fn: "RegisterCluster", msg: "RegisterCluster: could not read the cluster registry — refusing to rewrite it from scratch", carriers: "ident:clusterAddonsErr", verdict: "sink"},
	{file: "internal/orchestrator/cluster.go", fn: "RegisterCluster", msg: "RegisterCluster: direct ArgoCD cluster Secret write failed (continuing — Git + reconciler can still converge)", carriers: "ident:ensureErr", verdict: "sink"},
	{file: "internal/orchestrator/cluster.go", fn: "RegisterCluster", msg: "RegisterCluster: git commit failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/orchestrator/cluster.go", fn: "RegisterCluster", msg: "could not check cluster secret for foreign ArgoCD ownership — proceeding with registration", carriers: "ident:warnErr", verdict: "sink"},
	{file: "internal/orchestrator/cluster.go", fn: "RegisterCluster", msg: "failed to add cluster entry to cluster-addons.yaml — continuing with values file only", carriers: "ident:addEntryErr", verdict: "sink"},
	{file: "internal/orchestrator/git_helpers.go", fn: "commitChangesWithMeta", msg: "failed to delete branch after merge", carriers: "ident:delErr", verdict: "sink"},
	{file: "internal/orchestrator/remove.go", fn: "RemoveCluster", msg: "could not confirm Sharko's ownership label on the ArgoCD cluster Secret — refusing to delete it", carriers: "ident:labelErr", verdict: "sink"},
	{file: "internal/orchestrator/remove.go", fn: "RemoveCluster", msg: "could not fetch credentials for remote secret cleanup", carriers: "ident:credErr", verdict: "sink"},
	{file: "internal/orchestrator/remove.go", fn: "RemoveCluster", msg: "could not strip Sharko's ownership label from the user's ArgoCD cluster Secret during removal — no reconcile tick can do it now that the git entry is gone; remove the app.kubernetes.io/managed-by label by hand or the orphan sweep may delete the Secret", carriers: "ident:stripErr", verdict: "sink"},
	{file: "internal/orchestrator/remove.go", fn: "RemoveCluster", msg: "failed to delete ArgoCD cluster during removal", carriers: "ident:delErr", verdict: "sink"},
	{file: "internal/orchestrator/remove.go", fn: "RemoveCluster", msg: "failed to remove cluster entry from managed-clusters.yaml", carriers: "ident:removeErr", verdict: "sink"},
	{file: "internal/orchestrator/secrets.go", fn: "createAddonSecrets", msg: "[secrets] failed to create secret, continuing", carriers: "call:credsafe.Sentence", verdict: "safe", reason: "internal/credsafe already reduced this to one of Sharko’s own fixed sentences before it reached slog, so there are no provider words left in it."},
	{file: "internal/orchestrator/secrets.go", fn: "deleteAddonSecrets", msg: "failed to delete secret", carriers: "ident:delErr", verdict: "sink"},
	{file: "internal/orchestrator/secrets.go", fn: "deleteAllAddonSecrets", msg: "failed to delete secret", carriers: "ident:delErr", verdict: "sink"},
	{file: "internal/orchestrator/unadopt.go", fn: "UnadoptCluster", msg: "could not fetch credentials for remote secret cleanup", carriers: "ident:credErr", verdict: "sink"},
	{file: "internal/orchestrator/unadopt.go", fn: "UnadoptCluster", msg: "failed to remove cluster entry from managed-clusters.yaml", carriers: "ident:removeErr", verdict: "sink"},
	{file: "internal/providers/aws_sm.go", fn: "getCredentialsWithRoleARN", msg: "[provider] GetCredentials failed", carriers: "ident:tried", verdict: "safe", reason: "tried holds the Secrets Manager secret NAMES this lookup attempted, built from the cluster name and the operator’s own configured prefix. They are addresses the operator wrote, never secret values, and an operator who cannot see which names were tried cannot fix the path."},
	{file: "internal/prtracker/tracker.go", fn: "PollOnce", msg: "[prtracker] failed to delete branch after observed merge", carriers: "ident:delErr", verdict: "sink"},
	{file: "internal/prtracker/tracker.go", fn: "PollOnce", msg: "[prtracker] failed to poll PR", carriers: "ident:err", verdict: "sink"},
	{file: "internal/prtracker/tracker.go", fn: "PollOnce", msg: "[prtracker] failed to read state", carriers: "ident:err", verdict: "sink"},
	{file: "internal/prtracker/tracker.go", fn: "PollOnce", msg: "[prtracker] failed to write state", carriers: "ident:err", verdict: "sink"},
	{file: "internal/prtracker/tracker.go", fn: "PollSinglePR", msg: "[prtracker] failed to delete branch after observed merge", carriers: "ident:delErr", verdict: "sink"},
	{file: "internal/prtracker/tracker.go", fn: "extractPRs", msg: "[prtracker] failed to unmarshal PRs from state", carriers: "ident:err", verdict: "sink"},
	{file: "internal/remediation/remediation.go", fn: "OnMerge", msg: "remediation: failed to list ArgoCD applications", carriers: "ident:err", verdict: "sink"},
	{file: "internal/remediation/remediation.go", fn: "OnMergeRefresh", msg: "remediation: failed to refresh addon app after PR merge", carriers: "ident:err", verdict: "sink"},
	{file: "internal/remediation/remediation.go", fn: "OnMergeRefresh", msg: "remediation: failed to refresh bootstrap app after PR merge", carriers: "ident:lastErr", verdict: "sink"},
	{file: "internal/remediation/remediation.go", fn: "act", msg: "remediation: re-sync failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/remediation/remediation.go", fn: "act", msg: "remediation: terminate operation failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/remediation/remediation.go", fn: "act", msg: "remediation: terminate returned benign 'no operation' error; proceeding to sync", carriers: "ident:err", verdict: "sink"},
	{file: "internal/schema/validator.go", fn: "LogValidationFailure", msg: "schema_validation_failed", carriers: "ident:failure.Violations", verdict: "safe", reason: "Violations holds one “<json pointer>: <message>” line per schema violation. The pointer is a location in the operator’s own file and the message is the JSON Schema library’s own wording about the shape — neither quotes the offending VALUE, so a repository address with a token in it cannot arrive here. Reviewed by hand as part of B16."},
	{file: "internal/secrets/orphans.go", fn: "scanOrphans", msg: "[secrets] orphan scan: could not connect — keeping this cluster's previous leftover-secret records", carriers: "ident:err", verdict: "sink"},
	{file: "internal/secrets/orphans.go", fn: "scanOrphans", msg: "[secrets] orphan scan: could not get credentials — keeping this cluster's previous leftover-secret records", carriers: "call:credsafe.Sentence", verdict: "safe", reason: "internal/credsafe already reduced this to one of Sharko's own fixed sentences before it reached slog, so there are no provider words left in it."},
	{file: "internal/secrets/orphans.go", fn: "scanOrphans", msg: "[secrets] orphan scan: could not list secrets — keeping this cluster's previous leftover-secret records", carriers: "ident:err", verdict: "sink"},
	{file: "internal/secrets/reconciler.go", fn: "CheckAll", msg: "[secrets] could not check this addon-values secret — carrying on with the rest", carriers: "ident:checkErr", verdict: "sink"},
	{file: "internal/secrets/reconciler.go", fn: "reconcile", msg: "[secrets] could not work out which secrets to push — nothing was pushed this run", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/addon.go", fn: "GetAddonDetail", msg: "could not fetch applicationset status", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/addon.go", fn: "GetAddonValuesAndSchema", msg: "ignoring unparseable values schema", carriers: "ident:perr", verdict: "sink"},
	{file: "internal/service/addon.go", fn: "getCatalogUncached", msg: "could not fetch argocd applications", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/addon.go", fn: "getCatalogV4", msg: "could not fetch argocd applications", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/addon.go", fn: "getVersionMatrixUncached", msg: "could not fetch argocd applications", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/addon.go", fn: "getVersionMatrixV4", msg: "could not fetch argocd applications", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetClusterAddonValues", msg: "could not parse cluster overrides file", carriers: "ident:uerr", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetClusterAddonValues", msg: "ignoring unparseable values schema", carriers: "ident:jerr", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetClusterComparison", msg: "could not fetch argocd apps", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetClusterComparison", msg: "could not fetch argocd clusters for server url", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetClusterComparison", msg: "could not fetch argocd connection info", carriers: "ident:connErr", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetClusterDetail", msg: "could not fetch argocd apps for cluster", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetClusterDetail", msg: "could not fetch argocd clusters for server url", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetConfigDiff", msg: "could not marshal cluster values for addon", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "GetConfigDiff", msg: "no global defaults found for addon", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "getClusterValuesV4", msg: "could not parse cluster values file for addon", carriers: "ident:uErr", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "getClusterValuesV4", msg: "could not read cluster values file for addon", carriers: "ident:rErr", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "getConfigDiffV4", msg: "could not read cluster values for addon", carriers: "ident:cErr", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "getConfigDiffV4", msg: "no global defaults found for addon", carriers: "ident:gErr", verdict: "sink"},
	{file: "internal/service/cluster.go", fn: "listClustersUncached", msg: "could not fetch argocd clusters", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/connection.go", fn: "List", msg: "connection skipped: required fields empty (legacy store entry)", carriers: "ident:missing", verdict: "safe", reason: "missing holds FIELD NAMES from missingRequiredConnectionFields, which appends string literals. No stored connection value reaches it, and naming the empty fields is what tells the operator which legacy record to repair."},
	{file: "internal/service/dashboard.go", fn: "getStatsUncached", msg: "could not fetch argocd applications for dashboard", carriers: "ident:appsErr", verdict: "sink"},
	{file: "internal/service/dashboard.go", fn: "getStatsUncached", msg: "could not fetch argocd clusters for dashboard", carriers: "ident:clustersErr", verdict: "sink"},
	{file: "internal/service/dashboard.go", fn: "getStatsUncached", msg: "could not fetch bootstrap app status for dashboard", carriers: "ident:bootstrapErr", verdict: "sink"},
	{file: "internal/service/observability.go", fn: "getOverviewUncached", msg: "could not fetch argocd version", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/observability.go", fn: "getOverviewUncached", msg: "could not fetch git clusters for configured count", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "CheckUpgrade", msg: "conflict check failed for global values", carriers: "ident:conflictErr", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "CheckUpgrade", msg: "could not fetch global values", carriers: "ident:globalErr", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "CheckUpgrade", msg: "current version not available in Helm repo, searching for fallback", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "CheckUpgrade", msg: "fallback version fetch also failed", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "checkClusterConflictsV3", msg: "conflict check failed for cluster", carriers: "ident:conflictErr", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "checkClusterConflictsV3", msg: "could not fetch cluster addons config", carriers: "ident:clusterErr", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "checkClusterConflictsV3", msg: "could not parse cluster addons", carriers: "ident:parseErr", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "checkClusterConflictsV4", msg: "conflict check failed for cluster", carriers: "ident:conflictErr", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "checkClusterConflictsV4", msg: "could not list cluster-addons/*.yaml", carriers: "ident:err", verdict: "sink"},
	{file: "internal/service/upgrade.go", fn: "fetchAdvisoryMap", msg: "advisories fetch failed, proceeding without security data", carriers: "ident:err", verdict: "sink"},
	{file: "internal/settings/store.go", fn: "GetManagedSecretsSettings", msg: "managed secrets settings: settings read failed, serving last-known values from cache", carriers: "ident:err", verdict: "sink"},
	{file: "internal/settings/store.go", fn: "IsAPITest", msg: "probe_mode: settings read failed, serving last-known value from cache", carriers: "ident:err", verdict: "sink"},
	{file: "internal/settings/store.go", fn: "IsAddonValuesEngineEnabled", msg: "addon_values_engine_enabled: settings read failed, serving last-known value from cache", carriers: "ident:err", verdict: "sink"},
	{file: "internal/settings/store.go", fn: "IsInlineCredentialsAllowed", msg: "allow_inline_credentials: settings read failed, serving last-known value from cache", carriers: "ident:err", verdict: "sink"},
	{file: "internal/settings/store.go", fn: "IsManagedClusterSelfHealEnabled", msg: "managed_cluster_self_heal: settings read failed, serving last-known value from cache", carriers: "ident:err", verdict: "sink"},
	{file: "tests/e2e/harness/gitfake/cmd/gitfake-server/main.go", fn: "main", msg: "gitfake-server: graceful shutdown error", carriers: "ident:err", verdict: "sink"},
	{file: "tests/e2e/harness/gitfake/cmd/gitfake-server/main.go", fn: "main", msg: "gitfake-server: init gitfake failed", carriers: "ident:err", verdict: "sink"},
	{file: "tests/e2e/harness/gitfake/cmd/gitfake-server/main.go", fn: "main", msg: "gitfake-server: listener failed", carriers: "ident:err", verdict: "sink"},
	{file: "tests/e2e/harness/gitfake/cmd/gitfake-server/main.go", fn: "main", msg: "gitfake-server: seed failed", carriers: "ident:err", verdict: "sink"},
}

// logGuardRepoRoot walks up from the test's working directory until it finds
// go.mod.
func logGuardRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find the repository root (no go.mod above the test's working directory) — the guard would sweep nothing")
	return ""
}

// discoverLogSites type-checks the tree and reports every slog call that
// touches an error or an opaque payload, keyed the same way the list is.
func discoverLogSites(t *testing.T) map[string]discoveredLogSite {
	t.Helper()
	root := logGuardRepoRoot(t)
	for _, dir := range logGuardSweepDirs {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			t.Fatalf("swept directory %q does not exist — the guard silently covers less than it claims: %v", dir, err)
		}
	}

	pkgs := loadTypedPackages(t, root, "./...")
	found := map[string]discoveredLogSite{}
	filesRead := walkSlogCalls(pkgs, root, func(file, funcName, msg string, site discoveredLogSite) {
		found[file+"::"+funcName+"::"+msg+"::"+joinSet(site.carriers)] = site
	})

	if filesRead != wantSweptGoFiles {
		t.Fatalf(`the walk read %d shipping Go files, want exactly %d.

Fewer means it is not reaching the tree any more, and nothing it reports can be
trusted. More means the tree grew — update wantSweptGoFiles once you have
looked at whether the new files log anything.`, filesRead, wantSweptGoFiles)
	}
	return found
}

// inSweptDir keeps the walk's remit to the three directories this guard
// claims to cover, so a package added elsewhere in the module cannot quietly
// widen or narrow what the list is compared against.
func inSweptDir(relPath string) bool {
	for _, dir := range logGuardSweepDirs {
		if strings.HasPrefix(relPath, dir+"/") {
			return true
		}
	}
	return false
}

// TestLogErrorGuard_TheListIsWellFormed pins the list itself before it is used
// to judge anything.
func TestLogErrorGuard_TheListIsWellFormed(t *testing.T) {
	if len(logGuardSites) == 0 {
		t.Fatal("logGuardSites is empty — an empty list agrees with everything and proves nothing")
	}
	seen := map[string]bool{}
	sink, safe := 0, 0
	for _, s := range logGuardSites {
		key := s.file + "::" + s.fn + "::" + s.msg + "::" + s.carriers
		if seen[key] {
			t.Errorf("%s is listed twice — a duplicate hides whichever entry is wrong", key)
		}
		seen[key] = true
		switch s.verdict {
		case "sink":
			sink++
			if s.reason != "" {
				t.Errorf("%s is marked sink but carries a reason (%q) — sink needs no reason, and a reason there reads as an excuse for something else", key, s.reason)
			}
		case "safe":
			safe++
			if strings.TrimSpace(s.reason) == "" {
				t.Errorf("%s is marked safe with no reason. A reason in words is the whole cost of the escape hatch; without one this is an unexplained hole.", key)
			}
		default:
			t.Errorf("%s has verdict %q — it must be exactly \"sink\" or \"safe\"", key, s.verdict)
		}
		if s.carriers == "" {
			t.Errorf("%s lists no carriers, so it should not be in this list at all", key)
		}
	}
	if sink == 0 {
		t.Error("not one entry is protected by the log sink — the list cannot be describing a tree in which B9 landed")
	}
	if safe == 0 {
		t.Error("every entry claims the sink protects it — that is not this codebase, so the list has stopped describing it")
	}
}

// TestLogErrorGuard_ListMatchesTheTree is the guard. It fails in both
// directions and names every disagreement rather than the first.
func TestLogErrorGuard_ListMatchesTheTree(t *testing.T) {
	found := discoverLogSites(t)

	listed := map[string]logGuardSite{}
	for _, s := range logGuardSites {
		listed[s.file+"::"+s.fn+"::"+s.msg+"::"+s.carriers] = s
	}

	if len(found) != len(logGuardSites) {
		t.Errorf("the walk found %d slog sites carrying a raw value; the list has %d entries. The named disagreements below say which.", len(found), len(logGuardSites))
	}

	var unlisted, stale, overclaimed []string

	for key, site := range found {
		entry, ok := listed[key]
		if !ok {
			unlisted = append(unlisted, key)
			continue
		}
		// An entry may only claim the sink protects it when every carrier is
		// an error value. A string the sink cannot type-check needs the
		// written-reason escape hatch instead.
		if entry.verdict == "sink" && (site.kinds[carrierRawString] || site.kinds[carrierSafeCall]) {
			overclaimed = append(overclaimed, key+" (carries "+joinSet(site.kinds)+")")
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

	report("slog calls that touch an error or an opaque payload but are NOT in logGuardSites", unlisted,
		"Every one of these has to be classified. If it hands slog an error VALUE, the sink replaces the "+
			"words with credsafe.LogClass and the entry is verdict \"sink\". If it hands slog a string or an "+
			"opaque payload — a response body, err.Error(), an fmt.Sprintf of either, a []string of a "+
			"library's own messages — the sink cannot help, and the right move is to stop logging that value, "+
			"not to add a \"safe\" entry for it. Do not delete the entry to make this pass.")

	report("entries in logGuardSites that no longer match anything in the tree", stale,
		"A stale entry is a hole: the list stops describing the tree, and the next real site can hide behind "+
			"the mismatch. If the line moved or its carriers changed, update the entry — the carriers field is "+
			"part of the key on purpose, so a line that starts carrying something new shows up as one stale "+
			"entry plus one unlisted site rather than passing quietly.")

	report("entries claiming the log sink protects them when it cannot", overclaimed,
		"RedactHandler classifies by TYPE. A value that is already a string when it reaches slog has no type "+
			"left to classify, so \"sink\" is a claim that does not apply. Either stop logging the value or "+
			"mark the entry \"safe\" and write down why the string is Sharko's own words.")
}

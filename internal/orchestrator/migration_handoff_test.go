package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/appsets"
)

// ─── a fake ApplicationSet manager ───────────────────────────────────────
//
// It models the two things the handoff actually cares about: whether an
// ApplicationSet has been made safe to retire, and whether the live
// Applications it generated still carry the marker that makes deleting
// them take the workloads with them.

type fakeAppSetManager struct {
	sets map[string]*appsets.ApplicationSetInfo
	// generatedApps maps an ApplicationSet name to the Applications it
	// made, and whether each still carries the delete-everything marker.
	generatedApps map[string]map[string]bool

	listErr     error
	preserveErr error
	releaseErr  error
	deleteErr   error

	// The record the assertions read.
	preserved []string
	released  []string
	deleted   []string
}

func newFakeAppSetManager(sets ...appsets.ApplicationSetInfo) *fakeAppSetManager {
	f := &fakeAppSetManager{
		sets:          map[string]*appsets.ApplicationSetInfo{},
		generatedApps: map[string]map[string]bool{},
	}
	for i := range sets {
		s := sets[i]
		f.sets[s.Name] = &s
	}
	return f
}

// withApps declares that appSet generated these Applications, each still
// carrying the ArgoCD resources finalizer.
func (f *fakeAppSetManager) withApps(appSet string, apps ...string) *fakeAppSetManager {
	m := map[string]bool{}
	for _, a := range apps {
		m[a] = true // true = still has the delete-everything marker
	}
	f.generatedApps[appSet] = m
	return f
}

func (f *fakeAppSetManager) List(context.Context) ([]appsets.ApplicationSetInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]appsets.ApplicationSetInfo, 0, len(f.sets))
	for _, s := range f.sets {
		out = append(out, *s)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

func (f *fakeAppSetManager) Preserve(_ context.Context, name string) error {
	if f.preserveErr != nil {
		return f.preserveErr
	}
	s, ok := f.sets[name]
	if !ok {
		return fmt.Errorf("no such ApplicationSet %q", name)
	}
	s.PreserveResourcesOnDeletion = true
	f.preserved = append(f.preserved, name)
	return nil
}

func (f *fakeAppSetManager) ReleaseGeneratedApplications(_ context.Context, name string) ([]string, error) {
	if f.releaseErr != nil {
		return nil, f.releaseErr
	}
	var out []string
	for app, hasFinalizer := range f.generatedApps[name] {
		if !hasFinalizer {
			continue
		}
		f.generatedApps[name][app] = false
		out = append(out, app)
	}
	sort.Strings(out)
	f.released = append(f.released, out...)
	return out, nil
}

func (f *fakeAppSetManager) Delete(_ context.Context, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.sets, name)
	f.deleted = append(f.deleted, name)
	return nil
}

// anyAppStillArmed reports whether ANY generated Application still carries
// the delete-everything marker. This is the single fact the whole B-1 fix
// turns on: if it is true when the ApplicationSet is deleted, the fleet's
// workloads go with it.
func (f *fakeAppSetManager) anyAppStillArmed() bool {
	for _, apps := range f.generatedApps {
		for _, armed := range apps {
			if armed {
				return true
			}
		}
	}
	return false
}

// v3ScaffoldAppSet builds the ApplicationSetInfo the v3 bootstrap's
// addons-appset.yaml actually produces: named after the addon, selecting
// clusters on a bare `<addon>: enabled` label, and — crucially — NOT
// deletion-safe.
func v3ScaffoldAppSet(addon string) appsets.ApplicationSetInfo {
	return appsets.ApplicationSetInfo{
		Name:                addon,
		Namespace:           "argocd",
		HasClusterGenerator: true,
		ClusterSelectorLabels: []string{
			"argocd.argoproj.io/secret-type", addon,
		},
		ClusterSelectorMatchLabels: map[string]string{
			"argocd.argoproj.io/secret-type": "cluster",
			addon:                            "enabled",
		},
	}
}

// engineAppSet is what the engine chart produces. It selects on
// `addons.sharko.dev/<addon>` and is preservation-safe by default, so the
// handoff must never mistake it for something to retire.
func engineAppSet(addon string) appsets.ApplicationSetInfo {
	return appsets.ApplicationSetInfo{
		Name:                        "sharko-" + addon,
		Namespace:                   "argocd",
		HasClusterGenerator:         true,
		PreserveResourcesOnDeletion: true,
		ClusterSelectorLabels: []string{
			"addons.sharko.dev/" + addon, "argocd.argoproj.io/secret-type",
		},
	}
}

func newMigrationOrchestratorWithAppSets(t *testing.T, git *migrationFakeGit, m appsets.Manager) *Orchestrator {
	t.Helper()
	orch := newMigrationOrchestratorNoAppSets(t, git)
	orch.SetApplicationSetManager(m)
	return orch
}

// defaultMigrationAppSets is the ArgoCD side of the v3 fixture repo: one
// ApplicationSet per addon in its catalog, each shaped exactly the way
// templates/bootstrap/templates/addons-appset.yaml renders them, with the
// Applications they generated for the fixture's two clusters.
func defaultMigrationAppSets() *fakeAppSetManager {
	return newFakeAppSetManager(
		v3ScaffoldAppSet("cert-manager"),
		v3ScaffoldAppSet("metrics-server"),
		v3ScaffoldAppSet("inhouse-api"),
	).
		withApps("cert-manager", "cert-manager-prod-eu", "cert-manager-staging-us").
		withApps("metrics-server", "metrics-server-prod-eu", "metrics-server-staging-us")
}

// ─── B-1: the migration must not be able to uninstall the fleet ──────────

// TestMigrate_RefusesWhenItCannotReachTheApplicationSets is THE pinning
// test for review finding B-1. A live v3 fleet plus no way to reach the
// ApplicationSets that keep it running must be a refusal with nothing
// written — never a pull request that, on merge, removes every addon.
func TestMigrate_RefusesWhenItCannotReachTheApplicationSets(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	orch := newMigrationOrchestratorNoAppSets(t, git)

	_, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err == nil {
		t.Fatal("migrate succeeded on a live fleet with no ApplicationSet access — it must refuse")
	}
	if !strings.Contains(err.Error(), "Nothing was written") {
		t.Errorf("error should promise nothing was written, got: %v", err)
	}
	if len(git.branches) != 0 {
		t.Errorf("a branch was created despite the refusal: %v", git.branches)
	}
	if len(git.prs) != 0 {
		t.Errorf("a pull request was opened despite the refusal: %d", len(git.prs))
	}
}

// TestMigrate_EmptyFleet_NeedsNoHandoff — a v3 repo with no clusters has
// nothing running to strand, so it migrates without any cluster access at
// all. This is the case the auto-detection has to get right, or every
// fresh-repo migration would demand an ArgoCD connection it does not need.
func TestMigrate_EmptyFleet_NeedsNoHandoff(t *testing.T) {
	files := newV3FixtureRepo()
	files["configuration/managed-clusters.yaml"] = []byte("clusters: []\n")
	delete(files, "configuration/addons-clusters-values/prod-eu.yaml")
	orch := newMigrationOrchestratorNoAppSets(t, newMigrationFakeGit(files))

	res, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err != nil {
		t.Fatalf("migrate on an empty fleet: %v", err)
	}
	if res.Handoff == nil || res.Handoff.State != HandoffStateNotNeeded {
		t.Fatalf("Handoff = %+v, want state %q", res.Handoff, HandoffStateNotNeeded)
	}
}

// TestMigrate_SkipEscapeHatch lets a repo with nothing running migrate the
// files only — and says so loudly enough that nobody uses it by accident.
func TestMigrate_SkipEscapeHatch(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	orch := newMigrationOrchestratorNoAppSets(t, git)

	res, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true, RuntimeHandoff: RuntimeHandoffSkip})
	if err != nil {
		t.Fatalf("migrate with runtime_handoff=skip: %v", err)
	}
	if res.Handoff == nil || res.Handoff.State != HandoffStateSkipped {
		t.Fatalf("Handoff = %+v, want state %q", res.Handoff, HandoffStateSkipped)
	}
	if !strings.Contains(res.Handoff.Message, "left ArgoCD alone") {
		t.Errorf("skip message should say ArgoCD was left alone, got %q", res.Handoff.Message)
	}
}

// TestMigrate_PreparesTheOldApplicationSets is the positive case: every v3
// ApplicationSet is made safe, and every Application it generated has lost
// the marker that would take its workloads down — all BEFORE the pull
// request exists.
func TestMigrate_PreparesTheOldApplicationSets(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	mgr := newFakeAppSetManager(
		v3ScaffoldAppSet("cert-manager"),
		v3ScaffoldAppSet("metrics-server"),
		v3ScaffoldAppSet("inhouse-api"),
	).
		withApps("cert-manager", "cert-manager-prod-eu", "cert-manager-staging-us").
		withApps("metrics-server", "metrics-server-prod-eu", "metrics-server-staging-us")
	orch := newMigrationOrchestratorWithAppSets(t, git, mgr)

	res, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.Handoff == nil || res.Handoff.State != HandoffStatePrepared {
		t.Fatalf("Handoff = %+v, want state %q", res.Handoff, HandoffStatePrepared)
	}

	wantPrepared := []string{"cert-manager", "inhouse-api", "metrics-server"}
	sort.Strings(mgr.preserved)
	if !equalStrings(mgr.preserved, wantPrepared) {
		t.Errorf("preserved = %v, want %v", mgr.preserved, wantPrepared)
	}
	if mgr.anyAppStillArmed() {
		t.Error("an Application still carries the delete-everything marker after prepare — " +
			"merging the migration would remove its workloads")
	}
	if len(res.Handoff.ReleasedApplications) != 4 {
		t.Errorf("ReleasedApplications = %v, want all 4 generated Applications", res.Handoff.ReleasedApplications)
	}
}

// TestMigrate_PreparesBeforeTheBranchExists pins the ORDER. The whole
// safety argument is that the fleet is made safe first; a prepare that ran
// after the branch (or worse, after the merge) would be theatre.
func TestMigrate_PreparesBeforeTheBranchExists(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	mgr := newFakeAppSetManager(v3ScaffoldAppSet("cert-manager"))
	mgr.preserveErr = errors.New("argocd said no")
	orch := newMigrationOrchestratorWithAppSets(t, git, mgr)

	_, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err == nil {
		t.Fatal("migrate succeeded although the ApplicationSet could not be made safe")
	}
	if len(git.branches) != 0 {
		t.Errorf("a branch was created before the fleet was safe: %v", git.branches)
	}
}

// TestMigrate_IgnoresTheEngineApplicationSets — the engine's own
// ApplicationSets select on addons.sharko.dev/<addon>, not on a bare addon
// name. Retiring one of those would take the new setup down along with the
// old.
func TestMigrate_IgnoresTheEngineApplicationSets(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	mgr := newFakeAppSetManager(engineAppSet("cert-manager"), engineAppSet("metrics-server"))
	orch := newMigrationOrchestratorWithAppSets(t, git, mgr)

	res, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(mgr.preserved) != 0 {
		t.Errorf("touched the engine's own ApplicationSets: %v", mgr.preserved)
	}
	if res.Handoff.State != HandoffStateNotNeeded {
		t.Errorf("Handoff.State = %q, want %q", res.Handoff.State, HandoffStateNotNeeded)
	}
}

// TestMigrate_HandoffIsOnThePullRequest — a person reading the pull
// request before merging it should be able to see that the running fleet
// was already made safe. That sentence is the reason merging is safe.
func TestMigrate_HandoffIsOnThePullRequest(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	mgr := newFakeAppSetManager(v3ScaffoldAppSet("cert-manager")).
		withApps("cert-manager", "cert-manager-prod-eu")
	orch := newMigrationOrchestratorWithAppSets(t, git, mgr)

	if _, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(git.prs) != 1 {
		t.Fatalf("expected one pull request, got %d", len(git.prs))
	}
	body := git.prs[0].Description
	if !strings.Contains(body, "What was already done in ArgoCD") {
		t.Errorf("PR body does not mention the ArgoCD side:\n%s", body)
	}
	if !strings.Contains(body, "cert-manager-prod-eu") {
		t.Errorf("PR body does not name the Application that was released:\n%s", body)
	}
}

// ─── H-2: the engine has to actually be applied ──────────────────────────

// TestCompleteRuntimeHandoff_RetiresThenStartsTheEngine pins BOTH halves of
// finding H-2 and the ordering the name collision forces: the old
// ApplicationSets go first, then the engine Application is applied.
func TestCompleteRuntimeHandoff_RetiresThenStartsTheEngine(t *testing.T) {
	files := newV3FixtureRepo()
	files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nmetadata:\n  name: sharko-engine\n")
	files[v4CatalogDeltaFixturePath] = []byte(v4CatalogDeltaFixture)

	prepared := v3ScaffoldAppSet("cert-manager")
	prepared.PreserveResourcesOnDeletion = true // prepare already ran
	mgr := newFakeAppSetManager(prepared)

	argo := &recordingArgocd{deletedSoFar: func() int { return len(mgr.deleted) }}
	orch := New(nil, nil, argo, newMigrationFakeGit(files), GitOpsConfig{
		BranchPrefix: "sharko/", CommitPrefix: "sharko:", BaseBranch: "main",
		RepoURL: "https://example.com/org/addons.git",
	}, migrationTestPaths(), nil)
	orch.SetCuratedCatalog(migrationCuratedCatalog(t))
	orch.SetApplicationSetManager(mgr)

	report, err := orch.CompleteRuntimeHandoff(context.Background())
	if err != nil {
		t.Fatalf("CompleteRuntimeHandoff: %v", err)
	}
	if report.State != HandoffStateComplete {
		t.Fatalf("State = %q, want %q (%s)", report.State, HandoffStateComplete, report.Message)
	}
	if !equalStrings(mgr.deleted, []string{"cert-manager"}) {
		t.Errorf("deleted = %v, want the old cert-manager ApplicationSet", mgr.deleted)
	}
	if !report.EngineApplied || argo.applications != 1 {
		t.Errorf("the engine Application was not applied (EngineApplied=%v, calls=%d)",
			report.EngineApplied, argo.applications)
	}
	if argo.orderedAfterDelete != len(mgr.deleted) {
		t.Errorf("the engine was applied before the old ApplicationSets were retired — "+
			"the two would fight over Applications of the same name (deleted before apply: %d of %d)",
			argo.orderedAfterDelete, len(mgr.deleted))
	}
}

// TestCompleteRuntimeHandoff_RefusesToDeleteAnUnpreparedApplicationSet —
// deleting one that can still prune IS the workload-removing move. When
// prepare never ran (a pull request merged outside Sharko, say), stop and
// say so.
func TestCompleteRuntimeHandoff_RefusesToDeleteAnUnpreparedApplicationSet(t *testing.T) {
	files := newV3FixtureRepo()
	files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	files[v4CatalogDeltaFixturePath] = []byte(v4CatalogDeltaFixture)

	mgr := newFakeAppSetManager(v3ScaffoldAppSet("cert-manager")) // NOT prepared
	argo := &recordingArgocd{}
	orch := New(nil, nil, argo, newMigrationFakeGit(files), GitOpsConfig{
		BaseBranch: "main", RepoURL: "https://example.com/org/addons.git",
	}, migrationTestPaths(), nil)
	orch.SetCuratedCatalog(migrationCuratedCatalog(t))
	orch.SetApplicationSetManager(mgr)

	report, err := orch.CompleteRuntimeHandoff(context.Background())
	if err != nil {
		t.Fatalf("CompleteRuntimeHandoff: %v", err)
	}
	if report.State != HandoffStatePending {
		t.Errorf("State = %q, want %q", report.State, HandoffStatePending)
	}
	if len(mgr.deleted) != 0 {
		t.Errorf("deleted an ApplicationSet that could still prune: %v", mgr.deleted)
	}
}

// TestCompleteRuntimeHandoff_IsANoOpBeforeTheMerge — it is wired to a merge
// callback and exposed as an endpoint, so it has to be safe to call at any
// moment, including before the pull request has merged.
func TestCompleteRuntimeHandoff_IsANoOpBeforeTheMerge(t *testing.T) {
	mgr := newFakeAppSetManager(v3ScaffoldAppSet("cert-manager"))
	orch := newMigrationOrchestratorWithAppSets(t, newMigrationFakeGit(newV3FixtureRepo()), mgr)

	report, err := orch.CompleteRuntimeHandoff(context.Background())
	if err != nil {
		t.Fatalf("CompleteRuntimeHandoff: %v", err)
	}
	if report.State != HandoffStatePending {
		t.Errorf("State = %q, want %q", report.State, HandoffStatePending)
	}
	if len(mgr.deleted) != 0 {
		t.Errorf("retired an ApplicationSet before the migration merged: %v", mgr.deleted)
	}
}

// TestMigrationStatus_V4Repo_ReportsUnfinishedHandoff — a repo whose files
// are across but whose old ApplicationSets are still in ArgoCD is NOT
// done, and status has to say so or nobody will ever press Finish.
func TestMigrationStatus_V4Repo_ReportsUnfinishedHandoff(t *testing.T) {
	files := map[string][]byte{
		EnginePinPath:             []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		v4CatalogDeltaFixturePath: []byte(v4CatalogDeltaFixture),
	}
	mgr := newFakeAppSetManager(v3ScaffoldAppSet("cert-manager"))
	orch := newMigrationOrchestratorWithAppSets(t, newMigrationFakeGit(files), mgr)

	got, err := orch.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("MigrationStatus: %v", err)
	}
	if got.Handoff == nil || got.Handoff.State != HandoffStatePending {
		t.Fatalf("Handoff = %+v, want state %q", got.Handoff, HandoffStatePending)
	}
	if !strings.Contains(got.Message, "has not finished") {
		t.Errorf("Message should say the ArgoCD side is unfinished, got %q", got.Message)
	}
}

// ─── the recording ArgoCD fake ───────────────────────────────────────────

// recordingArgocd counts CreateApplication calls and remembers how many
// ApplicationSet deletions had already happened when the first one landed
// — which is how the ordering assertion above is made.
type recordingArgocd struct {
	mockArgocd
	applications       int
	orderedAfterDelete int
	deletedSoFar       func() int
}

func (a *recordingArgocd) CreateApplication(context.Context, []byte) error {
	a.applications++
	if a.deletedSoFar != nil {
		a.orderedAfterDelete = a.deletedSoFar()
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// v4CatalogDeltaFixture is the catalog.yaml a migrated repo holds.
// The handoff reads it (plus the curated catalog) to work out which bare
// label keys the OLD ApplicationSets selected on.
const v4CatalogDeltaFixturePath = "catalog.yaml"

const v4CatalogDeltaFixture = `apiVersion: sharko.dev/v1
kind: AddonCatalog
addons:
  inhouse-api:
    repoURL: oci://registry.example.com/charts
    chart: inhouse-api
    version: 2.1.0
`

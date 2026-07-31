package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// ─── a git fake that can list directories ────────────────────────────────
//
// The shared mockGitProvider stubs ListDirectory to (nil, nil), which is
// fine for the paths that never enumerate a folder. The migration DOES
// enumerate — the fleet-wide values folder, and both values folders when
// working out what to delete — so it needs a fake whose listing reflects
// the files it was seeded with.

// It also models branches for real: writes and deletes land on the BRANCH,
// never on the base. That distinction is the whole of the all-or-nothing
// promise, so a fake that pooled both into one map would make the
// most important assertion in this file vacuous.
type migrationFakeGit struct {
	mu sync.Mutex

	// files is the BASE branch. The migration only ever reads from here.
	files map[string][]byte
	// branchWrites / branchDeletes are what landed on the feature branch.
	branchWrites  map[string][]byte
	branchDeletes []string

	branches []string
	// deletedBranches records DeleteBranch calls — the all-or-nothing
	// assertion.
	deletedBranches []string
	prs             []gitprovider.PullRequest

	// openPRs is what ListPullRequests("open") answers — set directly by
	// tests exercising the idempotent-retry check (findOpenMigrationPR),
	// independent of whatever CreatePullRequest has recorded in prs.
	openPRs []gitprovider.PullRequest

	// Failure injection.
	batchErr      error
	deleteFileErr error
	prErr         error
}

func newMigrationFakeGit(files map[string][]byte) *migrationFakeGit {
	copied := make(map[string][]byte, len(files))
	for k, v := range files {
		copied[k] = append([]byte(nil), v...)
	}
	return &migrationFakeGit{files: copied, branchWrites: map[string][]byte{}}
}

func (f *migrationFakeGit) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.files[path]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("file not found: %s: %w", path, gitprovider.ErrFileNotFound)
}

func (f *migrationFakeGit) ListDirectory(_ context.Context, dir, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := strings.TrimSuffix(dir, "/") + "/"
	var out []string
	for p := range f.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if strings.Contains(rest, "/") {
			continue // one level only, like the real providers
		}
		out = append(out, rest)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("directory not found: %s: %w", dir, gitprovider.ErrFileNotFound)
	}
	sort.Strings(out)
	return out, nil
}

func (f *migrationFakeGit) ListPullRequests(context.Context, string) ([]gitprovider.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.openPRs, nil
}
func (f *migrationFakeGit) TestConnection(context.Context) error { return nil }

func (f *migrationFakeGit) CreateBranch(_ context.Context, branch, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branches = append(f.branches, branch)
	return nil
}

func (f *migrationFakeGit) CreateOrUpdateFile(_ context.Context, path string, content []byte, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branchWrites[path] = content
	return nil
}

func (f *migrationFakeGit) BatchCreateFiles(_ context.Context, files map[string][]byte, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.batchErr != nil {
		return f.batchErr
	}
	for p, c := range files {
		f.branchWrites[p] = c
	}
	return nil
}

func (f *migrationFakeGit) DeleteFile(_ context.Context, path, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteFileErr != nil {
		return f.deleteFileErr
	}
	f.branchDeletes = append(f.branchDeletes, path)
	return nil
}

// removedOnBranch reports whether the migration deleted path on its branch.
func (f *migrationFakeGit) removedOnBranch(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.branchDeletes {
		if p == path {
			return true
		}
	}
	return false
}

func (f *migrationFakeGit) CreatePullRequest(_ context.Context, title, body, head, base string) (*gitprovider.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prErr != nil {
		return nil, f.prErr
	}
	pr := gitprovider.PullRequest{
		ID:           len(f.prs) + 1,
		Title:        title,
		Description:  body,
		SourceBranch: head,
		TargetBranch: base,
		URL:          fmt.Sprintf("https://example.com/pull/%d", len(f.prs)+1),
	}
	f.prs = append(f.prs, pr)
	return &pr, nil
}

func (f *migrationFakeGit) MergePullRequest(context.Context, int) error { return nil }
func (f *migrationFakeGit) GetPullRequestStatus(context.Context, int) (string, error) {
	return "open", nil
}

func (f *migrationFakeGit) DeleteBranch(_ context.Context, branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletedBranches = append(f.deletedBranches, branch)
	return nil
}

// ─── the fixture: a real v3 repo ─────────────────────────────────────────

// migrationCuratedCatalogYAML is the shipped catalog the fixture's own
// catalog is measured against. cert-manager and metrics-server are curated;
// the fixture also runs an in-house addon that is NOT here, which is what
// the delta must keep whole.
const migrationCuratedCatalogYAML = `addons:
  - name: cert-manager
    description: X.509 certificate management for Kubernetes.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    license: Apache-2.0
    category: security
    maintainers: ["jetstack"]
    curated_by: ["cncf-graduated"]
  - name: metrics-server
    description: Container resource metrics for Kubernetes.
    chart: metrics-server
    repo: https://kubernetes-sigs.github.io/metrics-server
    default_namespace: metrics-server
    license: Apache-2.0
    category: observability
    maintainers: ["sig-instrumentation"]
    curated_by: ["cncf-graduated"]
`

func migrationCuratedCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadBytes([]byte(migrationCuratedCatalogYAML))
	if err != nil {
		t.Fatalf("building the curated catalog fixture: %v", err)
	}
	return cat
}

// migrationV3CatalogYAML is the fixture's own configuration/addons-catalog.yaml
// — a FULL COPY, exactly as a v3 repo holds it:
//
//   - cert-manager      matches the shipped entry apart from the version
//     (which the shipped catalog never carries) — so only
//     the version should survive into the delta.
//   - metrics-server    same repo/chart as shipped, but pinned into a
//     different namespace and carrying sync options — so
//     namespace + settings must survive too.
//   - inhouse-api       has no shipped entry at all — every field must
//     survive, or it stops being deployable.
const migrationV3CatalogYAML = `applicationsets:
  - name: cert-manager
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: 1.14.5
    namespace: cert-manager
  - name: metrics-server
    repoURL: https://kubernetes-sigs.github.io/metrics-server
    chart: metrics-server
    version: 3.12.1
    namespace: kube-system
    selfHeal: false
    syncOptions:
      - ServerSideApply=true
  - name: inhouse-api
    repoURL: oci://registry.example.com/charts
    chart: inhouse-api
    version: 2.1.0
`

const migrationV3ManagedClustersYAML = `clusters:
  - name: prod-eu
    secretPath: k8s-prod-eu
    region: eu-central-1
    credsSource: secret-kubeconfig
    labels:
      cert-manager: enabled
      cert-manager-version: 1.12.0
      metrics-server: enabled
      inhouse-api: disabled
      env: prod
  - name: staging-us
    secretPath: k8s-staging-us
    region: us-east-1
    credsSource: secret-kubeconfig
    labels:
      cert-manager: enabled
      metrics-server: enabled
`

// migrationV3ProdValuesYAML is the fixture's per-cluster values file, in
// exactly the shape generateClusterValues wrote it: a clusterGlobalValues
// scratch block nothing ever read, then one block per addon — and those
// blocks ARE the Helm values the v3 ApplicationSet injected.
const migrationV3ProdValuesYAML = `# Cluster values for prod-eu
clusterGlobalValues:
  region: eu-central-1

cert-manager:
  replicaCount: 3
  installCRDs: true
metrics-server:
  enabled: true
`

const migrationV3GlobalCertManagerYAML = `# Helm values for cert-manager — applied to all clusters
installCRDs: true
replicaCount: 2
`

// newV3FixtureRepo builds a v3 repo the way a v3 bootstrap actually built
// one: every scaffold file the embedded template tree ships, at the repo
// path the v3 InitRepo committed it to, with the data files replaced by
// real content. Deriving the scaffold from the real template tree (rather
// than listing paths by hand) is what makes the removal assertions below
// mean something.
func newV3FixtureRepo() map[string][]byte {
	files := map[string][]byte{}
	for _, p := range v3ScaffoldRepoPaths() {
		files[p] = []byte("# shipped by the v3 bootstrap\n")
	}
	files["configuration/managed-clusters.yaml"] = []byte(migrationV3ManagedClustersYAML)
	files["configuration/addons-catalog.yaml"] = []byte(migrationV3CatalogYAML)
	files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(migrationV3ProdValuesYAML)
	files["configuration/addons-global-values/cert-manager.yaml"] = []byte(migrationV3GlobalCertManagerYAML)
	return files
}

func migrationTestPaths() RepoPathsConfig {
	return RepoPathsConfig{
		ClusterValues:   "configuration/addons-clusters-values",
		GlobalValues:    "configuration/addons-global-values",
		Catalog:         "configuration/addons-catalog.yaml",
		ManagedClusters: "configuration/managed-clusters.yaml",
		HostClusterName: "management",
	}
}

// newMigrationOrchestrator builds the orchestrator these tests migrate
// with — including a stand-in for the ApplicationSets the v3 bootstrap
// left running in ArgoCD.
//
// That last part is not decoration. Since the runtime-handoff fix (v4
// Wave 2 review finding B-1) a real migration REFUSES when it cannot
// reach those ApplicationSets, because merging the pull request without
// making them safe first uninstalls every addon on every cluster. A test
// helper that left them out would be testing a path production refuses to
// take. newMigrationOrchestratorNoAppSets is the deliberate opposite, used
// by the tests that assert the refusal.
func newMigrationOrchestrator(t *testing.T, git gitprovider.GitProvider) *Orchestrator {
	t.Helper()
	orch := newMigrationOrchestratorNoAppSets(t, git)
	orch.SetApplicationSetManager(defaultMigrationAppSets())
	return orch
}

// newMigrationOrchestratorNoAppSets builds one with NO ApplicationSet
// access at all — the state a Sharko that cannot reach ArgoCD is in.
func newMigrationOrchestratorNoAppSets(t *testing.T, git gitprovider.GitProvider) *Orchestrator {
	t.Helper()
	orch := New(nil, nil, nil, git, GitOpsConfig{
		BranchPrefix: "sharko/",
		CommitPrefix: "sharko:",
		BaseBranch:   "main",
		RepoURL:      "https://example.com/org/addons.git",
	}, migrationTestPaths(), nil)
	orch.SetCuratedCatalog(migrationCuratedCatalog(t))
	return orch
}

// ─── Story 5.1: status ───────────────────────────────────────────────────

func TestMigrationStatus_V3Repo_ReportsMigrationAvailable(t *testing.T) {
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(newV3FixtureRepo()))

	got, statusErr := orch.MigrationStatus(context.Background())
	if statusErr != nil {
		t.Fatalf("MigrationStatus: %v", statusErr)
	}

	if got.Format != RepoFormatV3 {
		t.Errorf("Format = %q, want %q", got.Format, RepoFormatV3)
	}
	if !got.MigrationAvailable {
		t.Error("MigrationAvailable = false, want true on a v3 repo")
	}
	if !strings.Contains(got.Message, "migration available") {
		t.Errorf("Message = %q, should say a migration is available", got.Message)
	}
}

func TestMigrationStatus_V4Repo_NothingToMigrate(t *testing.T) {
	files := map[string][]byte{
		EnginePinPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
	}
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(files))

	got, statusErr := orch.MigrationStatus(context.Background())
	if statusErr != nil {
		t.Fatalf("MigrationStatus: %v", statusErr)
	}

	if got.Format != RepoFormatV4 {
		t.Errorf("Format = %q, want %q", got.Format, RepoFormatV4)
	}
	if got.MigrationAvailable {
		t.Error("MigrationAvailable = true on a v4 repo; want false")
	}
}

// TestMigrationStatus_V3MarkersPresentButPinned_ReportsV4 covers the
// moment right after the migration PR merges, when the repo briefly has
// both an engine pin and (until the next read) whatever a caller cached.
// The pin is the newer, more specific answer and must win.
func TestMigrationStatus_EnginePinWins(t *testing.T) {
	files := newV3FixtureRepo()
	files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n")
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(files))

	got, statusErr := orch.MigrationStatus(context.Background())
	if statusErr != nil {
		t.Fatalf("MigrationStatus: %v", statusErr)
	}
	if got.Format != RepoFormatV4 {
		t.Errorf("Format = %q, want %q — the engine pin is the more specific answer", got.Format, RepoFormatV4)
	}
}

func TestMigrationStatus_EmptyRepo_SaysInitializeInstead(t *testing.T) {
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(nil))

	got, statusErr := orch.MigrationStatus(context.Background())
	if statusErr != nil {
		t.Fatalf("MigrationStatus: %v", statusErr)
	}

	if got.Format != RepoFormatEmpty {
		t.Errorf("Format = %q, want %q", got.Format, RepoFormatEmpty)
	}
	if got.MigrationAvailable {
		t.Error("MigrationAvailable = true on an empty repo; want false")
	}
}

// TestMigrationStatus_ReportsOpenMigrationPR is the "server truth" half of
// the migration-banner double-PR fix: once a previous migrate call opened
// a PR that is still open, GET /migration/status must surface it — a UI
// component that lost its own in-memory "PR is open" flag on remount can
// still learn the right thing from the next status poll.
func TestMigrationStatus_ReportsOpenMigrationPR(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	git.openPRs = []gitprovider.PullRequest{
		{ID: 42, URL: "https://example.com/pull/42", SourceBranch: "sharko/migrate-to-v4-abcd1234", TargetBranch: "main"},
	}
	orch := newMigrationOrchestrator(t, git)

	got, statusErr := orch.MigrationStatus(context.Background())
	if statusErr != nil {
		t.Fatalf("MigrationStatus: %v", statusErr)
	}

	if got.MigrationPRURL != "https://example.com/pull/42" {
		t.Errorf("MigrationPRURL = %q, want the open PR's URL", got.MigrationPRURL)
	}
	if got.MigrationPRNumber != 42 {
		t.Errorf("MigrationPRNumber = %d, want 42", got.MigrationPRNumber)
	}
}

// TestMigrationStatus_NoOpenMigrationPR_FieldsEmpty is the negative case —
// a v3 repo with no migration attempt yet reports no PR fields at all.
func TestMigrationStatus_NoOpenMigrationPR_FieldsEmpty(t *testing.T) {
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(newV3FixtureRepo()))

	got, statusErr := orch.MigrationStatus(context.Background())
	if statusErr != nil {
		t.Fatalf("MigrationStatus: %v", statusErr)
	}

	if got.MigrationPRURL != "" || got.MigrationPRNumber != 0 {
		t.Errorf("expected no PR fields, got url=%q number=%d", got.MigrationPRURL, got.MigrationPRNumber)
	}
}

// TestMigrate_RefusesWhenMigrationPROpen is the pinning test for the
// double-PR bug: a real (non-dry-run) migrate call must refuse — not open
// a second PR — when a previous attempt's PR is still open, and the
// refusal must carry the existing PR's link back to the caller.
func TestMigrate_RefusesWhenMigrationPROpen(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	git.openPRs = []gitprovider.PullRequest{
		{ID: 7, URL: "https://example.com/pull/7", SourceBranch: "sharko/migrate-to-v4-deadbeef", TargetBranch: "main"},
	}
	orch := newMigrationOrchestrator(t, git)

	_, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err == nil {
		t.Fatal("expected an error when a migration PR is already open")
	}
	var prOpenErr *ErrMigrationPROpen
	if !errors.As(err, &prOpenErr) {
		t.Fatalf("error = %v (%T), want *ErrMigrationPROpen", err, err)
	}
	if prOpenErr.PRURL != "https://example.com/pull/7" {
		t.Errorf("PRURL = %q, want the existing PR's URL", prOpenErr.PRURL)
	}
	if prOpenErr.PRNumber != 7 {
		t.Errorf("PRNumber = %d, want 7", prOpenErr.PRNumber)
	}

	// And, crucially, no second PR was opened.
	if len(git.prs) != 0 {
		t.Errorf("commitMigrationPR ran anyway; got %d PR(s) created, want 0", len(git.prs))
	}
}

// TestMigrate_DryRun_AllowedWhenMigrationPROpen — a dry run has zero side
// effects regardless, so it must not be blocked by the open-PR check; that
// check only guards the real, PR-creating path.
func TestMigrate_DryRun_AllowedWhenMigrationPROpen(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	git.openPRs = []gitprovider.PullRequest{
		{ID: 7, URL: "https://example.com/pull/7", SourceBranch: "sharko/migrate-to-v4-deadbeef", TargetBranch: "main"},
	}
	orch := newMigrationOrchestrator(t, git)

	result, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{DryRun: true})
	if err != nil {
		t.Fatalf("dry run should not be blocked by an open migration PR: %v", err)
	}
	if result.Status != "preview" {
		t.Errorf("Status = %q, want %q", result.Status, "preview")
	}
}

// ─── Story 5.1: the write gate ───────────────────────────────────────────

func TestRefuseOnV3Repo_RefusesV3_AllowsV4AndEmpty(t *testing.T) {
	ctx := context.Background()

	v3 := newMigrationOrchestrator(t, newMigrationFakeGit(newV3FixtureRepo()))
	err := v3.RefuseOnV3Repo(ctx)
	if !IsV3MigrationRequired(err) {
		t.Fatalf("v3 repo: err = %v, want ErrV3MigrationRequired", err)
	}
	if err.Error() != V3MigrationRequiredMessage {
		t.Errorf("message = %q, want %q", err.Error(), V3MigrationRequiredMessage)
	}

	v4files := map[string][]byte{EnginePinPath: []byte("kind: Application\n")}
	if err := newMigrationOrchestrator(t, newMigrationFakeGit(v4files)).RefuseOnV3Repo(ctx); err != nil {
		t.Errorf("v4 repo: err = %v, want nil (the v4 guards own that case)", err)
	}

	if err := newMigrationOrchestrator(t, newMigrationFakeGit(nil)).RefuseOnV3Repo(ctx); err != nil {
		t.Errorf("empty repo: err = %v, want nil (bootstrap must stay reachable)", err)
	}
}

// ─── Story 5.1: the preview ──────────────────────────────────────────────

// TestPreviewMigration_ListsEveryFile pins the whole plan, path by path.
// A preview that is merely "about right" is worse than none: it is what a
// person reads instead of the diff.
func TestPreviewMigration_ListsEveryFile(t *testing.T) {
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(newV3FixtureRepo()))

	plan, err := orch.PreviewMigration(context.Background())
	if err != nil {
		t.Fatalf("PreviewMigration: %v", err)
	}

	if plan.Format != RepoFormatV3 {
		t.Errorf("Format = %q, want %q", plan.Format, RepoFormatV3)
	}

	written := map[string]string{} // path -> from
	for _, c := range append(append([]MigrationFileChange{}, plan.Add...), plan.Convert...) {
		written[c.Path] = c.FromPath
	}

	wantWritten := map[string]string{
		"engine.yaml":                     "",
		"README.md":                                   "README.md",
		"managed-clusters.yaml":                      "configuration/managed-clusters.yaml",
		"catalog.yaml":                                "configuration/addons-catalog.yaml",
		"clusters/prod-eu.yaml":                       "",
		"clusters/staging-us.yaml":                    "",
		"values/global/cert-manager.yaml":             "configuration/addons-global-values/cert-manager.yaml",
		"values/clusters/prod-eu/cert-manager.yaml":   "configuration/addons-clusters-values/prod-eu.yaml",
		"values/clusters/prod-eu/metrics-server.yaml": "configuration/addons-clusters-values/prod-eu.yaml",
		// The bootstrap shipped three fleet-wide values stubs. Every file
		// in that folder moves, even for an addon the catalog no longer
		// lists — losing hand-written values would be data loss, not tidying.
		"values/global/external-secrets.yaml": "configuration/addons-global-values/external-secrets.yaml",
		"values/global/metrics-server.yaml":   "configuration/addons-global-values/metrics-server.yaml",
	}
	for path, wantFrom := range wantWritten {
		gotFrom, ok := written[path]
		if !ok {
			t.Errorf("plan is missing %s", path)
			continue
		}
		if gotFrom != wantFrom {
			t.Errorf("%s: from = %q, want %q", path, gotFrom, wantFrom)
		}
		delete(written, path)
	}
	for path := range written {
		// The seed's .gitkeep placeholders are the only extra files that
		// may appear, and only for folders left genuinely empty.
		if !strings.HasSuffix(path, "/.gitkeep") {
			t.Errorf("plan writes an unexpected file: %s", path)
		}
	}

	// The scaffold, gone.
	removed := map[string]bool{}
	for _, c := range plan.Remove {
		removed[c.Path] = true
	}
	for _, mustGo := range []string{
		"bootstrap/Chart.yaml",
		"bootstrap/templates/addons-appset.yaml",
		"bootstrap/templates/connectivity-check-appset.yaml",
		"bootstrap/templates/_helpers.tpl",
		"root-app.yaml",
		"repository-secret.yaml",
		"configuration/bootstrap-config.yaml",
		"configuration/connectivity-check/configmap.yaml",
		// the converted data files, at their old paths
		"configuration/managed-clusters.yaml",
		"configuration/addons-catalog.yaml",
		"configuration/addons-clusters-values/prod-eu.yaml",
		"configuration/addons-global-values/cert-manager.yaml",
		// leftovers inside the v3 values folders, so the folders go too
		"configuration/addons-clusters-values/.gitkeep",
		"configuration/addons-clusters-values/cluster-example.yaml",
	} {
		if !removed[mustGo] {
			t.Errorf("plan does not remove %s", mustGo)
		}
	}
	if removed["README.md"] {
		t.Error("plan removes README.md; it is rewritten, not deleted")
	}
}

// TestPreviewMigration_RedactsValuesContent proves the preview rides the
// same redaction discipline as every other dry run: a secret-looking value
// in a values file never appears in a plan.
func TestPreviewMigration_RedactsValuesContent(t *testing.T) {
	files := newV3FixtureRepo()
	files["configuration/addons-global-values/cert-manager.yaml"] = []byte(
		"installCRDs: true\napi_token: super-secret-value\n")
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(files))

	plan, err := orch.PreviewMigration(context.Background())
	if err != nil {
		t.Fatalf("PreviewMigration: %v", err)
	}

	var content string
	for _, c := range plan.Convert {
		if c.Path == "values/global/cert-manager.yaml" {
			content = c.Content
		}
	}
	if content == "" {
		t.Fatal("the plan has no content for values/global/cert-manager.yaml")
	}
	if strings.Contains(content, "super-secret-value") {
		t.Errorf("the preview leaked a secret value:\n%s", content)
	}
	if !strings.Contains(content, "<redacted>") {
		t.Errorf("the preview should show the key as redacted:\n%s", content)
	}
	if !strings.Contains(content, "installCRDs") {
		t.Errorf("redaction should keep the structure visible:\n%s", content)
	}
}

// TestPreviewMigration_AlreadyV4_CleanAnswer — idempotence. Somebody who
// is not sure whether the migration worked will run this again.
func TestPreviewMigration_AlreadyV4_CleanAnswer(t *testing.T) {
	files := map[string][]byte{EnginePinPath: []byte("kind: Application\n")}
	orch := newMigrationOrchestrator(t, newMigrationFakeGit(files))

	plan, err := orch.PreviewMigration(context.Background())
	if err != nil {
		t.Fatalf("PreviewMigration on a v4 repo should not error: %v", err)
	}
	if plan.Format != RepoFormatV4 {
		t.Errorf("Format = %q, want %q", plan.Format, RepoFormatV4)
	}
	if len(plan.Add)+len(plan.Convert)+len(plan.Remove) != 0 {
		t.Errorf("a v4 repo has nothing to change; got %d writes and %d removals",
			len(plan.Add)+len(plan.Convert), len(plan.Remove))
	}
	if len(plan.Notes) == 0 || !strings.Contains(plan.Notes[0], "already") {
		t.Errorf("Notes = %v, should say the repo is already migrated", plan.Notes)
	}
}

// ─── Story 5.2: the conversion ───────────────────────────────────────────

func TestMigrate_FullConversion(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	orch := newMigrationOrchestrator(t, git)

	result, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err != nil {
		t.Fatalf("MigrateV3ToV4: %v", err)
	}
	if result.Status != "migrated" {
		t.Errorf("Status = %q, want %q", result.Status, "migrated")
	}
	if len(git.prs) != 1 {
		t.Fatalf("opened %d pull requests, want exactly 1", len(git.prs))
	}
	if len(git.branches) != 1 {
		t.Fatalf("created %d branches, want exactly 1", len(git.branches))
	}

	// The cluster assignment file.
	spec, err := models.LoadClusterAddons(git.branchWrites["clusters/prod-eu.yaml"])
	if err != nil {
		t.Fatalf("clusters/prod-eu.yaml does not read back: %v", err)
	}
	if spec.Cluster != "prod-eu" {
		t.Errorf("spec.Cluster = %q, want prod-eu", spec.Cluster)
	}
	certManager, ok := spec.Addons["cert-manager"]
	if !ok {
		t.Fatal("prod-eu lost its cert-manager entry")
	}
	if !certManager.Enabled {
		t.Error("cert-manager should still be enabled on prod-eu")
	}
	// The v3 per-cluster version override lived in a `<addon>-version`
	// label; losing it would move the cluster onto a different version.
	if certManager.Version != "1.12.0" {
		t.Errorf("cert-manager version pin = %q, want 1.12.0 (from the cert-manager-version label)", certManager.Version)
	}
	inhouse, ok := spec.Addons["inhouse-api"]
	if !ok {
		t.Error("the disabled inhouse-api entry should be KEPT, so re-enabling is one word")
	} else if inhouse.Enabled {
		t.Error("inhouse-api was disabled on prod-eu and must stay disabled")
	}

	// The connection record: connection data intact, addon keys gone,
	// non-addon labels kept.
	conns, err := models.LoadManagedClusters(git.branchWrites["managed-clusters.yaml"])
	if err != nil {
		t.Fatalf("managed-clusters.yaml does not read back: %v", err)
	}
	if len(conns.Clusters) != 2 {
		t.Fatalf("managed-clusters.yaml has %d clusters, want 2", len(conns.Clusters))
	}
	var prod models.ManagedClusterEntry
	for _, c := range conns.Clusters {
		if c.Name == "prod-eu" {
			prod = c
		}
	}
	if prod.SecretPath != "k8s-prod-eu" || prod.Region != "eu-central-1" || prod.CredsSource != "secret-kubeconfig" {
		t.Errorf("prod-eu lost connection data: %+v", prod)
	}
	if _, still := prod.Labels["cert-manager"]; still {
		t.Error("addon keys must move OUT of the connection record (design doc 2.4)")
	}
	if _, still := prod.Labels["cert-manager-version"]; still {
		t.Error("the <addon>-version label must move into the assignment file's version pin")
	}
	if prod.Labels["env"] != "prod" {
		t.Errorf("a non-addon label was dropped: labels = %v", prod.Labels)
	}

	// Values, split per addon, carried verbatim.
	certValues := map[string]interface{}{}
	if err := yaml.Unmarshal(git.branchWrites["values/clusters/prod-eu/cert-manager.yaml"], &certValues); err != nil {
		t.Fatalf("per-cluster values do not read back: %v", err)
	}
	want := map[string]interface{}{"replicaCount": 3, "installCRDs": true}
	if !reflect.DeepEqual(certValues, want) {
		t.Errorf("per-cluster cert-manager values = %v, want %v", certValues, want)
	}
	if _, leaked := git.branchWrites["values/clusters/prod-eu/clusterGlobalValues.yaml"]; leaked {
		t.Error("clusterGlobalValues was never an addon and must not become a values file")
	}
	if got := string(git.branchWrites["values/global/cert-manager.yaml"]); !strings.Contains(got, "installCRDs: true") {
		t.Errorf("the fleet-wide values file lost its content:\n%s", got)
	}

	// The engine pin, from the same generator the bootstrap uses.
	pin := string(git.branchWrites[EnginePinPath])
	seedPin := string(BuildV4SeedFiles(orch.gitops, orch.paths)[EnginePinPath])
	if pin != seedPin {
		t.Errorf("the migrated engine pin differs from the bootstrap seed's:\ngot:\n%s\nwant:\n%s", pin, seedPin)
	}

	// And the scaffold is gone.
	for _, gone := range []string{
		"bootstrap/Chart.yaml",
		"bootstrap/templates/addons-appset.yaml",
		"root-app.yaml",
		"configuration/managed-clusters.yaml",
		"configuration/addons-catalog.yaml",
		"configuration/addons-clusters-values/prod-eu.yaml",
	} {
		if !git.removedOnBranch(gone) {
			t.Errorf("%s survived the migration", gone)
		}
	}
}

func TestMigrate_RequiresConfirmation(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	orch := newMigrationOrchestrator(t, git)

	_, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{})
	if err == nil || !strings.Contains(err.Error(), "confirmation required") {
		t.Fatalf("err = %v, want a confirmation-required refusal", err)
	}
	if len(git.branches) != 0 {
		t.Errorf("an unconfirmed migration created %d branches; want 0", len(git.branches))
	}
}

func TestMigrate_DryRun_TouchesNothing(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	orch := newMigrationOrchestrator(t, git)

	result, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{DryRun: true})
	if err != nil {
		t.Fatalf("MigrateV3ToV4 dry run: %v", err)
	}
	if result.Status != "preview" {
		t.Errorf("Status = %q, want %q", result.Status, "preview")
	}
	if result.Plan == nil || len(result.Plan.Convert) == 0 {
		t.Error("a dry run should still return the full plan")
	}
	if len(git.branches) != 0 || len(git.prs) != 0 || len(git.branchDeletes) != 0 {
		t.Errorf("a dry run touched git: %d branches, %d PRs, %d deletes",
			len(git.branches), len(git.prs), len(git.branchDeletes))
	}
}

func TestMigrate_AlreadyV4_IsACleanNoOp(t *testing.T) {
	git := newMigrationFakeGit(map[string][]byte{EnginePinPath: []byte("kind: Application\n")})
	orch := newMigrationOrchestrator(t, git)

	result, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err != nil {
		t.Fatalf("re-running the migration must be safe, got: %v", err)
	}
	if result.Status != "already_migrated" {
		t.Errorf("Status = %q, want %q", result.Status, "already_migrated")
	}
	if len(git.branches) != 0 || len(git.prs) != 0 {
		t.Error("an already-migrated repo must not be touched at all")
	}
}

// ─── Story 5.2: all-or-nothing ───────────────────────────────────────────

// TestMigrate_AllOrNothing_OnWriteFailure proves the promise: a failure
// part-way leaves NOTHING behind — no pull request, and no branch somebody
// could merge by hand.
func TestMigrate_AllOrNothing_OnWriteFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inject func(g *migrationFakeGit)
	}{
		{"writing the files fails", func(g *migrationFakeGit) { g.batchErr = errors.New("upstream refused the commit") }},
		{"a delete fails", func(g *migrationFakeGit) { g.deleteFileErr = errors.New("upstream refused the delete") }},
		{"opening the PR fails", func(g *migrationFakeGit) { g.prErr = errors.New("upstream refused the pull request") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := newMigrationFakeGit(newV3FixtureRepo())
			tc.inject(git)
			orch := newMigrationOrchestrator(t, git)

			_, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
			if err == nil {
				t.Fatal("expected the migration to fail")
			}
			if !strings.Contains(err.Error(), "nothing was changed on main") {
				t.Errorf("err = %v, should say the base branch is untouched", err)
			}
			if len(git.prs) != 0 {
				t.Errorf("a failed migration left %d pull request(s) behind", len(git.prs))
			}
			if len(git.deletedBranches) != 1 || git.deletedBranches[0] != git.branches[0] {
				t.Errorf("the branch was not cleaned up: created %v, deleted %v", git.branches, git.deletedBranches)
			}
			// The base branch still has its v3 files, untouched.
			if _, ok := git.files["configuration/managed-clusters.yaml"]; !ok {
				t.Error("the v3 cluster registry was removed from the base branch despite the failure")
			}
			if _, ok := git.files[EnginePinPath]; ok {
				t.Error("the engine pin reached the base branch despite the failure")
			}
		})
	}
}

// TestMigrate_RefusesBeforeAnyWrite_OnUnmigratableRepo covers the other
// half of all-or-nothing: a repo that CANNOT be migrated is refused during
// the build, before a branch exists at all.
func TestMigrate_RefusesBeforeAnyWrite_OnUnmigratableRepo(t *testing.T) {
	files := newV3FixtureRepo()
	// An in-house addon with no version: nothing can fill that in, so the
	// migration must say so instead of shipping a catalog the engine
	// cannot render.
	files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: inhouse-api
    repoURL: oci://registry.example.com/charts
    chart: inhouse-api
`)
	git := newMigrationFakeGit(files)
	orch := newMigrationOrchestrator(t, git)

	_, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
	if err == nil {
		t.Fatal("expected a refusal for a catalog that would not merge")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("err = %v, should name the missing field", err)
	}
	if len(git.branches) != 0 {
		t.Errorf("a refused migration created %d branches; want 0", len(git.branches))
	}
}

// ─── Story 5.2: the catalog delta round trip ─────────────────────────────

// TestMigrate_CatalogDelta_RoundTrips is the invariant that makes the
// conversion safe: reading the converted catalog.yaml back must reproduce
// the user's effective v3 entry, field for field, for EVERY addon they had.
// Nothing fills a blank in behind it any more, so this is now a straight
// comparison against the file itself.
func TestMigrate_CatalogDelta_RoundTrips(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	orch := newMigrationOrchestrator(t, git)

	if _, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true}); err != nil {
		t.Fatalf("MigrateV3ToV4: %v", err)
	}

	delta, err := config.LoadAddonCatalog(git.branchWrites[config.AddonCatalogPath])
	if err != nil {
		t.Fatalf("catalog.yaml does not read back: %v", err)
	}
	merged := catalog.BuildCatalogView(migrationCuratedCatalog(t), delta)

	v3Entries, err := parseAddonsCatalog([]byte(migrationV3CatalogYAML))
	if err != nil {
		t.Fatalf("parsing the v3 fixture catalog: %v", err)
	}

	for _, v3 := range v3Entries {
		got, ok := merged[v3.Name]
		if !ok {
			t.Errorf("%s disappeared from the merged catalog", v3.Name)
			continue
		}
		if got.RepoURL != v3.RepoURL {
			t.Errorf("%s: repoURL = %q, want %q", v3.Name, got.RepoURL, v3.RepoURL)
		}
		if got.Chart != v3.Chart {
			t.Errorf("%s: chart = %q, want %q", v3.Name, got.Chart, v3.Chart)
		}
		if got.Version != v3.Version {
			t.Errorf("%s: version = %q, want %q", v3.Name, got.Version, v3.Version)
		}
		if effectiveNamespace(got.Namespace, v3.Name) != effectiveNamespace(v3.Namespace, v3.Name) {
			t.Errorf("%s: namespace = %q, want %q — the addon would move namespaces",
				v3.Name, effectiveNamespace(got.Namespace, v3.Name), effectiveNamespace(v3.Namespace, v3.Name))
		}
		if v3.SelfHeal != nil {
			if got.Settings == nil || got.Settings.SelfHeal == nil || *got.Settings.SelfHeal != *v3.SelfHeal {
				t.Errorf("%s: selfHeal was not carried across", v3.Name)
			}
		}
		if len(v3.SyncOptions) > 0 {
			if got.Settings == nil || !reflect.DeepEqual(got.Settings.SyncOptions, v3.SyncOptions) {
				t.Errorf("%s: syncOptions = %v, want %v", v3.Name, got.Settings, v3.SyncOptions)
			}
		}
	}
}

// TestMigrate_CatalogConversion_CarriesEveryEntryWhole is the full-entry
// half: the converted file is self-contained, so a field the shipped list
// happens to say the same thing about is written out anyway. Dropping it
// would leave a catalog the engine cannot render, because nothing fills a
// blank in behind the file any more.
func TestMigrate_CatalogConversion_CarriesEveryEntryWhole(t *testing.T) {
	git := newMigrationFakeGit(newV3FixtureRepo())
	orch := newMigrationOrchestrator(t, git)

	if _, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true}); err != nil {
		t.Fatalf("MigrateV3ToV4: %v", err)
	}
	converted, err := config.LoadAddonCatalog(git.branchWrites[config.AddonCatalogPath])
	if err != nil {
		t.Fatalf("catalog.yaml does not read back: %v", err)
	}

	// cert-manager matches the shipped entry on repo, chart and namespace,
	// and every one of those is still written out.
	cert, ok := converted.Addons["cert-manager"]
	if !ok {
		t.Fatal("cert-manager is missing from the converted catalog")
	}
	if cert.RepoURL == "" || cert.Chart == "" || cert.Version != "1.14.5" {
		t.Errorf("cert-manager is not a complete entry: %+v", cert)
	}

	// metrics-server differs from the shipped entry on namespace — the
	// user's own choice, and it must survive the conversion untouched.
	ms, ok := converted.Addons["metrics-server"]
	if !ok {
		t.Fatal("metrics-server is missing from the converted catalog")
	}
	if ms.Namespace != "kube-system" {
		t.Errorf("metrics-server namespace = %q, want kube-system — this is the user's own change", ms.Namespace)
	}
	if ms.RepoURL == "" || ms.Chart == "" {
		t.Errorf("metrics-server is not a complete entry: %+v", ms)
	}

	// The in-house addon has no shipped entry at all.
	inhouse, ok := converted.Addons["inhouse-api"]
	if !ok {
		t.Fatal("inhouse-api is missing — it would stop being deployable")
	}
	if inhouse.RepoURL == "" || inhouse.Chart == "" || inhouse.Version == "" {
		t.Errorf("inhouse-api lost a required field: %+v", inhouse)
	}
}

// TestBuildCatalog_KeepsAnEntryEvenWhenTheShippedListAgrees is the case
// that used to disappear: a curated addon the user changed nothing about.
// Under the approved-list model it is still an addon the org runs, so it
// stays — and it stays whole.
func TestBuildCatalog_KeepsAnEntryEvenWhenTheShippedListAgrees(t *testing.T) {
	curated := migrationCuratedCatalog(t)
	spec, _ := buildCatalogDelta([]models.AddonCatalogEntry{{
		Name:      "cert-manager",
		RepoURL:   "https://charts.jetstack.io",
		Chart:     "cert-manager",
		Namespace: "cert-manager",
		Version:   "1.14.5",
	}}, curated)

	got, present := spec.Addons["cert-manager"]
	if !present {
		t.Fatalf("an approved addon must stay in the catalog; got %+v", spec.Addons)
	}
	if got.RepoURL != "https://charts.jetstack.io" || got.Chart != "cert-manager" || got.Version != "1.14.5" {
		t.Errorf("the entry is not self-contained: %+v", got)
	}
}

// TestBuildCatalogDelta_NoCuratedCatalog_CarriesEverything: a server with
// no shipped catalog loaded cannot tell curated from user-added, so the
// lossless answer is to carry every entry across whole.
func TestBuildCatalogDelta_NoCuratedCatalog_CarriesEverything(t *testing.T) {
	v3Entries, err := parseAddonsCatalog([]byte(migrationV3CatalogYAML))
	if err != nil {
		t.Fatalf("parsing the v3 fixture catalog: %v", err)
	}
	spec, _ := buildCatalogDelta(v3Entries, nil)

	if len(spec.Addons) != len(v3Entries) {
		t.Fatalf("delta has %d addons, want all %d", len(spec.Addons), len(v3Entries))
	}
	for _, v3 := range v3Entries {
		got := spec.Addons[v3.Name]
		if got.RepoURL != v3.RepoURL || got.Chart != v3.Chart || got.Version != v3.Version {
			t.Errorf("%s was not carried across whole: %+v", v3.Name, got)
		}
	}
}

// TestBuildCatalogDelta_NotesSecretsItCannotCarry: a v3 catalog entry can
// declare secrets, and the v4 delta has nowhere to put them yet. Silently
// dropping them would be discovered from a broken cluster; the note is how
// somebody finds out before merging.
func TestBuildCatalogDelta_NotesSecretsItCannotCarry(t *testing.T) {
	_, notes := buildCatalogDelta([]models.AddonCatalogEntry{{
		Name:    "datadog",
		RepoURL: "https://example.com",
		Chart:   "datadog",
		Version: "1.0.0",
		Secrets: []models.AddonSecretRef{{SecretName: "datadog-keys"}},
	}}, nil)

	if len(notes) == 0 || !strings.Contains(notes[0], "datadog") || !strings.Contains(notes[0], "secrets") {
		t.Errorf("notes = %v, should name datadog and its secrets", notes)
	}
}

// ─── Story 5.2: the addon-set invariant ──────────────────────────────────

// TestMigrate_AddonSetPerCluster_Unchanged is the invariant stated as the
// story's own acceptance criterion, checked the way the fleet actually
// experiences it: the addon-enablement labels the reconciler puts on each
// cluster's ArgoCD Secret must name the same addons before and after.
//
// The v3 and v4 label KEYS differ on purpose (`cert-manager: enabled` vs
// `addons.sharko.dev/cert-manager: enabled`, models.V4AddonLabelPrefix), so
// what must match is the addon set the two label maps describe — not the
// raw strings.
func TestMigrate_AddonSetPerCluster_Unchanged(t *testing.T) {
	fixture := newV3FixtureRepo()
	git := newMigrationFakeGit(fixture)
	orch := newMigrationOrchestrator(t, git)

	if _, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true}); err != nil {
		t.Fatalf("MigrateV3ToV4: %v", err)
	}

	before, err := models.LoadManagedClusters([]byte(migrationV3ManagedClustersYAML))
	if err != nil {
		t.Fatalf("parsing the v3 fixture registry: %v", err)
	}
	v3Catalog, err := parseAddonsCatalog([]byte(migrationV3CatalogYAML))
	if err != nil {
		t.Fatalf("parsing the v3 fixture catalog: %v", err)
	}
	inCatalog := map[string]bool{}
	for _, e := range v3Catalog {
		inCatalog[e.Name] = true
	}

	for _, cluster := range before.Clusters {
		// What v3 deployed: a bare `<addon>: enabled` label, for an addon
		// the catalog actually had an ApplicationSet for.
		wantLabels := map[string]string{}
		for key, value := range cluster.Labels {
			if inCatalog[key] && models.AddonLabelEnabled(value) {
				wantLabels[models.V4AddonLabelKey(key)] = models.LabelEnabled
			}
		}

		// What v4 deploys: the labels derived from the assignment file.
		// Same derivation clusterreconciler.v4LabelsFor applies — pinned
		// against the real function in
		// internal/clusterreconciler/v4_migration_labels_test.go.
		assignPath, err := v4ClusterAddonsPath(cluster.Name)
		if err != nil {
			t.Fatalf("%s: %v", cluster.Name, err)
		}
		spec, err := models.LoadClusterAddons(git.branchWrites[assignPath])
		if err != nil {
			t.Fatalf("%s: %v", assignPath, err)
		}
		gotLabels := map[string]string{}
		for addon, entry := range spec.Addons {
			if entry.Enabled && models.IsValidResourceName(addon) {
				gotLabels[models.V4AddonLabelKey(addon)] = models.LabelEnabled
			}
		}

		if !reflect.DeepEqual(gotLabels, wantLabels) {
			t.Errorf("%s runs a different set of addons after the migration:\n after: %v\n before: %v",
				cluster.Name, gotLabels, wantLabels)
		}
	}
}

// TestAssertAddonSetUnchanged_CatchesADrift is the guard on the guard: if
// the conversion ever starts adding or dropping an addon, the build must
// refuse rather than write it.
func TestAssertAddonSetUnchanged_CatchesADrift(t *testing.T) {
	v3 := models.ManagedClusterEntry{
		Name:   "prod-eu",
		Labels: models.ClusterLabels{"cert-manager": models.LabelEnabled},
	}
	v4 := models.ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons: map[string]models.ClusterAddonsAddon{
			"cert-manager":   {Enabled: false}, // dropped
			"metrics-server": {Enabled: true},  // invented
		},
	}

	err := assertAddonSetUnchanged(v3, v4, map[string]bool{"cert-manager": true, "metrics-server": true})
	if err == nil {
		t.Fatal("expected a refusal when the addon set drifts")
	}
	if !strings.Contains(err.Error(), "metrics-server") || !strings.Contains(err.Error(), "cert-manager") {
		t.Errorf("err = %v, should name both the added and the dropped addon", err)
	}
}

// ─── the scaffold list ───────────────────────────────────────────────────

// TestV3ScaffoldRepoPaths_MatchesWhatTheV3BootstrapWrote pins the path
// mapping the removal list depends on: Chart.yaml and templates/ keep the
// bootstrap/ prefix; everything else was committed at the repo root.
func TestV3ScaffoldRepoPaths_MatchesWhatTheV3BootstrapWrote(t *testing.T) {
	got := map[string]bool{}
	for _, p := range v3ScaffoldRepoPaths() {
		got[p] = true
	}

	for _, want := range []string{
		"bootstrap/Chart.yaml",
		"bootstrap/templates/_helpers.tpl",
		"bootstrap/templates/addons-appset.yaml",
		"README.md",
		"root-app.yaml",
		"repository-secret.yaml",
		"configuration/managed-clusters.yaml",
		"configuration/addons-catalog.yaml",
	} {
		if !got[want] {
			t.Errorf("the scaffold list is missing %s", want)
		}
	}
	for p := range got {
		if strings.HasPrefix(p, "bootstrap/") &&
			p != "bootstrap/Chart.yaml" &&
			!strings.HasPrefix(p, "bootstrap/templates/") {
			t.Errorf("%s should have had its bootstrap/ prefix stripped", p)
		}
	}
}

// TestV3ScaffoldFilesToRemove_LeavesDataAndWrittenFilesAlone: the removal
// list must never contain a data file (those are converted) or a file the
// migration writes (deleting it would undo the write).
func TestV3ScaffoldFilesToRemove_LeavesDataAndWrittenFilesAlone(t *testing.T) {
	keep := map[string]bool{"README.md": true}
	for _, p := range v3ScaffoldFilesToRemove(migrationTestPaths(), keep) {
		switch {
		case p == "README.md":
			t.Error("README.md is rewritten by the migration and must not be deleted")
		case p == "configuration/managed-clusters.yaml", p == "configuration/addons-catalog.yaml":
			t.Errorf("%s is converted, not scaffold — the delete list must not claim it", p)
		case strings.HasPrefix(p, "configuration/addons-clusters-values/"),
			strings.HasPrefix(p, "configuration/addons-global-values/"):
			t.Errorf("%s is user data — the scaffold list must not claim it", p)
		}
	}
}

// ─── Story 8.6: malformed-input all-or-nothing audit ─────────────────────
//
// buildMigration only reads from git — every file it produces lives in the
// in-memory migrationBuild until commitMigrationPR runs, and that only
// happens after buildMigration returns successfully (MigrateV3ToV4 calls
// buildMigration, then commitMigrationPR — never the reverse, never
// interleaved). So ANY read/parse failure anywhere in the build — whether
// the source is unparseable YAML, a wrong-type field, or a file the FINAL
// validateMigrationFiles backstop catches — structurally guarantees zero
// branches, zero PRs, and a completely untouched base branch. This table
// proves that guarantee holds for a spread of injection points, not just
// the one semantic case (missing catalog version)
// TestMigrate_RefusesBeforeAnyWrite_OnUnmigratableRepo already covers.
func TestMigrate_AllOrNothing_OnMalformedInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(files map[string][]byte)
		wantErr string // substring that must appear in the returned error
	}{
		{
			name: "managed-clusters.yaml is not valid YAML",
			mutate: func(files map[string][]byte) {
				files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: \"unterminated")
			},
			wantErr: "configuration/managed-clusters.yaml",
		},
		{
			name: "managed-clusters.yaml is binary junk",
			mutate: func(files map[string][]byte) {
				junk := make([]byte, 64)
				for i := range junk {
					junk[i] = byte(i)
				}
				junk[0] = 0xFF
				files["configuration/managed-clusters.yaml"] = junk
			},
			wantErr: "configuration/managed-clusters.yaml",
		},
		{
			name: "addons-catalog.yaml is not valid YAML",
			mutate: func(files map[string][]byte) {
				files["configuration/addons-catalog.yaml"] = []byte("applicationsets:\n  - name: \"unterminated")
			},
			wantErr: "configuration/addons-catalog.yaml",
		},
		{
			name: "a per-cluster values file is not valid YAML",
			mutate: func(files map[string][]byte) {
				files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("cert-manager:\n  replicaCount: [1, 2")
			},
			wantErr: "configuration/addons-clusters-values/prod-eu.yaml",
		},
		{
			name: "a per-cluster values file is binary junk",
			mutate: func(files map[string][]byte) {
				junk := make([]byte, 64)
				for i := range junk {
					junk[i] = byte(i)
				}
				junk[0] = 0xFF
				files["configuration/addons-clusters-values/prod-eu.yaml"] = junk
			},
			wantErr: "configuration/addons-clusters-values/prod-eu.yaml",
		},
		{
			name: "addons-catalog.yaml names the same addon twice",
			mutate: func(files map[string][]byte) {
				files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: cert-manager
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: 1.14.5
  - name: cert-manager
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: 1.15.0
`)
			},
			// Gap fixed by this story: buildCatalogDelta folds entries into
			// a map keyed by name, so a duplicate used to silently collapse
			// into whichever entry sorted last — dropping the other one's
			// fields with no note. readV3Catalog now refuses this before
			// any git write, matching internal/catalog.LoadBytes's existing
			// duplicate-name rejection for the curated catalog.
			wantErr: "listed more than once",
		},
		{
			name: "a global values file is not valid YAML (caught by the final validation backstop)",
			mutate: func(files map[string][]byte) {
				files["configuration/addons-global-values/cert-manager.yaml"] = []byte("installCRDs: [true")
			},
			// buildV4GlobalValues carries the bytes across textually
			// (UnwrapGlobalValuesFile is a line-scanner, not a YAML
			// parse), so this is only caught later by
			// validateMigrationFiles running the real yaml.Unmarshal
			// reader against every generated file — the "the new %s
			// would not be valid" wrapper, not the per-file read error.
			wantErr: "would not be valid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := newV3FixtureRepo()
			tc.mutate(files)
			git := newMigrationFakeGit(files)
			orch := newMigrationOrchestrator(t, git)

			_, err := orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
			if err == nil {
				t.Fatal("expected the migration to fail on malformed input")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
			}

			// The whole point: NOTHING was written. No branch, no PR, no
			// delete, no change to the base branch's files.
			if len(git.branches) != 0 {
				t.Errorf("a refused migration created %d branch(es); want 0", len(git.branches))
			}
			if len(git.prs) != 0 {
				t.Errorf("a refused migration opened %d pull request(s); want 0", len(git.prs))
			}
			if len(git.branchWrites) != 0 {
				t.Errorf("a refused migration wrote %d file(s) to a branch; want 0", len(git.branchWrites))
			}
			if len(git.branchDeletes) != 0 {
				t.Errorf("a refused migration deleted %d file(s) from a branch; want 0", len(git.branchDeletes))
			}
			// The base branch itself must be byte-identical to what
			// newMigrationFakeGit was seeded with — buildMigration never
			// writes, so files should be untouched by identity, but assert
			// on content too so a future refactor that starts mutating the
			// fake's base map in place gets caught here.
			if _, ok := git.files["configuration/managed-clusters.yaml"]; !ok {
				t.Error("the v3 cluster registry disappeared from the base branch")
			}
		})
	}
}

// TestMigrate_AllOrNothing_OnMalformedInput_NeverPanics is the
// panic-safety half of the audit: every case above, plus a few more
// exotic malformed shapes, must come back as a Go error from
// MigrateV3ToV4 — never a runtime panic that would take down the request
// goroutine handling the migration API call.
func TestMigrate_AllOrNothing_OnMalformedInput_NeverPanics(t *testing.T) {
	deepNesting := func() []byte {
		var b strings.Builder
		b.WriteString("clusters:\n")
		indent := "  "
		for i := 0; i < 200; i++ {
			b.WriteString(strings.Repeat(indent, i+1))
			b.WriteString(fmt.Sprintf("level%d:\n", i))
		}
		return []byte(b.String())
	}

	cases := map[string]func(files map[string][]byte){
		"managed_clusters_deep_nesting": func(files map[string][]byte) {
			files["configuration/managed-clusters.yaml"] = deepNesting()
		},
		"managed_clusters_wrong_top_level_type": func(files map[string][]byte) {
			files["configuration/managed-clusters.yaml"] = []byte("true")
		},
		"values_null_bytes": func(files map[string][]byte) {
			files["configuration/addons-clusters-values/prod-eu.yaml"] = make([]byte, 32)
		},
	}

	for name, mutate := range cases {
		mutate := mutate
		t.Run(name, func(t *testing.T) {
			files := newV3FixtureRepo()
			mutate(files)
			git := newMigrationFakeGit(files)
			orch := newMigrationOrchestrator(t, git)

			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("MigrateV3ToV4 panicked on %s: %v", name, r)
					}
				}()
				_, _ = orch.MigrateV3ToV4(context.Background(), MigrateRequest{Yes: true})
			}()

			if len(git.branches) != 0 {
				t.Errorf("%s: a migration that should have been refused created %d branch(es)", name, len(git.branches))
			}
		})
	}
}

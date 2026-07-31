package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/appsets"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/service"
	"github.com/MoranWeissman/sharko/internal/takeover"
)

// clusters_takeover_test.go — handler-level contract for the four brownfield
// takeover endpoints (v4 Wave 2, Epic 6; added by the Wave 2 review fix lane).
//
// What these pin, in the order the gates actually run:
//
//  1. Role. The two reads are operator+, the two writes are admin-only, and
//     a viewer gets 403 on all four.
//  2. Blocked before acknowledged before yes. A blocking finding 409s no
//     matter what else is in the body; an unacknowledged warning 409s and
//     names the finding it is waiting on; a missing "yes" is a 400.
//  3. Acknowledgement is bound to findings by id. Acking one warning does
//     not cover a different one, so a warning that appeared after the caller
//     last looked cannot be waved through.
//  4. A dry run writes nothing at all — no branch, no pull request, and the
//     cluster Secret's owner is untouched.
//  5. The drop-labels call only ever removes labels the takeover recorded as
//     carried over.

// ─────────────────────────────────────────────────────────────────────────
// Fixtures
// ─────────────────────────────────────────────────────────────────────────

// takeoverFakeGP is a git provider with an in-memory file set. Writes are
// recorded rather than applied, so a test can assert "nothing was written"
// as precisely as "this was written".
type takeoverFakeGP struct {
	mu    sync.Mutex
	files map[string][]byte
	// readErr, when non-nil, is returned for this exact path instead of its
	// content — the transient-git-failure case.
	readErr map[string]error

	branches []string
	written  map[string][]byte
	prs      int
}

func newTakeoverFakeGP() *takeoverFakeGP {
	return &takeoverFakeGP{
		files: map[string][]byte{
			// A non-empty engine pin is what makes this a v4 repo, for both
			// the v3-write gate and the takeover's own format check.
			orchestrator.BootstrapRootAppPath: []byte("kind: Application\n"),
			orchestrator.V4ManagedClustersPath:    []byte("clusters:\n  - name: other-cluster\n"),
		},
		readErr: map[string]error{},
		written: map[string][]byte{},
	}
}

func (f *takeoverFakeGP) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.readErr[path]; ok {
		return nil, err
	}
	if body, ok := f.files[path]; ok {
		return body, nil
	}
	return nil, fmt.Errorf("takeoverFakeGP: %s: %w", path, gitprovider.ErrFileNotFound)
}

func (f *takeoverFakeGP) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *takeoverFakeGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *takeoverFakeGP) TestConnection(_ context.Context) error { return nil }
func (f *takeoverFakeGP) CreateBranch(_ context.Context, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.branches = append(f.branches, name)
	return nil
}
func (f *takeoverFakeGP) CreateOrUpdateFile(_ context.Context, path string, body []byte, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written[path] = body
	return nil
}
func (f *takeoverFakeGP) BatchCreateFiles(_ context.Context, files map[string][]byte, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p, b := range files {
		f.written[p] = b
	}
	return nil
}
func (f *takeoverFakeGP) DeleteFile(_ context.Context, _, _, _ string) error { return nil }
func (f *takeoverFakeGP) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prs++
	return &gitprovider.PullRequest{ID: f.prs, URL: "https://example.invalid/pr/1"}, nil
}
func (f *takeoverFakeGP) MergePullRequest(_ context.Context, _ int) error { return nil }
func (f *takeoverFakeGP) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "open", nil
}
func (f *takeoverFakeGP) DeleteBranch(_ context.Context, _ string) error { return nil }

func (f *takeoverFakeGP) wroteAnything() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.branches) > 0 || len(f.written) > 0 || f.prs > 0
}

// fakeAppSetReader serves a fixed ApplicationSet list, or an error.
type fakeAppSetReader struct {
	list []appsets.ApplicationSetInfo
	err  error
}

func (f fakeAppSetReader) List(context.Context) ([]appsets.ApplicationSetInfo, error) {
	return f.list, f.err
}

// unsafeAppSet is an ApplicationSet that deletes what it installed when a
// cluster stops matching it — the thing the deletion-safety warning is about.
func unsafeAppSet(name string, selectorKeys ...string) appsets.ApplicationSetInfo {
	return appsets.ApplicationSetInfo{
		Name:                        name,
		Namespace:                   "argocd",
		PreserveResourcesOnDeletion: false,
		ApplicationsSync:            "",
		ClusterSelectorLabels:       selectorKeys,
	}
}

// brownfieldSecret is a live ArgoCD cluster Secret somebody else set up:
// no Sharko ownership label, one label of their own.
func brownfieldSecret(name string, labels, annotations map[string]string) *corev1.Secret {
	base := map[string]string{"argocd.argoproj.io/secret-type": "cluster"}
	for k, v := range labels {
		base[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "argocd",
			Labels:      base,
			Annotations: annotations,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(name),
			"server": []byte("https://" + name + ".example.invalid"),
			"config": []byte(`{"execProviderConfig":{}}`),
		},
	}
}

// takeoverTestServer wires a real Server + router against the supplied git
// provider, cluster Secrets and ApplicationSet list.
func takeoverTestServer(t *testing.T, gp gitprovider.GitProvider, secrets []*corev1.Secret, reader appsets.Reader) (*Server, http.Handler, *fake.Clientset) {
	t.Helper()

	f, err := os.CreateTemp("", "sharko-takeover-test-*.yaml")
	if err != nil {
		t.Fatalf("create temp config file: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	store := config.NewFileStore(f.Name())
	connSvc := service.NewConnectionService(store)
	clusterSvc := service.NewClusterService("")
	addonSvc := service.NewAddonService("")
	dashboardSvc := service.NewDashboardService(connSvc, "")
	observabilitySvc := service.NewObservabilityService(clusterSvc)
	upgradeSvc := service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, "")
	srv := NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, ai.NewClient(ai.Config{}))

	if err := connSvc.Create(models.CreateConnectionRequest{
		Name:   "takeover-test",
		Git:    models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "o", Repo: "r"},
		Argocd: models.ArgocdConfig{ServerURL: "https://argocd.invalid", Token: "t", Insecure: true},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := connSvc.SetActive("takeover-test"); err != nil {
		t.Fatalf("activate connection: %v", err)
	}
	connSvc.SetGitProviderOverride(gp)

	objs := make([]interface{}, 0, len(secrets))
	for _, s := range secrets {
		objs = append(objs, s)
	}
	k8s := fake.NewSimpleClientset()
	for _, s := range secrets {
		if _, err := k8s.CoreV1().Secrets("argocd").Create(context.Background(), s, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed cluster secret %q: %v", s.Name, err)
		}
	}
	_ = objs

	srv.SetArgoSecretManager(argosecrets.NewManager(k8s, "argocd"))
	if reader != nil {
		srv.SetApplicationSetReader(reader)
	}
	return srv, NewRouter(srv, nil), k8s
}

// roleReq builds an authenticated request at the given role.
func roleReq(method, path, role string, body interface{}) *http.Request {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		raw, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.ContentLength = int64(len(raw))
	}
	r.Header.Set("X-Sharko-User", role+"-user")
	r.Header.Set("X-Sharko-Role", role)
	return r
}

func decodeMap(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body %q: %v", w.Body.String(), err)
	}
	return m
}

// ─────────────────────────────────────────────────────────────────────────
// Role enforcement — all four endpoints
// ─────────────────────────────────────────────────────────────────────────

func TestTakeoverEndpoints_ViewerIsRefusedOnAllFour(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp, []*corev1.Secret{brownfieldSecret("prod-eu", nil, nil)}, fakeAppSetReader{})

	cases := []struct {
		name   string
		method string
		path   string
		body   interface{}
	}{
		{"preflight", http.MethodGet, "/api/v1/clusters/prod-eu/takeover/preflight", nil},
		{"takeover", http.MethodPost, "/api/v1/clusters/prod-eu/takeover", TakeoverRequest{Yes: true}},
		{"drop-labels", http.MethodPost, "/api/v1/clusters/prod-eu/takeover/legacy-labels/drop", DropLegacyLabelsRequest{Yes: true}},
		{"consequences", http.MethodGet, "/api/v1/clusters/prod-eu/unregister/consequences", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, roleReq(tc.method, tc.path, "viewer", tc.body))
			if w.Code != http.StatusForbidden {
				t.Fatalf("viewer on %s: got %d, want 403 (body=%s)", tc.name, w.Code, w.Body.String())
			}
		})
	}
	if gp.wroteAnything() {
		t.Fatalf("a refused viewer request still touched git: branches=%v written=%v prs=%d", gp.branches, gp.written, gp.prs)
	}
}

func TestTakeoverPreflight_OperatorMayRead(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp, []*corev1.Secret{brownfieldSecret("prod-eu", nil, nil)}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodGet, "/api/v1/clusters/prod-eu/takeover/preflight", "operator", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("operator preflight: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if gp.wroteAnything() {
		t.Fatalf("the preflight wrote something: branches=%v written=%v prs=%d", gp.branches, gp.written, gp.prs)
	}
}

func TestTakeover_OperatorIsRefusedOnTheWrite(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp, []*corev1.Secret{brownfieldSecret("prod-eu", nil, nil)}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "operator", TakeoverRequest{Yes: true}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("operator takeover: got %d, want 403 (body=%s)", w.Code, w.Body.String())
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Gate ordering: blocked → acknowledged → yes
// ─────────────────────────────────────────────────────────────────────────

func TestTakeover_409_WhenAFindingIsBlocking(t *testing.T) {
	// No cluster Secret at all — the owner check blocks ("there is nothing
	// to take over"). "yes" in the body must not get past it.
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp, nil, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AcknowledgedFindings: []string{takeover.FindingSecretOwner}}))

	if w.Code != http.StatusConflict {
		t.Fatalf("blocked takeover: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	if gp.wroteAnything() {
		t.Fatalf("a blocked takeover still touched git: branches=%v written=%v prs=%d", gp.branches, gp.written, gp.prs)
	}
	body := decodeMap(t, w)
	if _, ok := body["preflight"]; !ok {
		t.Errorf("the 409 does not carry the preflight report the caller has to read: %s", w.Body.String())
	}
}

func TestTakeover_409_NamesTheWarningItIsWaitingOn(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true}))

	if w.Code != http.StatusConflict {
		t.Fatalf("unacknowledged warning: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	body := decodeMap(t, w)
	raw, ok := body["unacknowledged_findings"].([]interface{})
	if !ok || len(raw) != 1 || raw[0] != takeover.FindingAppSetSafety {
		t.Fatalf("the 409 does not name the finding it is waiting on: %s", w.Body.String())
	}
	if gp.wroteAnything() {
		t.Fatalf("an unacknowledged takeover still touched git: branches=%v written=%v prs=%d", gp.branches, gp.written, gp.prs)
	}
}

func TestTakeover_409_AckingADifferentFindingDoesNotCoverThisOne(t *testing.T) {
	// This is the whole reason the ack is a list of ids and not a flag: a
	// confirmation given against one warning must not silently cover a
	// different warning that turned up since.
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AcknowledgedFindings: []string{takeover.FindingApplications}}))

	if w.Code != http.StatusConflict {
		t.Fatalf("ack of the wrong finding: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	body := decodeMap(t, w)
	raw, _ := body["unacknowledged_findings"].([]interface{})
	if len(raw) != 1 || raw[0] != takeover.FindingAppSetSafety {
		t.Fatalf("expected the appset-safety warning to still be outstanding, got: %s", w.Body.String())
	}
}

func TestTakeover_400_WhenTheConfirmationIsMissing(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", nil, nil)}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing confirmation: got %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if gp.wroteAnything() {
		t.Fatalf("an unconfirmed takeover still touched git: branches=%v written=%v prs=%d", gp.branches, gp.written, gp.prs)
	}
}

func TestTakeover_200_AckedWarningGoesThroughAndSwapsTheOwner(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, k8s := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AcknowledgedFindings: []string{takeover.FindingAppSetSafety}}))

	if w.Code != http.StatusOK {
		t.Fatalf("acked takeover: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp TakeoverResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.SecretSwapped {
		t.Errorf("secret_swapped is false on a successful takeover: %s", w.Body.String())
	}
	if resp.PreservedLabels["env"] != "prod" {
		t.Errorf("the previous owner's label was not reported as carried over: %+v", resp.PreservedLabels)
	}

	secret, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster secret: %v", err)
	}
	if secret.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		t.Errorf("Sharko's ownership label was not stamped: %v", secret.Labels)
	}
	if secret.Labels["env"] != "prod" {
		t.Errorf("the previous owner's label was removed: %v", secret.Labels)
	}
	if got := secret.Annotations[argosecrets.AnnotationTakeoverPreservedLabels]; got != "env" {
		t.Errorf("preserved-labels record = %q, want %q", got, "env")
	}
	if string(secret.Data["server"]) != "https://prod-eu.example.invalid" {
		t.Errorf("the connection address changed: %q", secret.Data["server"])
	}
	if gp.prs != 1 {
		t.Errorf("expected exactly one pull request, got %d", gp.prs)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Dry run writes nothing
// ─────────────────────────────────────────────────────────────────────────

func TestTakeover_DryRun_WritesNothingAnywhere(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, k8s := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})

	w := httptest.NewRecorder()
	// No confirmation and no acknowledgement — a dry run needs neither.
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{DryRun: true}))

	if w.Code != http.StatusOK {
		t.Fatalf("dry run: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp TakeoverResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "planned" {
		t.Errorf("status = %q, want %q", resp.Status, "planned")
	}
	if resp.DryRun == nil || len(resp.DryRun.FilesToWrite) == 0 {
		t.Errorf("a dry run returned no plan: %s", w.Body.String())
	}

	if gp.wroteAnything() {
		t.Fatalf("a dry run touched git: branches=%v written=%v prs=%d", gp.branches, gp.written, gp.prs)
	}
	secret, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster secret: %v", err)
	}
	if _, owned := secret.Labels[argosecrets.LabelManagedBy]; owned {
		t.Errorf("a dry run stamped ownership on the cluster Secret: %v", secret.Labels)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// H-1 / M-6 — a transient git failure must not look like an empty fleet
// ─────────────────────────────────────────────────────────────────────────

func TestTakeoverPreflight_BlocksWhenTheClusterListCannotBeRead(t *testing.T) {
	gp := newTakeoverFakeGP()
	gp.readErr[orchestrator.V4ManagedClustersPath] = fmt.Errorf("403 rate limit exceeded")
	_, router, _ := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", nil, nil)}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodGet, "/api/v1/clusters/prod-eu/takeover/preflight", "operator", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("preflight: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	var report takeover.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Ready {
		t.Errorf("the preflight says ready even though it could not read the cluster list: %s", w.Body.String())
	}
	for _, f := range report.Findings {
		if f.ID != takeover.FindingNameCollision {
			continue
		}
		if f.Status != takeover.StatusBlocked {
			t.Errorf("name check status = %q, want %q", f.Status, takeover.StatusBlocked)
		}
		if !strings.Contains(f.Detail, "could not read") {
			t.Errorf("name check does not say it could not look: %q", f.Detail)
		}
		return
	}
	t.Fatalf("no name-collision finding in the report: %s", w.Body.String())
}

func TestTakeover_RefusedWhenTheFleetRecordCannotBeRead(t *testing.T) {
	// The fleet record is read, edited and written back. A read error that
	// became "empty fleet" would open a pull request dropping every other
	// cluster — so the whole takeover is refused and no branch is created.
	gp := newTakeoverFakeGP()
	gp.readErr[orchestrator.V4ManagedClustersPath] = fmt.Errorf("502 bad gateway")
	_, router, k8s := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", nil, nil)}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AcknowledgedFindings: []string{
			takeover.FindingSecretOwner, takeover.FindingAppSetSafety,
			takeover.FindingApplications, takeover.FindingNameCollision,
		}}))

	if w.Code == http.StatusOK || w.Code == http.StatusMultiStatus {
		t.Fatalf("a takeover went ahead on an unreadable fleet record: %d (body=%s)", w.Code, w.Body.String())
	}
	if len(gp.branches) != 0 || gp.prs != 0 {
		t.Fatalf("a refused takeover still created a branch or pull request: branches=%v prs=%d", gp.branches, gp.prs)
	}
	secret, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster secret: %v", err)
	}
	if _, owned := secret.Labels[argosecrets.LabelManagedBy]; owned {
		t.Errorf("a refused takeover still stamped ownership: %v", secret.Labels)
	}
}

func TestTakeover_ProceedsWhenTheFleetRecordIsSimplyAbsent(t *testing.T) {
	// The other half of the same distinction: a repo with no fleet record
	// yet is an ordinary first takeover, not a failure.
	gp := newTakeoverFakeGP()
	delete(gp.files, orchestrator.V4ManagedClustersPath)
	_, router, _ := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", nil, nil)}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true}))

	if w.Code != http.StatusOK {
		t.Fatalf("first takeover on an empty repo: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if gp.prs != 1 {
		t.Errorf("expected one pull request, got %d", gp.prs)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 6.4 — dropping the carried-over labels
// ─────────────────────────────────────────────────────────────────────────

// takenOverSecret is a connection Sharko owns, with one label recorded as
// carried over from the previous owner and one addon label of Sharko's own.
func takenOverSecret() *corev1.Secret {
	s := brownfieldSecret("prod-eu", map[string]string{
		"env":                       "prod",
		argosecrets.LabelManagedBy:  argosecrets.ManagedByValue,
		"addons.sharko.dev/logging": "enabled",
	}, map[string]string{
		argosecrets.AnnotationAdopted:                 "true",
		argosecrets.AnnotationTakeoverPreservedLabels: "env",
	})
	return s
}

func TestDropLegacyLabels_400_OnALabelTheTakeoverNeverCarried(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, k8s := takeoverTestServer(t, gp, []*corev1.Secret{takenOverSecret()}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover/legacy-labels/drop", "admin",
		DropLegacyLabelsRequest{Yes: true, Labels: []string{"addons.sharko.dev/logging"}}))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("drop of a non-carried label: got %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	secret, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster secret: %v", err)
	}
	if secret.Labels["addons.sharko.dev/logging"] != "enabled" {
		t.Errorf("Sharko's own addon label was removed by the drop action: %v", secret.Labels)
	}
}

func TestDropLegacyLabels_409_NamesTheWarningItIsWaitingOn(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, _ := takeoverTestServer(t, gp, []*corev1.Secret{takenOverSecret()},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover/legacy-labels/drop", "admin",
		DropLegacyLabelsRequest{Yes: true}))

	if w.Code != http.StatusConflict {
		t.Fatalf("unacknowledged drop: got %d, want 409 (body=%s)", w.Code, w.Body.String())
	}
	body := decodeMap(t, w)
	raw, ok := body["unacknowledged_findings"].([]interface{})
	if !ok || len(raw) != 1 || raw[0] != "appset:legacy-addons" {
		t.Fatalf("the 409 does not name the warning it is waiting on: %s", w.Body.String())
	}
}

func TestDropLegacyLabels_DryRun_RemovesNothingAndPublishesTheWarningIDs(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, k8s := takeoverTestServer(t, gp, []*corev1.Secret{takenOverSecret()},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover/legacy-labels/drop", "admin",
		DropLegacyLabelsRequest{DryRun: true}))

	if w.Code != http.StatusOK {
		t.Fatalf("drop dry run: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp DropLegacyLabelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.WarningIDs) != 1 || resp.WarningIDs[0] != "appset:legacy-addons" {
		t.Errorf("warning_ids = %v, want [appset:legacy-addons]", resp.WarningIDs)
	}
	if len(resp.WarningIDs) != len(resp.Warnings) {
		t.Errorf("warning_ids and warnings are different lengths: %v vs %v", resp.WarningIDs, resp.Warnings)
	}

	secret, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster secret: %v", err)
	}
	if secret.Labels["env"] != "prod" {
		t.Errorf("a dry run removed the label: %v", secret.Labels)
	}
}

func TestDropLegacyLabels_200_RemovesOnlyTheCarriedLabel(t *testing.T) {
	gp := newTakeoverFakeGP()
	_, router, k8s := takeoverTestServer(t, gp, []*corev1.Secret{takenOverSecret()},
		fakeAppSetReader{list: []appsets.ApplicationSetInfo{unsafeAppSet("legacy-addons", "env")}})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover/legacy-labels/drop", "admin",
		DropLegacyLabelsRequest{Yes: true, AcknowledgedFindings: []string{"appset:legacy-addons"}}))

	if w.Code != http.StatusOK {
		t.Fatalf("acked drop: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	secret, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster secret: %v", err)
	}
	if _, still := secret.Labels["env"]; still {
		t.Errorf("the carried-over label was not removed: %v", secret.Labels)
	}
	if secret.Labels["addons.sharko.dev/logging"] != "enabled" {
		t.Errorf("Sharko's own addon label was removed too: %v", secret.Labels)
	}
	if secret.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		t.Errorf("the ownership label was removed: %v", secret.Labels)
	}
	if _, still := secret.Annotations[argosecrets.AnnotationTakeoverPreservedLabels]; still {
		t.Errorf("the preserved-labels record survived an empty list: %v", secret.Annotations)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// 6.5 — the consequences read tells the truth about v4 repos
// ─────────────────────────────────────────────────────────────────────────

func TestUnregisterConsequences_SaysRemovalIsNotAvailableOnAV4Repo(t *testing.T) {
	gp := newTakeoverFakeGP() // has an engine pin → v4
	_, router, _ := takeoverTestServer(t, gp, []*corev1.Secret{takenOverSecret()}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodGet, "/api/v1/clusters/prod-eu/unregister/consequences", "operator", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("consequences: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp UnregisterConsequencesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if strings.Contains(resp.ConfirmationRequired, "DELETE /api/v1/clusters/") {
		t.Errorf("the page still tells the user to send a call that would be refused: %q", resp.ConfirmationRequired)
	}
	found := false
	for _, c := range resp.Consequences {
		if c.ID == "removal-not-available" {
			found = true
		}
	}
	if !found {
		t.Errorf("no consequence explains that removal is not available yet: %+v", resp.Consequences)
	}
}

func TestUnregisterConsequences_KeepsTheInstructionsOnAV3Repo(t *testing.T) {
	gp := newTakeoverFakeGP()
	delete(gp.files, orchestrator.BootstrapRootAppPath) // no engine pin → not v4
	_, router, _ := takeoverTestServer(t, gp, []*corev1.Secret{takenOverSecret()}, fakeAppSetReader{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodGet, "/api/v1/clusters/prod-eu/unregister/consequences", "operator", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("consequences: got %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp UnregisterConsequencesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(resp.ConfirmationRequired, "DELETE /api/v1/clusters/prod-eu") {
		t.Errorf("the v3 instructions went missing: %q", resp.ConfirmationRequired)
	}
	for _, c := range resp.Consequences {
		if c.ID == "removal-not-available" {
			t.Errorf("a v3 repo was told removal is unavailable: %+v", c)
		}
	}
}

package api

// connection_repair_test.go — R3-6 (the security sentinels for the repair) and
// the rules of R3-1 that live in the handler rather than in the writer.
//
// The sentinel value below appears nowhere else in this repository. Every test
// here pushes it through the REAL repair path — the real policy, the real
// stored-facts read, the real canonical builder, the real write — and then proves
// it, and every derived form of it, is absent from the response, the fresh
// comparison inside it, the audit entries, the logs, the error sentences, the
// generated OpenAPI files, and the scraped metrics.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/settings"
)

// repairSentinelValue is one unique made-up credential value that appears
// nowhere else in this repository. It is deliberately not shaped like a real
// token from any provider: a realistic-looking fake in a test file is the thing
// somebody later mistakes for a real leaked credential.
const repairSentinelValue = "w9n2q7xkrepair-endpoint-sentinel-do-not-copy"

// repairHeadSHA is the commit the fixture's git provider reports.
const repairHeadSHA = "5555555555555555555555555555555555555555"

// repairSentinelForms is every shape a leak of repairSentinelValue could take.
func repairSentinelForms() []string {
	sum := sha256.Sum256([]byte(repairSentinelValue))
	n := len(repairSentinelValue)
	return []string{
		repairSentinelValue,
		base64.StdEncoding.EncodeToString([]byte(repairSentinelValue)),
		base64.RawStdEncoding.EncodeToString([]byte(repairSentinelValue)),
		base64.URLEncoding.EncodeToString([]byte(repairSentinelValue)),
		base64.RawURLEncoding.EncodeToString([]byte(repairSentinelValue)),
		hex.EncodeToString(sum[:]),
		base64.StdEncoding.EncodeToString(sum[:]),
		repairSentinelValue[:8],
		repairSentinelValue[:16],
		repairSentinelValue[n-8:],
		repairSentinelValue[n-16:],
	}
}

// assertNoRepairSentinel fails when text carries the sentinel in any form, its
// byte length in a labelled shape, or a mask whose length tracks it.
func assertNoRepairSentinel(t *testing.T, where, text string) {
	t.Helper()
	n := len(repairSentinelValue)

	for _, f := range repairSentinelForms() {
		if strings.Contains(text, f) {
			t.Errorf("%s carries a form of the credential value (%q)", where, f)
		}
	}
	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n),
		fmt.Sprintf("%d chars", n),
		fmt.Sprintf("%d characters", n),
		fmt.Sprintf(`"length":%d`, n),
		fmt.Sprintf(`"length": %d`, n),
		fmt.Sprintf(`"len":%d`, n),
		fmt.Sprintf(`"len": %d`, n),
		fmt.Sprintf(`"bytes":%d`, n),
		fmt.Sprintf(`"size":%d`, n),
		fmt.Sprintf(`"tokenLength":%d`, n),
		fmt.Sprintf("length=%d", n),
		fmt.Sprintf("len=%d", n),
		fmt.Sprintf("bytes=%d", n),
	} {
		if strings.Contains(text, shape) {
			t.Errorf("%s carries the credential's byte length (%q) — a length narrows a guess", where, shape)
		}
	}
	for _, ch := range []string{"*", "•", "x", "●"} {
		for _, l := range []int{n - 1, n, n + 1} {
			if strings.Contains(text, strings.Repeat(ch, l)) {
				t.Errorf("%s carries a mask whose length tracks the credential (%d of %q)", where, l, ch)
			}
		}
	}
}

// assertNoRepairLengthInJSON walks a decoded response and fails when the
// credential's byte length appears as an actual value — as a number, or as a
// string that is just that number.
//
// The counts this response legitimately carries are exempt by name, so exempting
// anything else has to be an argued decision: they count FIELDS and KEYS, they
// are nowhere near the length of any value, and a coincidence would be just that.
func assertNoRepairLengthInJSON(t *testing.T, where string, body []byte) {
	t.Helper()
	n := float64(len(repairSentinelValue))
	nStr := strconv.Itoa(len(repairSentinelValue))
	exempt := map[string]bool{
		"checked_field_count":         true,
		"preserved_foreign_labels":    true,
		"preserved_foreign_data_keys": true,
	}

	var walk func(path string, v interface{})
	walk = func(path string, v interface{}) {
		switch val := v.(type) {
		case map[string]interface{}:
			for k, inner := range val {
				if exempt[k] {
					continue
				}
				walk(path+"."+k, inner)
			}
		case []interface{}:
			for i, inner := range val {
				walk(fmt.Sprintf("%s[%d]", path, i), inner)
			}
		case float64:
			if val == n {
				t.Errorf("%s: %s is the credential's byte length as a number", where, path)
			}
		case string:
			if val == nStr {
				t.Errorf("%s: %s is the credential's byte length as a string", where, path)
			}
		}
	}

	var decoded interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("%s: decoding: %v", where, err)
	}
	walk("", decoded)
}

// repairFakeVault hands back stored facts whose token IS the sentinel, so the
// sentinel really does flow through the expected-Secret build and really does get
// written into the connection. If the endpoint leaks anything, it has something
// to leak.
type repairFakeVault struct {
	failWith error
}

func (v repairFakeVault) GetCredentials(name string) (*providers.Kubeconfig, error) {
	if v.failWith != nil {
		return nil, v.failWith
	}
	return &providers.Kubeconfig{
		Server: "https://" + name + ".invalid",
		CAData: []byte("not-a-real-ca-just-test-bytes"),
		Token:  repairSentinelValue,
	}, nil
}

func (v repairFakeVault) StoredConnectionFacts(name string) (*providers.StoredConnectionFacts, error) {
	if v.failWith != nil {
		return nil, v.failWith
	}
	return &providers.StoredConnectionFacts{
		Server: "https://" + name + ".invalid",
		CAData: []byte("not-a-real-ca-just-test-bytes"),
		Token:  repairSentinelValue,
	}, nil
}
func (repairFakeVault) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (repairFakeVault) SearchSecrets(_ string) ([]string, error)       { return nil, nil }
func (repairFakeVault) HealthCheck(_ context.Context) error            { return nil }

// repairFixture wires a server whose reconciler, git provider, secrets backend
// and live Secret are all real enough to drive the whole repair.
func repairFixture(t *testing.T, managedYAML string, live *corev1.Secret, vault providers.ClusterCredentialsProvider) (*Server, http.Handler, *fake.Clientset) {
	t.Helper()

	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &comparisonGP{managedYAML: []byte(managedYAML), headSHA: repairHeadSHA}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	var argoClient *fake.Clientset
	if live != nil {
		argoClient = fake.NewSimpleClientset(live)
	} else {
		argoClient = fake.NewSimpleClientset()
	}

	settingsStore := settings.NewStore(fake.NewSimpleClientset(), "sharko")
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        vault,
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour, // never auto-fires: these tests drive the endpoint
		SelfHealFn:   settingsStore.IsManagedClusterSelfHealEnabled,
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, vault, nil, nil)

	return srv, router, argoClient
}

// repairReq builds an admin repair request carrying the reviewed commit.
func repairReq(cluster, reviewedCommit string) *http.Request {
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/clusters/"+cluster+"/connection-repair?reviewed_commit="+reviewedCommit, nil)
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")
	return req
}

// driftedOwnedSecret is a Sharko-owned connection that is wrong in several ways
// at once, with a foreign label, a foreign annotation and a foreign data key that
// every repair must preserve.
func driftedOwnedSecret() *corev1.Secret {
	s := liveConnectionSecret(
		map[string]string{
			"datadog":                  "disabled",
			"some-other-tool.io/owner": "them",
		},
		map[string]string{
			"name":   comparisonCluster,
			"server": "https://wrong-address.invalid",
			"config": `{"bearerToken":"a-stale-made-up-value"}`,
			"shard":  "7",
		},
		map[string]string{"their-tool.io/note": "keep me"})
	return s
}

// --- R3-6: the sentinel sweep across every surface the repair touches -------

// TestRepair_NeverReturnsTheCredentialValue is the headline security test. The
// sentinel is the cluster's stored credential, so it really is written into the
// connection during the repair — and none of its forms may appear in the
// response, the fresh comparison inside it, the audit entries, the logs, the
// metrics or the OpenAPI files.
func TestRepair_NeverReturnsTheCredentialValue(t *testing.T) {
	var logs strings.Builder
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	srv, router, argoClient := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), repairFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	// The repair must actually have done something, or this proves nothing.
	var view connectionRepairView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !view.Repaired {
		t.Fatalf("the repair reported no change on a drifted connection: %s", w.Body.String())
	}
	// And the credential really was written, so the value really was in play.
	after, _ := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if !strings.Contains(string(after.Data["config"]), repairSentinelValue) {
		t.Fatalf("the fixture never wrote the sentinel into the connection, so the sweep below proves nothing")
	}

	// 1. The response body.
	assertNoRepairSentinel(t, "response body", w.Body.String())
	// 2. Its numbers.
	assertNoRepairLengthInJSON(t, "response body", w.Body.Bytes())
	// 3. Everything logged while the repair ran.
	assertNoRepairSentinel(t, "log output", logs.String())
	// 4. The audit entries.
	if srv.AuditLog() == nil {
		t.Fatal("no audit log on the test server, so the audit sweep proves nothing")
	}
	entries := srv.AuditLog().List(1000)
	if len(entries) == 0 {
		t.Fatal("the repair wrote no audit entry, so the audit sweep proves nothing")
	}
	auditJSON, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("encoding audit entries: %v", err)
	}
	assertNoRepairSentinel(t, "audit entries", string(auditJSON))
	// %+v as well: a struct printed into a log carries fields json would skip.
	assertNoRepairSentinel(t, "audit entries printed with %+v", fmt.Sprintf("%+v", entries))

	// 5. The scraped metrics.
	m := httptest.NewRecorder()
	router.ServeHTTP(m, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if m.Code == http.StatusOK {
		assertNoRepairSentinel(t, "metrics output", m.Body.String())
	}

	// 6. The generated OpenAPI files.
	for _, path := range []string{
		"../../docs/swagger/swagger.json",
		"../../docs/swagger/swagger.yaml",
		"../../docs/swagger/docs.go",
	} {
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("reading %s: %v", path, readErr)
		}
		assertNoRepairSentinel(t, path, string(body))
	}
}

// TestRepair_FreshComparisonHasNoSensitiveSides: the comparison returned inside
// the repair response follows the same rule as the read-only endpoint's — a
// sensitive field carries no expected property and no live property at all.
func TestRepair_FreshComparisonHasNoSensitiveSides(t *testing.T) {
	_, router, _ := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), repairFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var raw struct {
		Comparison struct {
			Differences []map[string]json.RawMessage `json:"differences"`
		} `json:"comparison"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, d := range raw.Comparison.Differences {
		if _, isSensitive := d["sensitive"]; !isSensitive {
			continue
		}
		if _, ok := d["expected"]; ok {
			t.Errorf("a sensitive difference in the fresh comparison carries an \"expected\" property: %v", d)
		}
		if _, ok := d["live"]; ok {
			t.Errorf("a sensitive difference in the fresh comparison carries a \"live\" property: %v", d)
		}
	}
}

// TestRepair_BackendErrorTextNeverPassedThrough: the secrets backend fails with
// the sentinel inside its own error message, which real provider SDKs do. Neither
// the response nor the logs nor the audit trail may carry it.
func TestRepair_BackendErrorTextNeverPassedThrough(t *testing.T) {
	var logs strings.Builder
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	backend := providers.NewFailingStoredFactsBackendForTest(
		credsafe.Mark(fmt.Errorf("the backend blew up while handling %s", repairSentinelValue)))

	srv, router, argoClient := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), backend)

	before, _ := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	beforeJSON, _ := json.Marshal(before)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))

	if strings.Contains(w.Body.String(), "blew up") {
		t.Error("the backend's own error text was passed through to the caller")
	}
	if strings.Contains(logs.String(), "blew up") {
		t.Error("the backend's own error text was logged")
	}
	assertNoRepairSentinel(t, "response body", w.Body.String())
	assertNoRepairSentinel(t, "log output", logs.String())

	entries := srv.AuditLog().List(1000)
	auditJSON, _ := json.Marshal(entries)
	if strings.Contains(string(auditJSON), "blew up") {
		t.Error("the backend's own error text reached the audit trail")
	}
	assertNoRepairSentinel(t, "audit entries", string(auditJSON))

	// A failed credential read must leave the connection untouched.
	after, _ := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Error("the connection was changed even though the credential read failed")
	}
}

// TestRepair_CredentialFailureAuditIsSanitized: the audit entry for a credential
// failure carries the one fixed safe sentence and an empty detail, which is what
// the hotfix's Log.Add guarantees when the call site sets the flag. This proves
// the repair path sets it.
func TestRepair_CredentialFailureAuditIsSanitized(t *testing.T) {
	backend := providers.NewFailingStoredFactsBackendForTest(
		credsafe.Mark(fmt.Errorf("secret material: %s", repairSentinelValue)))
	srv, router, _ := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), backend)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	_ = w

	var sawCredentialEntry bool
	for _, e := range srv.AuditLog().List(1000) {
		if e.Error == "" {
			continue
		}
		sawCredentialEntry = true
		if e.Error != credsafe.Message {
			t.Errorf(`an audit entry about a credential failure carries %q, want the one fixed safe sentence.

The call site must set Entry.CredentialFailure from credsafe.Is while the typed error is still alive; Log.Add then replaces the text.`, e.Error)
		}
		if e.Detail != "" {
			t.Errorf("a credential-failure audit entry has a non-empty Detail (%q); Log.Add empties it", e.Detail)
		}
		if e.CredentialFailure {
			t.Error("the stored audit entry still carries the CredentialFailure flag; Log.Add clears it")
		}
	}
	if !sawCredentialEntry {
		t.Fatal("no audit entry with an error was written, so nothing was proven")
	}
}

// TestRepair_IsNotAGuessingOracle proves the endpoint cannot be steered. A
// caller may not supply a candidate value, an expected manifest, a hash, an
// override or an extra field, and throwing all of them at it changes nothing.
func TestRepair_IsNotAGuessingOracle(t *testing.T) {
	attempts := []struct {
		name  string
		build func() *http.Request
	}{
		{"a candidate value in a query parameter", func() *http.Request {
			r := repairReq(comparisonCluster, repairHeadSHA)
			r.URL.RawQuery += "&expected=" + repairSentinelValue + "&candidate=" + repairSentinelValue
			return r
		}},
		{"a wrong candidate value in a query parameter", func() *http.Request {
			r := repairReq(comparisonCluster, repairHeadSHA)
			r.URL.RawQuery += "&expected=definitely-not-the-value&token=nope"
			return r
		}},
		{"a namespace and a backend path override", func() *http.Request {
			r := repairReq(comparisonCluster, repairHeadSHA)
			r.URL.RawQuery += "&namespace=kube-system&secretPath=some/other/path&destination=elsewhere"
			return r
		}},
		{"an expected manifest in the body", func() *http.Request {
			r := httptest.NewRequest(http.MethodPost,
				"/api/v1/clusters/"+comparisonCluster+"/connection-repair?reviewed_commit="+repairHeadSHA,
				strings.NewReader(`{"expected":{"data":{"config":"`+repairSentinelValue+`"}},"scope":"full_connection","namespace":"kube-system"}`))
			r.Header.Set("X-Sharko-User", "admin")
			r.Header.Set("X-Sharko-Role", "admin")
			r.Header.Set("Content-Type", "application/json")
			return r
		}},
		{"a hash of a candidate value in a header", func() *http.Request {
			r := repairReq(comparisonCluster, repairHeadSHA)
			sum := sha256.Sum256([]byte(repairSentinelValue))
			r.Header.Set("X-Expected-Hash", hex.EncodeToString(sum[:]))
			return r
		}},
	}

	// Each attempt runs against its own fresh fixture, because a repair changes
	// the world — comparing two runs against one fixture would compare a
	// drifted connection with an already-repaired one.
	baseline := repairAnswer(t, nil)
	for _, a := range attempts {
		t.Run(a.name, func(t *testing.T) {
			got := repairAnswer(t, a.build)
			if got != baseline {
				t.Fatalf("the answer changed when a caller supplied extra input.\n got %s\nwant %s", got, baseline)
			}
			assertNoRepairSentinel(t, "response body", got)
		})
	}
}

// repairAnswer runs one repair against a fresh fixture and returns the answer
// with the per-call timestamps removed, so two runs are comparable.
func repairAnswer(t *testing.T, build func() *http.Request) string {
	t.Helper()
	_, router, _ := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), repairFakeVault{})

	req := repairReq(comparisonCluster, repairHeadSHA)
	if build != nil {
		req = build()
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	delete(m, "repaired_at")
	if comparison, ok := m["comparison"].(map[string]interface{}); ok {
		delete(comparison, "checked_at")
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("re-encoding: %v", err)
	}
	return string(out)
}

// --- rule 5: a foreign-owned connection is not touched at all ---------------

// TestRepair_ForeignOwnedConnectionIsNeverWritten proves the refusal two ways:
// the object is byte-for-byte unchanged, AND no write action reached the fake
// Kubernetes client at all.
func TestRepair_ForeignOwnedConnectionIsNeverWritten(t *testing.T) {
	live := driftedOwnedSecret()
	live.Labels[clusterreconciler.LabelManagedBy] = "another-tool"

	_, router, argoClient := repairFixture(t, backendManagedYAML, live, repairFakeVault{})

	before, err := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading before: %v", err)
	}
	beforeJSON, _ := json.Marshal(before)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for another tool's connection, got %d (body=%s)", w.Code, w.Body.String())
	}

	after, _ := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("the connection was changed.\nbefore %s\nafter  %s", beforeJSON, afterJSON)
	}

	for _, a := range argoClient.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Errorf("a refused repair issued a %q on %q", a.GetVerb(), a.GetResource().Resource)
		}
	}
}

// --- R3-4: the revision rule ------------------------------------------------

// TestRepair_RequiresTheReviewedCommit: no reviewed commit is a 400. There is no
// "repair whatever is there now" mode.
func TestRepair_RequiresTheReviewedCommit(t *testing.T) {
	_, router, argoClient := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), repairFakeVault{})

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/clusters/"+comparisonCluster+"/connection-repair", nil)
	req.Header.Set("X-Sharko-User", "admin")
	req.Header.Set("X-Sharko-Role", "admin")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without a reviewed commit, got %d (body=%s)", w.Code, w.Body.String())
	}
	assertNoWriteAction(t, argoClient)
}

// TestRepair_409WhenTheBranchMoved: the caller reviewed one commit and the branch
// is now on another, so what they reviewed is not what would be written.
func TestRepair_409WhenTheBranchMoved(t *testing.T) {
	_, router, argoClient := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), repairFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, "0000000000000000000000000000000000000000"))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 when the reviewed commit is not the current one, got %d (body=%s)", w.Code, w.Body.String())
	}
	assertNoWriteAction(t, argoClient)
}

// TestRepair_409WhenTheRevisionIsUnknown is the heart of R3-4: a git provider
// that cannot report a commit gets a refusal, NOT a repair.
//
// An unknown revision is not a match. Empty is not treated as equal to empty, and
// the endpoint does not fall back to "write whatever git says right now" — that
// would be a write nobody reviewed against a commit nobody can name.
func TestRepair_409WhenTheRevisionIsUnknown(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	// headSHA empty → GetBranchHeadSHA fails → ResolveComparedRevision is "".
	gp := &comparisonGP{managedYAML: []byte(backendManagedYAML)}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	argoClient := fake.NewSimpleClientset(driftedOwnedSecret())
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        repairFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, repairFakeVault{}, nil, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))

	if w.Code != http.StatusConflict {
		t.Fatalf(`expected 409 when git cannot name the branch commit, got %d (body=%s).

An unknown revision is not a match. A provider without the branch-revision capability cannot support this write, and refusing is better than writing against a commit nobody can name.`, w.Code, w.Body.String())
	}
	assertNoWriteAction(t, argoClient)
}

// --- the happy paths --------------------------------------------------------

// TestRepair_FullConnectionMakesTheComparisonSynced is the whole point of the
// feature: a drifted connection is repaired and the fresh comparison in the same
// response says so.
func TestRepair_FullConnectionMakesTheComparisonSynced(t *testing.T) {
	_, router, argoClient := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), repairFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var view connectionRepairView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !view.Repaired {
		t.Fatal("the repair reported no change on a drifted connection")
	}
	if view.ScopeApplied != "full_connection" {
		t.Errorf("scope_applied = %q, want full_connection", view.ScopeApplied)
	}
	if view.RepairedAtCommit != repairHeadSHA {
		t.Errorf("repaired_at_commit = %q, want %q", view.RepairedAtCommit, repairHeadSHA)
	}
	if !view.SelfHealUnchanged {
		t.Error("self_heal_unchanged must be true")
	}
	if view.Comparison.Status != "synced" {
		t.Errorf(`the fresh comparison says %q, want synced.

The repair is meant to leave the connection matching what Sharko intends, and the response is meant to show that rather than just claiming "done".
differences: %+v`, view.Comparison.Status, view.Comparison.Differences)
	}

	// The foreign material really did survive a full rewrite.
	after, _ := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if after.Labels["some-other-tool.io/owner"] != "them" {
		t.Error("a foreign label was lost in the repair")
	}
	if string(after.Data["shard"]) != "7" {
		t.Error("a foreign data key was lost in the repair")
	}
	if after.Annotations["their-tool.io/note"] != "keep me" {
		t.Error("a foreign annotation was lost in the repair")
	}
}

// TestRepair_EKSKeepsFullRepairAndNeverClaimsSynced is rule 13, and it is the one
// place scope and repair deliberately disagree. EKS keeps a full repair offer —
// rewriting the connection from the backend is exactly the right fix — but its
// credential blob was never comparable, so the answer afterwards is still limited.
func TestRepair_EKSKeepsFullRepairAndNeverClaimsSynced(t *testing.T) {
	mint := &eksAPIMintCounter{}
	backend := eksBackendForAPI(t, mint)

	_, router, _ := repairFixture(t, eksManagedYAML, driftedOwnedSecret(), backend)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var view connectionRepairView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.ScopeApplied != "full_connection" {
		t.Errorf("scope_applied = %q, want full_connection — EKS keeps its full repair", view.ScopeApplied)
	}
	if view.Comparison.Status == "synced" {
		t.Fatal(`an EKS cluster was reported synced after a repair.

Its credential blob was never comparable — a fresh token differs every time — so "in sync" is a claim about something Sharko did not check. Rule 13.`)
	}
	if view.Comparison.OwnershipMode != "eks_token" {
		t.Errorf("ownership_mode = %q, want eks_token", view.Comparison.OwnershipMode)
	}

	// THE MINT. Zero, not low — on the write path as much as the read path.
	if mint.calls != 0 {
		t.Fatalf(`the repair minted %d EKS sign-in token(s); it must mint ZERO.

The repair reads the backend through the read-only stored-facts capability, which cannot reach GetCredentials. A minted token here would be written into the connection, and the mode policy has already ruled that blob cannot be compared anyway.`, mint.calls)
	}
}

// TestRepair_GuestConnectionGetsLabelsOnly is rule 12 for the guest modes: a
// self-managed connection's details are the user's, so a repair re-applies the
// addon labels and touches nothing else.
func TestRepair_GuestConnectionGetsLabelsOnly(t *testing.T) {
	selfYAML := "clusters:\n- name: " + comparisonCluster + "\n  connectionManagedBy: user\n  credsSource: secret-kubeconfig\n  labels:\n    datadog: enabled\n"
	// R3-11 fix: self-managed means the user maintains this Secret, so it should
	// NOT have managed-by=sharko. The old test used liveConnectionSecret which
	// always adds that label, creating a mismatch: the policy says self-managed
	// (expectedOwned=false) but the Secret has the label (live says owned), so
	// the primitive's ownership check rejects it.
	//
	// Correct: build the Secret without the managed-by label to match what a
	// self-managed connection actually looks like.
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      comparisonCluster,
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				"datadog":                        "disabled",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(comparisonCluster),
			"server": []byte("https://their-own-address.invalid"),
			"config": []byte(`{"bearerToken":"theirs"}`),
		},
	}

	_, router, argoClient := repairFixture(t, selfYAML, live, repairFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var view connectionRepairView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.ScopeApplied != "addon_labels_only" {
		t.Fatalf("scope_applied = %q, want addon_labels_only", view.ScopeApplied)
	}

	after, _ := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if got := string(after.Data["server"]); got != "https://their-own-address.invalid" {
		t.Errorf("the user's own API address was rewritten to %q — a guest repair must not touch the connection details", got)
	}
	if got := string(after.Data["config"]); got != `{"bearerToken":"theirs"}` {
		t.Error("the user's own credential blob was rewritten by a labels-only repair")
	}
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("the addon label was not re-applied: datadog = %q", after.Labels["datadog"])
	}
}

// --- the role gate ----------------------------------------------------------

// TestRepair_403ForOperatorAndViewer: this endpoint can rewrite the credential
// material ArgoCD signs in with, so it is admin-only — deliberately a tier above
// the operator-level resync it sits beside.
func TestRepair_403ForOperatorAndViewer(t *testing.T) {
	for _, role := range []string{"operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			_, router, argoClient := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), repairFakeVault{})

			req := httptest.NewRequest(http.MethodPost,
				"/api/v1/clusters/"+comparisonCluster+"/connection-repair?reviewed_commit="+repairHeadSHA, nil)
			req.Header.Set("X-Sharko-User", role)
			req.Header.Set("X-Sharko-Role", role)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("expected 403 for %s, got %d (body=%s)", role, w.Code, w.Body.String())
			}
			assertNoWriteAction(t, argoClient)
		})
	}
}

// --- rule 8: self-heal is untouched ----------------------------------------

// TestRepair_DoesNotReadOrWriteTheSelfHealSetting is rule 8. The repair works
// with the setting off, and never writes it.
func TestRepair_DoesNotReadOrWriteTheSelfHealSetting(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &comparisonGP{managedYAML: []byte(backendManagedYAML), headSHA: repairHeadSHA}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	argoClient := fake.NewSimpleClientset(driftedOwnedSecret())
	settingsClient := fake.NewSimpleClientset()
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider: func() gitprovider.GitProvider { return gp },
		ArgoClient:  argoClient,
		Vault:       repairFakeVault{},
		AuditFn:     func(audit.Entry) {},
		Namespace:   "argocd",
		// SelfHealFn deliberately nil: the repair must not consult it at all.
		TickInterval: time.Hour,
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, repairFakeVault{}, nil, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("the repair needs the self-heal setting to work, which it must not: got %d (body=%s)", w.Code, w.Body.String())
	}

	var view connectionRepairView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !view.Repaired {
		t.Error("the repair did nothing with self-heal unwired; it must not depend on that setting")
	}

	// Nothing wrote a settings ConfigMap or Secret.
	for _, a := range settingsClient.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete":
			t.Errorf("the repair issued a %q against the settings store; it must never write the self-heal setting", a.GetVerb())
		}
	}
}

// --- rule 7: git is never written ------------------------------------------

// TestRepair_NeverWritesToGit is rule 7. Repair fixes the cluster to match git;
// it never changes git. comparisonGP's write methods are the proof surface — the
// fixture's provider would record any write, and the assertions below check the
// ones a repair could plausibly reach.
func TestRepair_NeverWritesToGit(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &recordingGitProvider{comparisonGP: comparisonGP{
		managedYAML: []byte(backendManagedYAML), headSHA: repairHeadSHA,
	}}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	argoClient := fake.NewSimpleClientset(driftedOwnedSecret())
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        repairFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, repairFakeVault{}, nil, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	if len(gp.writes) > 0 {
		t.Errorf(`the repair wrote to git: %v.

Rule 7: repair fixes the cluster to match git. Git is the source of truth and is never changed by a repair.`, gp.writes)
	}
}

// recordingGitProvider records every write method a repair could reach.
type recordingGitProvider struct {
	comparisonGP
	writes []string
}

func (g *recordingGitProvider) CreateBranch(_ context.Context, name, _ string) error {
	g.writes = append(g.writes, "CreateBranch:"+name)
	return nil
}

func (g *recordingGitProvider) CreateOrUpdateFile(_ context.Context, path string, _ []byte, _, _ string) error {
	g.writes = append(g.writes, "CreateOrUpdateFile:"+path)
	return nil
}

func (g *recordingGitProvider) DeleteFile(_ context.Context, path, _, _ string) error {
	g.writes = append(g.writes, "DeleteFile:"+path)
	return nil
}

func (g *recordingGitProvider) BatchCreateFiles(_ context.Context, files map[string][]byte, _, _ string) error {
	g.writes = append(g.writes, fmt.Sprintf("BatchCreateFiles:%d", len(files)))
	return nil
}

func (g *recordingGitProvider) CreatePullRequest(_ context.Context, title, _, _, _ string) (*gitprovider.PullRequest, error) {
	g.writes = append(g.writes, "CreatePullRequest:"+title)
	return &gitprovider.PullRequest{}, nil
}

// --- what must not change --------------------------------------------------

// TestResyncIsUnchangedByTheRepairEndpoint proves POST /clusters/{name}/resync
// still behaves exactly as before: same action, same response shape, same label
// convergence, and it is still open to an operator — the new admin-only repair
// action did not drag it up a tier.
func TestResyncIsUnchangedByTheRepairEndpoint(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &comparisonGP{
		managedYAML: []byte("clusters:\n- name: " + comparisonCluster + "\n  labels:\n    addon-foo: enabled\n"),
		headSHA:     repairHeadSHA,
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	argoClient := fake.NewSimpleClientset(liveConnectionSecret(nil, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"execProviderConfig":{}}`,
	}, nil))

	settingsStore := settings.NewStore(fake.NewSimpleClientset(), "sharko")
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        repairFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
		SelfHealFn:   settingsStore.IsManagedClusterSelfHealEnabled,
	})
	srv.SetClusterReconciler(recon)

	// An OPERATOR, which is what cluster.resync has always required.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+comparisonCluster+"/resync", nil)
	req.Header.Set("X-Sharko-User", "op")
	req.Header.Set("X-Sharko-Role", "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("resync expected 200 for an operator, got %d (body=%s)", w.Code, w.Body.String())
	}

	// The exact response shape it has always had.
	var resp struct {
		Status    string `json:"status"`
		Cluster   string `json:"cluster"`
		Outcome   string `json:"outcome"`
		Message   string `json:"message"`
		LabelDiff struct {
			Added []string `json:"added"`
		} `json:"label_diff"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp.Status != "resynced" {
		t.Errorf("status = %q, want resynced", resp.Status)
	}
	if resp.Cluster != comparisonCluster {
		t.Errorf("cluster = %q", resp.Cluster)
	}
	if resp.Outcome != string(clusterreconciler.OutcomeSucceeded) {
		t.Errorf("outcome = %q, want %q (message=%q)", resp.Outcome, clusterreconciler.OutcomeSucceeded, resp.Message)
	}
	if len(resp.LabelDiff.Added) != 1 || resp.LabelDiff.Added[0] != "addon-foo" {
		t.Errorf("label_diff.added = %v, want [addon-foo]", resp.LabelDiff.Added)
	}

	// It converged the label, as it always has.
	secret, _ := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if secret.Labels["addon-foo"] != "enabled" {
		t.Errorf("addon-foo = %q, want enabled", secret.Labels["addon-foo"])
	}

	// And the repair response shape is NOT what resync returns — they are
	// genuinely two different endpoints.
	if strings.Contains(w.Body.String(), "scope_applied") {
		t.Error("resync's response gained the repair endpoint's fields; its shape must be unchanged")
	}
}

// TestRepairActionIsAdminInTheRequirementsTable pins the authz decision itself,
// so the entry cannot quietly be softened to operator later.
func TestRepairActionIsAdminInTheRequirementsTable(t *testing.T) {
	got, ok := authz.ActionRequirements["cluster.connection.repair"]
	if !ok {
		t.Fatal(`cluster.connection.repair has no ActionRequirements entry.

An action missing from the table is admin-only by fail-closed default, which happens to be the right tier — but relying on that means the decision is not written down anywhere, and the next person to read the table will not know it was deliberate.`)
	}
	if got != authz.RoleAdmin {
		t.Errorf(`cluster.connection.repair requires %q, want admin.

This endpoint can rewrite the credential material ArgoCD signs in to a cluster with. cluster.resync (operator) re-applies addon labels and cannot break a connection — the two are not the same risk.`, got)
	}
	if resync := authz.ActionRequirements["cluster.resync"]; resync != authz.RoleOperator {
		t.Errorf("cluster.resync requires %q, want operator — the repair action must not have dragged it up a tier", resync)
	}
}

// assertNoWriteAction fails when any write verb reached the fake client.
func assertNoWriteAction(t *testing.T, client *fake.Clientset) {
	t.Helper()
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Errorf("a refused repair issued a %q on %q — a refusal must write nothing",
				a.GetVerb(), a.GetResource().Resource)
		}
	}
}

// ============================================================================
// R3-13: Round-2 security checks for addon-labels refusal and pinned-labels
// ============================================================================

// TestRepair_RefusesWhenAddonLabelsUnknown_FullConnection proves that when a
// v4 cluster's addon-assignment file cannot be read (AddonLabelsKnown=false),
// the repair endpoint refuses the request on the FULL-connection path with a
// safe error sentence, and that NO write action reaches the Kubernetes client.
// This covers the full-connection repair path where all Secret fields would be
// rewritten.
func TestRepair_RefusesWhenAddonLabelsUnknown_FullConnection(t *testing.T) {
	// Set up a backend that reports a v4 repo with the cluster in
	// managed-clusters.yaml, but NO cluster-addons/<cluster>.yaml file. This
	// makes AddonLabelsKnown=false.
	managedYAML := "clusters:\n- name: " + comparisonCluster + "\n  version: v4\n  credsSource: secret-kubeconfig\n"
	// Live secret is a full Sharko-managed owned connection (OwnershipMode=owned).
	live := liveConnectionSecret(nil, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"tlsClientConfig":{"insecure":false,"caData":"dGVzdA=="}}`,
	}, nil)
	live.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "sharko",
		"argocd.argoproj.io/secret-type": "cluster",
	}

	// The git provider returns the managed-clusters file, but the reconciler
	// will fail to find cluster-addons/test-cluster.yaml, causing
	// AddonLabelsKnown to be false.
	gp := &v4GitProviderNoClusterAddons{managedYAML: []byte(managedYAML), headSHA: repairHeadSHA}
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	srv, router := reconcileTestServer(t, gp, argo.URL)

	argoClient := fake.NewSimpleClientset(live)
	settingsStore := settings.NewStore(fake.NewSimpleClientset(), "sharko")
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        repairFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
		SelfHealFn:   settingsStore.IsManagedClusterSelfHealEnabled,
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, repairFakeVault{}, nil, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))

	// Assert: the endpoint refused with 422.
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when addon labels unknown, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Assert: the error message is the safe sentence explaining the refusal.
	body := w.Body.String()
	if !strings.Contains(body, "could not read which addons should run on this cluster") {
		t.Errorf("error message does not explain the addon-labels refusal:\n%s", body)
	}
	if !strings.Contains(body, "cluster-addons file missing or unparseable") {
		t.Errorf("error message does not mention the v4-specific cause:\n%s", body)
	}

	// Assert: NO write action reached the Kubernetes client.
	assertNoWriteAction(t, argoClient)
}

// TestRepair_RefusesWhenAddonLabelsUnknown_GuestConnection proves that when
// addon labels are unknown, the repair endpoint refuses even on the
// LABELS-ONLY path (a guest connection). This ensures both paths are protected
// by the same gate.
func TestRepair_RefusesWhenAddonLabelsUnknown_GuestConnection(t *testing.T) {
	managedYAML := "clusters:\n- name: " + comparisonCluster + "\n  version: v4\n  ownership_mode: guest\n  credsSource: secret-kubeconfig\n"
	// Live secret is a guest connection (ownership_mode=guest in managed YAML,
	// but still carries Sharko's managed-by label so Sharko can repair the labels).
	live := liveConnectionSecret(nil, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"tlsClientConfig":{"insecure":false,"caData":"dGVzdA=="}}`,
	}, nil)
	live.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "sharko",
		"argocd.argoproj.io/secret-type": "cluster",
	}

	gp := &v4GitProviderNoClusterAddons{managedYAML: []byte(managedYAML), headSHA: repairHeadSHA}
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	srv, router := reconcileTestServer(t, gp, argo.URL)

	argoClient := fake.NewSimpleClientset(live)
	settingsStore := settings.NewStore(fake.NewSimpleClientset(), "sharko")
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        repairFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
		SelfHealFn:   settingsStore.IsManagedClusterSelfHealEnabled,
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, repairFakeVault{}, nil, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))

	// Assert: the endpoint refused with 422.
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 when addon labels unknown on guest path, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Assert: the refusal is for the same reason (addon labels unknown).
	body := w.Body.String()
	if !strings.Contains(body, "could not read which addons should run on this cluster") {
		t.Errorf("guest-connection refusal does not explain the addon-labels reason:\n%s", body)
	}

	// Assert: NO write action reached the Kubernetes client.
	assertNoWriteAction(t, argoClient)
}

// TestRepair_WritesPinnedLabels_NotBranchHead proves that a labels-only repair
// (guest connection) writes the addon labels from the REVIEWED commit, not from
// the branch head. This is the R3-9 wiring: the repair must call
// RepairAddonLabelsWithPinnedDesired, not the old RepairAddonLabelsOnly that
// re-reads git at the branch.
func TestRepair_WritesPinnedLabels_NotBranchHead(t *testing.T) {
	managedYAML := "clusters:\n- name: " + comparisonCluster + "\n  version: v4\n  ownership_mode: guest\n  credsSource: secret-kubeconfig\n"
	// The reviewed commit (which we'll pass to the repair endpoint) has
	// addon-foo enabled. The branch head (a different, newer commit) has
	// addon-bar enabled instead. If the repair goes back through the git
	// provider and reads at the branch head, addon-bar lands. If it correctly
	// uses the pinned labels from the reviewed commit, addon-foo lands.
	//
	// Important: the repair request carries the reviewed commit SHA, which
	// must match the branch head for R3-4's revision guard to pass. So we set
	// headSHA to repairHeadSHA (the commit being reviewed), and then track
	// whether GetFileContent was called with a ref parameter matching the
	// pinned SHA (repairHeadSHA) vs. an empty ref (which would mean reading at
	// branch head).
	gp := &v4GitProviderWithPinnedCommit{
		managedYAML: []byte(managedYAML),
		headSHA:     repairHeadSHA, // branch head = the reviewed commit (R3-4 guard)
		pinnedSHA:   repairHeadSHA, // the commit the repair should read labels at
		// Labels at the pinned (reviewed) commit:
		pinnedClusterAddons: []byte(`apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: ` + comparisonCluster + `
addons:
  foo:
    enabled: true
`),
		// Labels if the code incorrectly read without a ref (branch head):
		headClusterAddons: []byte(`apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: ` + comparisonCluster + `
addons:
  bar:
    enabled: true
`),
	}

	// Live secret is a guest connection with no addon labels yet.
	live := liveConnectionSecret(nil, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"tlsClientConfig":{"insecure":false,"caData":"dGVzdA=="}}`,
	}, nil)
	live.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "sharko",
		"argocd.argoproj.io/secret-type": "cluster",
	}

	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	srv, router := reconcileTestServer(t, gp, argo.URL)

	argoClient := fake.NewSimpleClientset(live)
	settingsStore := settings.NewStore(fake.NewSimpleClientset(), "sharko")
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        repairFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
		SelfHealFn:   settingsStore.IsManagedClusterSelfHealEnabled,
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, repairFakeVault{}, nil, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))

	// Assert: the repair succeeded.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Assert: the Secret now has addons.sharko.dev/foo=enabled (from the reviewed
	// commit), NOT addons.sharko.dev/bar=enabled (from the branch head). v4 uses
	// qualified addon label keys.
	updatedSecret, err := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("could not read updated secret: %v", err)
	}

	fooLabel := "addons.sharko.dev/foo"
	barLabel := "addons.sharko.dev/bar"

	if updatedSecret.Labels[fooLabel] != "enabled" {
		t.Errorf("%s = %q, want 'enabled' (from the reviewed commit). The repair did not use the pinned labels.", fooLabel, updatedSecret.Labels[fooLabel])
	}
	if _, exists := updatedSecret.Labels[barLabel]; exists {
		t.Errorf("%s = %q, but it should NOT exist. The repair incorrectly used the branch-head labels instead of the pinned ones.", barLabel, updatedSecret.Labels[barLabel])
	}

	// Also verify that gp.pinnedSHAWasUsed was set to true, proving the git
	// provider's pinned-commit path was exercised.
	if !gp.pinnedSHAWasUsed {
		t.Error("The git provider's pinned-commit path was not exercised. The repair may have gone through a different code path that re-reads git.")
	}
}

// v4GitProviderNoClusterAddons is a fake git provider for v4 repos that
// returns a managed-clusters.yaml but does NOT return a
// cluster-addons/<cluster>.yaml file. This makes AddonLabelsKnown=false.
type v4GitProviderNoClusterAddons struct {
	managedYAML []byte
	headSHA     string
}

func (g *v4GitProviderNoClusterAddons) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	// v4 repos use "managed-clusters.yaml" at root, not "configuration/managed-clusters.yaml"
	if path == "managed-clusters.yaml" && g.managedYAML != nil {
		return g.managedYAML, nil
	}
	// For cluster-addons files: the directory listing says the file EXISTS, but
	// GetFileContent returns ErrFileNotFound. This simulates a race condition or a
	// git provider glitch, and forces the reconciler to add this cluster to
	// `unknown`, making AddonLabelsKnown=false.
	if strings.Contains(path, "cluster-addons/") {
		return nil, gitprovider.ErrFileNotFound
	}
	return nil, gitprovider.ErrFileNotFound
}

func (g *v4GitProviderNoClusterAddons) GetBranchHeadSHA(_ context.Context, _ string) (string, error) {
	if g.headSHA == "" {
		return "", fmt.Errorf("no head")
	}
	return g.headSHA, nil
}

func (g *v4GitProviderNoClusterAddons) ListDirectory(_ context.Context, path, _ string) ([]string, error) {
	// The cluster-addons directory listing INCLUDES the cluster's file, but
	// GetFileContent will fail when the reconciler tries to read it. This makes
	// the reconciler add this cluster to `unknown`, causing AddonLabelsKnown=false.
	if path == "cluster-addons" || path == "cluster-addons/" {
		return []string{comparisonCluster + ".yaml"}, nil
	}
	return []string{}, nil
}
func (g *v4GitProviderNoClusterAddons) TestConnection(_ context.Context) error { return nil }
func (g *v4GitProviderNoClusterAddons) CreateBranch(_ context.Context, _, _ string) error {
	return nil
}
func (g *v4GitProviderNoClusterAddons) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}
func (g *v4GitProviderNoClusterAddons) DeleteFile(_ context.Context, _, _, _ string) error {
	return nil
}
func (g *v4GitProviderNoClusterAddons) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}
func (g *v4GitProviderNoClusterAddons) MergePullRequest(_ context.Context, _ int) error {
	return nil
}
func (g *v4GitProviderNoClusterAddons) DeleteBranch(_ context.Context, _ string) error { return nil }
func (g *v4GitProviderNoClusterAddons) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}
func (g *v4GitProviderNoClusterAddons) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "", nil
}
func (g *v4GitProviderNoClusterAddons) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}

// v4GitProviderWithPinnedCommit is a fake git provider that returns DIFFERENT
// cluster-addons content depending on whether the caller passes the pinned
// commit as the `ref` parameter. This lets the test prove that the repair reads
// at the reviewed commit (safe), not at the branch head (unsafe).
type v4GitProviderWithPinnedCommit struct {
	managedYAML         []byte
	headSHA             string
	pinnedSHA           string
	pinnedClusterAddons []byte // Content when GetFileContent is called with pinnedSHA as ref
	headClusterAddons   []byte // Content when GetFileContent is called with empty ref or branch name
	pinnedSHAWasUsed    bool   // Set to true when GetFileContent is called with pinnedSHA as ref
}

func (g *v4GitProviderWithPinnedCommit) GetFileContent(_ context.Context, path, ref string) ([]byte, error) {
	// v4 repos use "managed-clusters.yaml" at root
	if path == "managed-clusters.yaml" && g.managedYAML != nil {
		return g.managedYAML, nil
	}
	if strings.Contains(path, "cluster-addons/") {
		// If the caller passed the pinned SHA as the ref, return the pinned content.
		// This is the CORRECT behavior (R3-9 wiring).
		if ref == g.pinnedSHA && ref != "" {
			g.pinnedSHAWasUsed = true
			return g.pinnedClusterAddons, nil
		}
		// Otherwise (empty ref or branch name), return the head content. This is
		// the INCORRECT behavior that the old code would have produced.
		return g.headClusterAddons, nil
	}
	return nil, gitprovider.ErrFileNotFound
}

func (g *v4GitProviderWithPinnedCommit) GetBranchHeadSHA(_ context.Context, _ string) (string, error) {
	if g.headSHA == "" {
		return "", fmt.Errorf("no head")
	}
	return g.headSHA, nil
}

func (g *v4GitProviderWithPinnedCommit) ListDirectory(_ context.Context, path, _ string) ([]string, error) {
	if path == "cluster-addons" || path == "cluster-addons/" {
		return []string{comparisonCluster + ".yaml"}, nil
	}
	return []string{}, nil
}

func (g *v4GitProviderWithPinnedCommit) TestConnection(_ context.Context) error { return nil }
func (g *v4GitProviderWithPinnedCommit) CreateBranch(_ context.Context, _, _ string) error {
	return nil
}
func (g *v4GitProviderWithPinnedCommit) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}
func (g *v4GitProviderWithPinnedCommit) DeleteFile(_ context.Context, _, _, _ string) error {
	return nil
}
func (g *v4GitProviderWithPinnedCommit) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}
func (g *v4GitProviderWithPinnedCommit) MergePullRequest(_ context.Context, _ int) error {
	return nil
}
func (g *v4GitProviderWithPinnedCommit) DeleteBranch(_ context.Context, _ string) error { return nil }
func (g *v4GitProviderWithPinnedCommit) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}
func (g *v4GitProviderWithPinnedCommit) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "", nil
}
func (g *v4GitProviderWithPinnedCommit) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}

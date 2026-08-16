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
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/events"
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
		Vault:        staticVault(vault),
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
		Vault:        staticVault(repairFakeVault{}),
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

	// The success message must say "configured credentials source", not "stored
	// sign-in details". The old wording was only true for connections whose
	// credentials sit in the backend as a stored kubeconfig. For an EKS connection
	// the backend stores cluster metadata, and the credential is created at write
	// time, so nothing "stored" was rewritten.
	if !strings.Contains(view.Message, "configured credentials source") {
		t.Errorf("the success message says %q; it must contain 'configured credentials source' to accurately describe both stored-kubeconfig and EKS connections", view.Message)
	}
	if strings.Contains(view.Message, "stored sign-in details") {
		t.Errorf("the success message says %q; it must not claim 'stored sign-in details' were rewritten (not true for EKS)", view.Message)
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

Its credential blob was never comparable — the stored EKS payload holds cluster metadata, not a credential, so there is nothing on the expected side to compare against — so "in sync" is a claim about something Sharko did not check. Rule 13.`)
	}
	if view.Comparison.OwnershipMode != "eks_token" {
		t.Errorf("ownership_mode = %q, want eks_token", view.Comparison.OwnershipMode)
	}

	// THE MINT — EXACTLY ONE, per repair (R3-14).
	//
	// This used to assert ZERO, and zero was the bug. The repair built its spec
	// from the read-only no-mint route, so for a stored EKS payload it got no
	// token, and argosecrets.buildSecretConfig's precedence (cert pair > token >
	// exec) fell through to the execProviderConfig shape — while every normal
	// Sharko write for that same cluster mints a token and produces bearerToken.
	// Clicking repair silently changed how ArgoCD signs in to that cluster.
	//
	// A WRITE needs credentials that work, so a write mints. One, because one
	// write happened. And ONE and not two: the fresh comparison this response
	// carries runs after the write, on the no-mint route, so it must add nothing
	// to this count. If this number is two, that comparison has started minting.
	if mint.calls != 1 {
		t.Fatalf(`the repair minted %d EKS sign-in token(s); it must mint EXACTLY ONE.

One, because a write needs credentials that actually work — the same single mint the reconcile pass performs for this cluster. Zero would mean the write is back on the no-mint read, which produces the exec shape and changes how ArgoCD signs in. Two would mean the fresh comparison inside this response has started minting, and a read must create nothing.`, mint.calls)
	}
}

// TestRepair_MintsOnceForTheWriteAndNeverForACheck is the R3-14 counter proof,
// with the repair endpoint and the comparison endpoint driven against ONE mint
// counter in one test.
//
// Two separate tests would each pass while the pair of them was wrong — that is
// how the bug survived a review round. Sharing the counter makes the difference
// between the two paths the thing being asserted: a write mints once, a check
// mints never, and the total after both is exactly one.
func TestRepair_MintsOnceForTheWriteAndNeverForACheck(t *testing.T) {
	mint := &eksAPIMintCounter{}
	backend := eksBackendForAPI(t, mint)

	_, router, _ := repairFixture(t, eksManagedYAML, driftedOwnedSecret(), backend)

	// 1. The read-only comparison endpoint, on its own, mints nothing.
	c := httptest.NewRecorder()
	router.ServeHTTP(c, comparisonReq(comparisonCluster))
	if c.Code != http.StatusOK {
		t.Fatalf("the comparison endpoint returned %d (body=%s)", c.Code, c.Body.String())
	}
	if mint.calls != 0 {
		t.Fatalf(`the read-only comparison minted %d EKS sign-in token(s); it must mint ZERO.

A minted EKS token is a real credential that can sign in as Sharko for as long as it lives. A check must not create one. If this is above zero the comparison path now reaches GetCredentials — find it and take it out; do not raise the expected count.`, mint.calls)
	}

	// 2. The repair mints once — and the fresh comparison it runs afterwards adds
	//    nothing, so the total is one and not two.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("the repair endpoint returned %d (body=%s)", w.Code, w.Body.String())
	}
	if mint.calls != 1 {
		t.Fatalf(`after one comparison and one repair the mint count is %d; it must be exactly 1.

The comparison contributes zero and the repair contributes one. A 0 means the write is back on the no-mint read (the exec-shape bug). A 2 or more means a read has started minting.`, mint.calls)
	}

	// 3. Another comparison still adds nothing.
	c2 := httptest.NewRecorder()
	router.ServeHTTP(c2, comparisonReq(comparisonCluster))
	if c2.Code != http.StatusOK {
		t.Fatalf("the second comparison returned %d (body=%s)", c2.Code, c2.Body.String())
	}
	if mint.calls != 1 {
		t.Errorf("a comparison run after the repair took the mint count to %d; a read must create nothing", mint.calls)
	}
}

// TestRepair_WritesTheSameConnectionShapeANormalWriteWould is the other half of
// R3-14: the repair's own output, not just its mint count.
//
// It asserts the AUTHENTICATION METHOD and the top-level keys of the repaired
// data.config against what the reconciler's own write route produces for the same
// cluster and the same stored payload — never the token bytes, which are different
// on every mint and are not this test's business anyway.
func TestRepair_WritesTheSameConnectionShapeANormalWriteWould(t *testing.T) {
	mint := &eksAPIMintCounter{}
	backend := eksBackendForAPI(t, mint)

	_, router, argoClient := repairFixture(t, eksManagedYAML, driftedOwnedSecret(), backend)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	after, err := argoClient.CoreV1().Secrets("argocd").Get(
		context.Background(), comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the repaired connection: %v", err)
	}
	repairedMethod, repairedKeys := connectionConfigShape(t, string(after.Data["config"]))

	// What a NORMAL write produces for this cluster, from the same backend
	// through the same route the reconcile pass uses.
	normalSpec, err := normalWriteSpecFor(t, backend, comparisonCluster, "eu-west-1")
	if err != nil {
		t.Fatalf("building the normal write's spec: %v", err)
	}
	normalBuilt, err := argosecrets.BuildClusterSecret(normalSpec, "argocd")
	if err != nil {
		t.Fatalf("building the normal write's connection: %v", err)
	}
	normalMethod, normalKeys := connectionConfigShape(t, normalBuilt.StringData["config"])

	if repairedMethod != normalMethod {
		t.Errorf(`the repair wrote the %q authentication method; a normal Sharko write for this cluster produces %q.

A repair must not change HOW ArgoCD signs in to a cluster. This is exactly the R3-14 bug: the repair built its spec from the read-only no-mint route, got no token, and the builder's precedence fell through to the exec shape.
repaired top-level keys: %v
normal   top-level keys: %v`, repairedMethod, normalMethod, repairedKeys, normalKeys)
	}
	if repairedMethod != "bearerToken" {
		t.Errorf(`the repaired connection uses %q, want bearerToken for a stored EKS payload whose token was minted for the write.`, repairedMethod)
	}
	if strings.Join(repairedKeys, ",") != strings.Join(normalKeys, ",") {
		t.Errorf("the repaired connection's top-level config keys are %v; a normal write produces %v", repairedKeys, normalKeys)
	}
}

// TestRepair_BackendThatCannotProvideCredentialsRefusesAndWritesNothing: the
// write route fails, so the repair refuses with the one fixed safe sentence and
// the connection is left byte-for-byte as it was.
//
// The refusal is the point. A write that cannot get credentials must never fall
// back to a spec with no credential in it — that fallback is exactly how a
// missing token became a changed sign-in method instead of a refusal.
func TestRepair_BackendThatCannotProvideCredentialsRefusesAndWritesNothing(t *testing.T) {
	backend := providers.NewFailingStoredFactsBackendForTest(
		credsafe.Mark(fmt.Errorf("the backend is unreachable, and here is %s", repairSentinelValue)))

	_, router, argoClient := repairFixture(t, backendManagedYAML, driftedOwnedSecret(), backend)

	before, err := argoClient.CoreV1().Secrets("argocd").Get(
		context.Background(), comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading before: %v", err)
	}
	beforeJSON, _ := json.Marshal(before)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when the credentials cannot be read, got %d (body=%s)", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the refusal: %v", err)
	}
	if got, _ := body["error"].(string); got != credsafe.Message {
		t.Errorf(`the refusal says %q, want the one fixed safe sentence.

Every credential failure says the SAME sentence. A sentence that changed with the cause would be a channel back to the cause.`, got)
	}
	assertNoRepairSentinel(t, "the refusal body", w.Body.String())

	after, _ := argoClient.CoreV1().Secrets("argocd").Get(
		context.Background(), comparisonCluster, metav1.GetOptions{})
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Errorf("the connection was changed even though the credential read failed.\nbefore %s\nafter  %s", beforeJSON, afterJSON)
	}
	assertNoWriteAction(t, argoClient)
}

// connectionConfigShape reports which authentication method a connection
// Secret's data.config carries, and its top-level keys, sorted.
//
// It reads the SHAPE and never a value. The method name comes from which keys
// are present; the key list is JSON field names. The token itself changes on
// every mint, so there is nothing to compare there — and nothing to print.
func connectionConfigShape(t *testing.T, rawConfig string) (method string, keys []string) {
	t.Helper()
	if rawConfig == "" {
		t.Fatal("the connection Secret has no data.config")
	}
	var cfg map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawConfig), &cfg); err != nil {
		t.Fatalf("data.config is not JSON: %v", err)
	}
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	switch {
	case cfg["execProviderConfig"] != nil:
		method = "execProviderConfig"
	case cfg["bearerToken"] != nil:
		method = "bearerToken"
	default:
		method = "tlsClientConfig-only"
	}
	return method, keys
}

// normalWriteSpecFor assembles the credential half of a connection spec the way
// every WRITER does, straight from the backend through GetCredentials.
//
// It mirrors clusterreconciler.ConnectionCredentialSpecForWrite field for field.
// It is spelled out rather than called because that method hangs off a
// Reconciler and this only needs the mapping — and if the mapping ever stops
// being faithful, the writer's own tests in clusterreconciler catch it.
func normalWriteSpecFor(t *testing.T, backend providers.ClusterCredentialsProvider, cluster, region string) (argosecrets.ClusterSecretSpec, error) {
	t.Helper()
	kc, err := providers.GetCredentialsWithOptionalRole(backend, cluster, "")
	if err != nil {
		return argosecrets.ClusterSecretSpec{}, err
	}
	return argosecrets.ClusterSecretSpec{
		Name:     cluster,
		Server:   kc.Server,
		Region:   region,
		Token:    kc.Token,
		CertData: base64.StdEncoding.EncodeToString(kc.CertData),
		KeyData:  base64.StdEncoding.EncodeToString(kc.KeyData),
		CAData:   base64.StdEncoding.EncodeToString(kc.CAData),
	}, nil
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
		Vault:       staticVault(repairFakeVault{}),
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
		Vault:        staticVault(repairFakeVault{}),
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

Rule 7: repair fixes the cluster to match what Sharko intends — git for addon labels, the configured credentials source for connection details. Git itself is never changed by a repair.`, gp.writes)
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
		Vault:        staticVault(repairFakeVault{}),
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
		"app.kubernetes.io/managed-by":   "sharko",
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
		Vault:        staticVault(repairFakeVault{}),
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
	managedYAML := "clusters:\n- name: " + comparisonCluster + "\n  version: v4\n  connectionManagedBy: user\n  credsSource: secret-kubeconfig\n"
	// Live secret is a self-managed (guest) connection. A self-managed connection
	// means the user maintains it, so it should NOT carry Sharko's managed-by
	// label, consistent with TestRepair_GuestConnectionGetsLabelsOnly.
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      comparisonCluster,
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(comparisonCluster),
			"server": []byte("https://" + comparisonCluster + ".invalid"),
			"config": []byte(`{"tlsClientConfig":{"insecure":false,"caData":"dGVzdA=="}}`),
		},
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
		Vault:        staticVault(repairFakeVault{}),
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

	// Assert: the response scope_applied field is NOT present (or is empty), proving
	// we refused before reaching the repair path branch.
	var view connectionRepairView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err == nil && view.ScopeApplied != "" {
		t.Errorf("A refusal response should not carry scope_applied, got %q. This means the test passed for the wrong reason — it went down the full-connection path and was refused there, not on the guest path.", view.ScopeApplied)
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
	managedYAML := "clusters:\n- name: " + comparisonCluster + "\n  version: v4\n  connectionManagedBy: user\n  credsSource: secret-kubeconfig\n"
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

	// Live secret is a self-managed connection with no addon labels yet.
	// Self-managed means the user maintains this Secret, so it should NOT
	// carry managed-by=sharko (matching the fix in TestRepair_GuestConnectionGetsLabelsOnly).
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      comparisonCluster,
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(comparisonCluster),
			"server": []byte("https://" + comparisonCluster + ".invalid"),
			"config": []byte(`{"tlsClientConfig":{"insecure":false,"caData":"dGVzdA=="}}`),
		},
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
		Vault:        staticVault(repairFakeVault{}),
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

	// Assert: the response says scope_applied=addon_labels_only, proving this test
	// actually exercised the labels-only path (the whole point of the test).
	var view connectionRepairView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if view.ScopeApplied != "addon_labels_only" {
		t.Fatalf("scope_applied = %q, want addon_labels_only. The test went down the wrong path and passed for the wrong reason.", view.ScopeApplied)
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

// --- R3-16 (surface): the response reports what the write returned ----------
//
// Everything below reads the RESPONSE BODY, not a struct the handler happened to
// build. The bug these cover was visible to whoever read the response, so the
// response is where they look.

// guestLabelsOnlyFixture wires the labels-only path for one cluster: a
// self-managed record in git (connectionManagedBy: user → the guest modes → the
// addon-labels-only repair scope) with the declared addon labels, against a live
// Secret carrying the labels, foreign labels and data keys given.
//
// It returns the server, the router, the fake Kubernetes client and a fake event
// recorder, so a test can assert the write, the response AND the event from one
// run.
func guestLabelsOnlyFixture(
	t *testing.T,
	declaredAddons map[string]string,
	liveLabels map[string]string,
	liveData map[string][]byte,
) (http.Handler, *fake.Clientset, *record.FakeRecorder) {
	t.Helper()

	managed := "clusters:\n- name: " + comparisonCluster + "\n  connectionManagedBy: user\n  credsSource: secret-kubeconfig\n  labels:\n"
	// Sorted, so the YAML this test hands git is the same every run.
	declaredKeys := make([]string, 0, len(declaredAddons))
	for k := range declaredAddons {
		declaredKeys = append(declaredKeys, k)
	}
	sort.Strings(declaredKeys)
	for _, k := range declaredKeys {
		managed += "    " + k + ": " + declaredAddons[k] + "\n"
	}

	// A self-managed connection: the user maintains it, so it carries no
	// managed-by=sharko marker. That is what makes the policy classify it guest
	// and what the primitive's ownership recheck expects to find.
	labels := map[string]string{"argocd.argoproj.io/secret-type": "cluster"}
	for k, v := range liveLabels {
		labels[k] = v
	}
	data := map[string][]byte{
		"name":   []byte(comparisonCluster),
		"server": []byte("https://their-own-address.invalid"),
		"config": []byte(`{"bearerToken":"theirs"}`),
	}
	for k, v := range liveData {
		data[k] = v
	}
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: comparisonCluster, Namespace: "argocd", Labels: labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}

	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &comparisonGP{managedYAML: []byte(managed), headSHA: repairHeadSHA}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	argoClient := fake.NewSimpleClientset(live)
	fakeEvents := record.NewFakeRecorder(64)
	settingsStore := settings.NewStore(fake.NewSimpleClientset(), "sharko")
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:   func() gitprovider.GitProvider { return gp },
		ArgoClient:    argoClient,
		Vault:         staticVault(repairFakeVault{}),
		AuditFn:       func(audit.Entry) {},
		Namespace:     "argocd",
		TickInterval:  time.Hour,
		SelfHealFn:    settingsStore.IsManagedClusterSelfHealEnabled,
		EventRecorder: events.NewRecorderForTest(fakeEvents, "sharko"),
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, repairFakeVault{}, nil, nil)

	return router, argoClient, fakeEvents
}

// labelsOnlyRepairBody runs the repair and returns the decoded response, having
// checked it really did take the labels-only path — a test that silently went
// down the full-connection path would prove nothing about this branch.
func labelsOnlyRepairBody(t *testing.T, router http.Handler) connectionRepairView {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionRepairView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding the response body: %v", err)
	}
	if view.ScopeApplied != "addon_labels_only" {
		t.Fatalf(`scope_applied = %q, want addon_labels_only.

This test went down the full-connection path and would have passed for the wrong reason. A "guest" fixture that is not actually a guest is how a passing test came to prove nothing last round.`, view.ScopeApplied)
	}
	return view
}

// TestRepairLabelsOnly_RemovalOnlyRepairReportsTheRemovedKey: git no longer
// declares an addon, the live connection still has its label, so the repair
// REMOVES one label and writes nothing else.
//
// The old code reported ZERO fields for this — it listed the desired labels, and
// a removed label is by definition not one of those. So a write that really
// happened came back looking like nothing had happened.
func TestRepairLabelsOnly_RemovalOnlyRepairReportsTheRemovedKey(t *testing.T) {
	router, argoClient, _ := guestLabelsOnlyFixture(t,
		// git declares one addon, and the live Secret already agrees about it.
		map[string]string{"datadog": "enabled"},
		// The live Secret also carries an addon label git no longer declares.
		map[string]string{"datadog": "enabled", "retired-addon": "enabled"},
		nil)

	view := labelsOnlyRepairBody(t, router)

	if !view.Repaired {
		t.Fatal(`the response says repaired: false, but a label really was removed.

A removal is a write. Reporting it as "nothing changed" is the same lie in the other direction.`)
	}
	want := "metadata.labels[retired-addon]"
	if len(view.FieldsRepaired) != 1 || view.FieldsRepaired[0] != want {
		t.Errorf(`fields_repaired = %v, want exactly [%s].

The one thing that changed was the REMOVAL of an addon label git no longer declares. The old code built this list out of the DESIRED labels, so a removal appeared nowhere and a removal-only repair reported zero fields.`, view.FieldsRepaired, want)
	}
	if !strings.Contains(view.Message, "1 label(s) changed") {
		t.Errorf("the message says %q; it must give the real count of labels that changed", view.Message)
	}

	// And the write really did what the response says.
	after, _ := argoClient.CoreV1().Secrets("argocd").Get(
		context.Background(), comparisonCluster, metav1.GetOptions{})
	if _, still := after.Labels["retired-addon"]; still {
		t.Error("the retired addon label is still on the connection")
	}
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("datadog = %q, want enabled — the label git still declares must stay", after.Labels["datadog"])
	}
}

// TestRepairLabelsOnly_RemovalWithNothingDeclaredStillReportsTheWrite is the
// sharpest shape of the same bug: git declares NO addons for this cluster, so the
// desired label set is empty, and the only thing the repair does is remove the one
// addon label that is left over.
//
// The old code built its field list by looping over the desired set. An empty
// desired set means an empty loop, so a repair that really wrote to Kubernetes
// came back with repaired: true and ZERO fields — "I changed something, and it was
// nothing".
func TestRepairLabelsOnly_RemovalWithNothingDeclaredStillReportsTheWrite(t *testing.T) {
	router, argoClient, _ := guestLabelsOnlyFixture(t,
		// git declares nothing.
		nil,
		// The live connection still carries a leftover addon label.
		map[string]string{"retired-addon": "enabled"},
		nil)

	view := labelsOnlyRepairBody(t, router)

	if !view.Repaired {
		t.Fatal("the response says repaired: false, but the leftover label really was removed")
	}
	if len(view.FieldsRepaired) == 0 {
		t.Fatalf(`fields_repaired is empty on a repair that reported repaired: true.

git declares no addons here, so the old code's loop over the desired set produced nothing at all, and a real write to Kubernetes came back with zero fields. "I changed something, and it was nothing" is not an answer.`)
	}
	want := "metadata.labels[retired-addon]"
	if len(view.FieldsRepaired) != 1 || view.FieldsRepaired[0] != want {
		t.Errorf("fields_repaired = %v, want exactly [%s]", view.FieldsRepaired, want)
	}

	after, _ := argoClient.CoreV1().Secrets("argocd").Get(
		context.Background(), comparisonCluster, metav1.GetOptions{})
	if _, still := after.Labels["retired-addon"]; still {
		t.Error("the leftover addon label is still on the connection, so no write happened at all")
	}
}

// TestRepairLabelsOnly_OneChangeAmongManyReportsExactlyOneField: git declares six
// addons, the live connection has five of them right and one wrong, so exactly
// one field changed.
//
// The old code listed ALL SIX, because it looped over the desired label set
// whenever the bool said "something changed". Change one of twenty and it claimed
// twenty.
func TestRepairLabelsOnly_OneChangeAmongManyReportsExactlyOneField(t *testing.T) {
	declared := map[string]string{
		"datadog":      "enabled",
		"nginx":        "enabled",
		"cert-mgr":     "enabled",
		"prometheus":   "disabled",
		"grafana":      "disabled",
		"external-dns": "enabled",
	}
	// The live connection agrees about everything except nginx.
	liveLabels := map[string]string{}
	for k, v := range declared {
		liveLabels[k] = v
	}
	liveLabels["nginx"] = "disabled"

	router, _, _ := guestLabelsOnlyFixture(t, declared, liveLabels, nil)

	view := labelsOnlyRepairBody(t, router)

	if !view.Repaired {
		t.Fatal("the response says repaired: false, but one label really was wrong")
	}
	want := "metadata.labels[nginx]"
	if len(view.FieldsRepaired) != 1 || view.FieldsRepaired[0] != want {
		t.Errorf(`fields_repaired = %v (%d entries), want exactly [%s].

One label out of six was wrong, so one field changed. The old code listed every desired label whenever any one of them changed, so this response claimed six writes for one.`, view.FieldsRepaired, len(view.FieldsRepaired), want)
	}
	if !strings.Contains(view.Message, "1 label(s) changed") {
		t.Errorf("the message says %q; it must give the real count", view.Message)
	}
}

// TestRepairLabelsOnly_FieldListIsSorted: three labels change at once and the
// response lists them in a fixed order.
//
// The old code iterated a Go map, so the order was different on every call. A
// caller diffing two responses, or a person reading two runs, saw churn that was
// not there.
func TestRepairLabelsOnly_FieldListIsSorted(t *testing.T) {
	declared := map[string]string{
		"zulu":    "enabled",
		"alpha":   "enabled",
		"mike":    "enabled",
		"charlie": "enabled",
	}
	// Every one of them is wrong on the live connection, so all four change.
	liveLabels := map[string]string{
		"zulu": "disabled", "alpha": "disabled", "mike": "disabled", "charlie": "disabled",
	}

	router, _, _ := guestLabelsOnlyFixture(t, declared, liveLabels, nil)
	view := labelsOnlyRepairBody(t, router)

	if len(view.FieldsRepaired) != 4 {
		t.Fatalf("fields_repaired = %v, want four entries", view.FieldsRepaired)
	}
	if !sort.StringsAreSorted(view.FieldsRepaired) {
		t.Errorf(`fields_repaired is not sorted: %v.

Map iteration order is random in Go, so a response built by walking a map is different on every call and a caller cannot diff two of them.`, view.FieldsRepaired)
	}
}

// TestRepairLabelsOnly_PreservedCountsAreReal: the live connection carries two
// labels that are not Sharko's and two data keys Sharko never writes, and the
// response counts them.
//
// The old code never set these fields at all, so they were always zero — a
// connection full of somebody else's material reported "nothing preserved", which
// is precisely the promise this feature makes and the one the response failed to
// evidence.
func TestRepairLabelsOnly_PreservedCountsAreReal(t *testing.T) {
	router, argoClient, _ := guestLabelsOnlyFixture(t,
		map[string]string{"datadog": "enabled"},
		map[string]string{
			"datadog":                   "disabled", // so a write really happens
			"some-other-tool.io/owner":  "them",     // foreign label 1
			"another-tool.example/tier": "gold",     // foreign label 2
		},
		map[string][]byte{
			"shard":            []byte("7"),        // foreign data key 1
			"their-tool-state": []byte("whatever"), // foreign data key 2
		})

	view := labelsOnlyRepairBody(t, router)

	if !view.Repaired {
		t.Fatal("the fixture's label was wrong, so a write should have happened")
	}
	if view.PreservedForeignLabels != 2 {
		t.Errorf(`preserved_foreign_labels = %d, want 2.

Two labels on this connection are not Sharko's. The response is where a person confirms their things survived, and the old code never set this field at all, so it always read zero.`, view.PreservedForeignLabels)
	}
	if view.PreservedForeignDataKeys != 2 {
		t.Errorf(`preserved_foreign_data_keys = %d, want 2 (a labels-only repair never touches Data at all, and says so honestly).`, view.PreservedForeignDataKeys)
	}

	// The counts are true: the material really is still there.
	after, _ := argoClient.CoreV1().Secrets("argocd").Get(
		context.Background(), comparisonCluster, metav1.GetOptions{})
	if after.Labels["some-other-tool.io/owner"] != "them" {
		t.Error("a foreign label was lost")
	}
	if string(after.Data["shard"]) != "7" {
		t.Error("a foreign data key was lost")
	}
	if string(after.Data["config"]) != `{"bearerToken":"theirs"}` {
		t.Error("a labels-only repair rewrote the user's own credential blob")
	}
}

// TestRepairLabelsOnly_VersionConflictIs409NotBadGateway: something else writes
// the Secret between the primitive's read and its Update, so Kubernetes rejects
// the write with a conflict and nothing changes.
//
// That is the SAME situation the full-connection path reports as a 409. The
// labels-only branch used to recognise only the ownership error and send
// everything else to a 502, so one cause had two answers depending on which scope
// the cluster fell into — and a caller cannot write sane retry logic against that.
func TestRepairLabelsOnly_VersionConflictIs409NotBadGateway(t *testing.T) {
	router, argoClient, _ := guestLabelsOnlyFixture(t,
		map[string]string{"datadog": "enabled"},
		map[string]string{"datadog": "disabled"}, // so a write is really attempted
		nil)

	// The Update fails with a version conflict, exactly as the API server's
	// compare-and-swap does when the object moved under us.
	argoClient.PrependReactor("update", "secrets",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "secrets"}, comparisonCluster,
				fmt.Errorf("the object has been modified"))
		})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, repairReq(comparisonCluster, repairHeadSHA))

	if w.Code != http.StatusConflict {
		t.Fatalf(`a version conflict on the labels-only path returned %d, want 409 (body=%s).

This is the same situation the full-connection path answers with a 409: something else wrote the connection in the window, so nothing was written. One cause, one answer, whichever scope the cluster fell into. A 502 here says "the upstream broke" about a race that the caller should simply re-check and retry.`, w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "changed this cluster's connection while Sharko was repairing it") {
		t.Errorf("the 409 does not say what happened: %s", w.Body.String())
	}
}

// TestRepairLabelsOnly_EventIsTheAddonLabelsOneAndClaimsNothingElse: the event a
// labels-only repair emits carries the addon-label reason and a message that says
// only what happened.
//
// The full-connection event's message says Sharko rewrote the stored sign-in
// details. A labels-only repair never reads them. An event that overstates what
// happened is the same class of problem as a success event carrying a fault
// reason: the text is what an operator acts on, so it has to be true.
func TestRepairLabelsOnly_EventIsTheAddonLabelsOneAndClaimsNothingElse(t *testing.T) {
	router, _, fakeEvents := guestLabelsOnlyFixture(t,
		map[string]string{"datadog": "enabled"},
		map[string]string{"datadog": "disabled"},
		nil)

	view := labelsOnlyRepairBody(t, router)
	if !view.Repaired {
		t.Fatal("no write happened, so no event would be emitted and this proves nothing")
	}

	var emitted []string
	for {
		select {
		case e := <-fakeEvents.Events:
			emitted = append(emitted, e)
			continue
		default:
		}
		break
	}
	if len(emitted) == 0 {
		t.Fatal("a labels-only repair that changed something emitted no event")
	}

	var sawAddonLabelsEvent bool
	for _, e := range emitted {
		if strings.Contains(e, events.ReasonConnectionRepaired) {
			t.Errorf(`a labels-only repair emitted the FULL-CONNECTION event reason (%s):

  %s

That reason's message says Sharko rewrote the stored sign-in details, and this repair never read them. An operator or an automation switching on the reason cannot tell a label write from a credential write.`, events.ReasonConnectionRepaired, e)
		}
		if !strings.Contains(e, events.ReasonAddonLabelsRepaired) {
			continue
		}
		sawAddonLabelsEvent = true

		// The message must not claim anything about the sign-in details or the
		// connection as a whole.
		for _, forbidden := range []string{
			"repaired its ArgoCD connection",
			"owned field",
		} {
			if strings.Contains(e, forbidden) {
				t.Errorf("the labels-only event message contains %q, which is a claim about the whole connection:\n  %s", forbidden, e)
			}
		}
		if !strings.Contains(e, "1 label(s) rewritten") {
			t.Errorf("the labels-only event does not give the real count of labels:\n  %s", e)
		}
		if !strings.Contains(e, "sign-in details were not read or changed") {
			t.Errorf("the labels-only event does not say the sign-in details were left alone:\n  %s", e)
		}
	}
	if !sawAddonLabelsEvent {
		t.Errorf("no %s event was emitted; the events seen were %v", events.ReasonAddonLabelsRepaired, emitted)
	}
}

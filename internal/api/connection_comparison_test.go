package api

// connection_comparison_test.go — CC2-4 (the security sentinels) and the
// endpoint half of CC2-5.
//
// The sentinel value below appears nowhere else in this repository. Every test
// here proves it — and every derived form of it a leak could take — is absent
// from the comparison response, the OpenAPI document, the logs, the error
// messages and the metrics.

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

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/settings"
)

// comparisonSentinel is one unique made-up credential value that appears
// nowhere else in this repository. If a grep for it ever finds a second
// occurrence outside this file, that occurrence is the bug.
//
// It is not shaped like a real token from any provider on purpose: a
// realistic-looking fake in a test file is the thing somebody later mistakes
// for a real leaked credential.
const comparisonSentinel = "qzx7v4m2sentinel-comparison-value-do-not-copy"

// sentinelForms are every shape a leak of comparisonSentinel could take. A
// response, a log line, an error message or a metrics dump containing ANY of
// them is a failure.
func sentinelForms() []string {
	sum := sha256.Sum256([]byte(comparisonSentinel))
	forms := []string{
		// The raw value.
		comparisonSentinel,
		// Its base64 form, and the base64 of its base64 (Secret data is
		// base64 on the wire, so one extra round is worth checking).
		base64.StdEncoding.EncodeToString([]byte(comparisonSentinel)),
		base64.RawStdEncoding.EncodeToString([]byte(comparisonSentinel)),
		base64.URLEncoding.EncodeToString([]byte(comparisonSentinel)),
		// Its SHA-256, hex and base64. A hash is not a safe summary of a
		// secret: a short or guessable value is recovered from one.
		hex.EncodeToString(sum[:]),
		base64.StdEncoding.EncodeToString(sum[:]),
		// Prefix and suffix fragments. A "first few characters" preview is a
		// leak of the first few characters.
		comparisonSentinel[:8],
		comparisonSentinel[:16],
		comparisonSentinel[len(comparisonSentinel)-8:],
		comparisonSentinel[len(comparisonSentinel)-16:],
	}
	return forms
}

// sentinelLengthForms are the value's byte length written out in the LABELLED
// shapes a response or a log line would use it in. A length narrows a guess, so
// none of these may appear.
//
// The bare number on its own is deliberately NOT in this list: a two-digit
// number appears by chance in any large body of text (a port, a duration, a
// byte offset in a generated file), so sweeping for it would fail on
// coincidences and teach everyone to ignore this test. The bare number IS
// checked, precisely, against the comparison response — see
// assertNoSentinelLengthInJSON, which walks the decoded response and looks at
// actual values instead of searching text.
func sentinelLengthForms() []string {
	n := len(comparisonSentinel)
	return []string{
		fmt.Sprintf("%d bytes", n),
		fmt.Sprintf("%d chars", n),
		fmt.Sprintf("%d characters", n),
		fmt.Sprintf("\"length\":%d", n),
		fmt.Sprintf("\"length\": %d", n),
		fmt.Sprintf("\"len\":%d", n),
		fmt.Sprintf("\"len\": %d", n),
		fmt.Sprintf("\"bytes\":%d", n),
		fmt.Sprintf("\"size\":%d", n),
		fmt.Sprintf("length=%d", n),
		fmt.Sprintf("len=%d", n),
		fmt.Sprintf("bytes=%d", n),
	}
}

// assertNoSentinelLengthInJSON walks a decoded response and fails when the
// secret's byte length appears as an actual value anywhere in it — as a number,
// or as a string that is just that number. This is the precise version of the
// bare-number check: it looks at values rather than searching text, so it
// cannot fire on a coincidence and cannot be dodged by nesting.
//
// checked_field_count is exempt: it is a count of FIELDS compared, it is
// nowhere near the length of any value, and a run of exactly that many fields
// would be a coincidence, not a disclosure. The exemption is named so that
// exempting anything else has to be an argued decision.
func assertNoSentinelLengthInJSON(t *testing.T, where string, body []byte) {
	t.Helper()
	n := float64(len(comparisonSentinel))
	nStr := strconv.Itoa(len(comparisonSentinel))

	var walk func(path string, v interface{})
	walk = func(path string, v interface{}) {
		switch val := v.(type) {
		case map[string]interface{}:
			for k, inner := range val {
				if k == "checked_field_count" {
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
				t.Errorf("%s: %s is the secret's byte length as a number — a length narrows a guess", where, path)
			}
		case string:
			if val == nStr {
				t.Errorf("%s: %s is the secret's byte length as a string — a length narrows a guess", where, path)
			}
		}
	}

	var decoded interface{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("%s: decoding: %v", where, err)
	}
	walk("", decoded)
}

// variableLengthMasks are masks whose length tracks the value — a run of stars
// or bullets exactly as long as the secret, one shorter and one longer. A mask
// like that is a length disclosure wearing a disguise.
func variableLengthMasks() []string {
	n := len(comparisonSentinel)
	var out []string
	for _, ch := range []string{"*", "•", "x", "●"} {
		for _, l := range []int{n - 1, n, n + 1} {
			out = append(out, strings.Repeat(ch, l))
		}
	}
	return out
}

// comparisonFakeVault hands back a Kubeconfig whose token IS the sentinel, so
// the sentinel really does flow through the expected-Secret build and the
// in-memory compare. If the endpoint leaks anything, it has something to leak.
type comparisonFakeVault struct {
	failWith error
}

func (v comparisonFakeVault) GetCredentials(name string) (*providers.Kubeconfig, error) {
	if v.failWith != nil {
		return nil, v.failWith
	}
	return &providers.Kubeconfig{
		Server: "https://" + name + ".invalid",
		CAData: []byte("not-a-real-ca-just-test-bytes"),
		Token:  comparisonSentinel,
	}, nil
}
func (comparisonFakeVault) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (comparisonFakeVault) SearchSecrets(_ string) ([]string, error)       { return nil, nil }
func (comparisonFakeVault) HealthCheck(_ context.Context) error            { return nil }

// comparisonGP serves a managed-clusters file and nothing else, and reports a
// branch head SHA so the pinned-commit path is exercised.
type comparisonGP struct {
	managedYAML []byte
	headSHA     string
	readErr     error
}

func (g *comparisonGP) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if g.readErr != nil {
		return nil, g.readErr
	}
	if path == "configuration/managed-clusters.yaml" && g.managedYAML != nil {
		return g.managedYAML, nil
	}
	return nil, gitprovider.ErrFileNotFound
}
func (g *comparisonGP) GetBranchHeadSHA(_ context.Context, _ string) (string, error) {
	if g.headSHA == "" {
		return "", fmt.Errorf("no head")
	}
	return g.headSHA, nil
}
func (g *comparisonGP) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (g *comparisonGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (g *comparisonGP) TestConnection(_ context.Context) error            { return nil }
func (g *comparisonGP) CreateBranch(_ context.Context, _, _ string) error { return nil }
func (g *comparisonGP) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}
func (g *comparisonGP) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}
func (g *comparisonGP) DeleteFile(_ context.Context, _, _, _ string) error { return nil }
func (g *comparisonGP) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}
func (g *comparisonGP) MergePullRequest(_ context.Context, _ int) error { return nil }
func (g *comparisonGP) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "", nil
}
func (g *comparisonGP) DeleteBranch(_ context.Context, _ string) error { return nil }

const comparisonCluster = "prod-eu"

// comparisonFixture wires a server with a real cluster reconciler, a git
// provider serving the given managed-clusters YAML, a secrets backend handing
// back the sentinel token, and a live connection Secret seeded from liveSecret.
func comparisonFixture(t *testing.T, managedYAML string, live *corev1.Secret, vault providers.ClusterCredentialsProvider) (*Server, http.Handler, *fake.Clientset) {
	t.Helper()

	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &comparisonGP{
		managedYAML: []byte(managedYAML),
		headSHA:     "1111111111111111111111111111111111111111",
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	var argoClient *fake.Clientset
	if live != nil {
		argoClient = fake.NewSimpleClientset(live)
	} else {
		argoClient = fake.NewSimpleClientset()
	}

	settingsClient := fake.NewSimpleClientset()
	settingsStore := settings.NewStore(settingsClient, "sharko")

	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        vault,
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour, // never auto-fires: this endpoint reads only
		SelfHealFn:   settingsStore.IsManagedClusterSelfHealEnabled,
	})
	srv.SetClusterReconciler(recon)

	// The backend that holds the sentinel. The ArgoCD reader is disabled, so
	// no test can accidentally read the live Secret as a credential source.
	installCredProvider(srv, vault, nil, nil)

	return srv, router, argoClient
}

func comparisonReq(cluster string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+cluster+"/connection-comparison", nil)
	req.Header.Set("X-Sharko-User", "op")
	req.Header.Set("X-Sharko-Role", "operator")
	return req
}

// liveConnectionSecret builds a live connection Secret Sharko owns.
func liveConnectionSecret(labels map[string]string, data map[string]string, annotations map[string]string) *corev1.Secret {
	l := map[string]string{
		clusterreconciler.LabelManagedBy: clusterreconciler.LabelValueSharko,
		"argocd.argoproj.io/secret-type": "cluster",
	}
	for k, v := range labels {
		l[k] = v
	}
	d := map[string][]byte{}
	for k, v := range data {
		d[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: comparisonCluster, Namespace: "argocd", Labels: l, Annotations: annotations},
		Type:       corev1.SecretTypeOpaque,
		Data:       d,
	}
}

const backendManagedYAML = "clusters:\n- name: " + comparisonCluster + "\n  credsSource: secret-kubeconfig\n  labels:\n    datadog: enabled\n"

// --- CC2-4: the sentinel sweep ---------------------------------------------

// TestConnectionComparison_NeverReturnsTheSecretValue is the headline security
// test. The sentinel really is the cluster's stored token, so it really does
// flow through the expected-Secret build and the in-memory compare — and none
// of its forms may appear in the response, whether the connection matches or
// not.
func TestConnectionComparison_NeverReturnsTheSecretValue(t *testing.T) {
	cases := []struct {
		name       string
		liveConfig string
	}{
		{"credential blob matches", ""}, // filled in below from the real expected build
		{"credential blob differs", `{"bearerToken":"a-totally-different-made-up-value"}`},
		{"credential blob missing", "\x00absent"},
		{"credential blob is the sentinel in the clear", `{"bearerToken":"` + comparisonSentinel + `"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]string{
				"name":   comparisonCluster,
				"server": "https://" + comparisonCluster + ".invalid",
			}
			if tc.liveConfig != "\x00absent" {
				if tc.liveConfig == "" {
					// The shape a real write would produce for the sentinel.
					data["config"] = `{"bearerToken":"` + comparisonSentinel + `","tlsClientConfig":{}}`
				} else {
					data["config"] = tc.liveConfig
				}
			}
			live := liveConnectionSecret(map[string]string{"datadog": "enabled"}, data, nil)
			_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

			w := httptest.NewRecorder()
			router.ServeHTTP(w, comparisonReq(comparisonCluster))
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
			}
			assertNoSentinel(t, "response body", w.Body.String())
			assertNoSentinelLengthInJSON(t, "response body", w.Body.Bytes())

			// And the answer must actually say something — a test that
			// passes because the endpoint returned nothing proves nothing.
			var view connectionComparisonView
			if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if view.Status == "" {
				t.Fatal("the response carried no status")
			}
			if !view.ValuesNeverReturned {
				t.Error("values_never_returned must be true")
			}
			for _, d := range view.Differences {
				if d.Sensitive && (d.Expected != nil || d.Live != nil) {
					t.Errorf("sensitive difference %q carried a side", d.Path)
				}
			}
		})
	}
}

// TestConnectionComparison_SensitiveDifferenceHasNoSideProperties checks the
// raw JSON, not the decoded struct: the rule is that the properties are ABSENT,
// which a decoded struct with nil pointers cannot tell apart from present-and-
// null.
func TestConnectionComparison_SensitiveDifferenceHasNoSideProperties(t *testing.T) {
	live := liveConnectionSecret(
		map[string]string{"datadog": "enabled"},
		map[string]string{
			"name":   comparisonCluster,
			"server": "https://" + comparisonCluster + ".invalid",
			"config": `{"bearerToken":"a-different-made-up-value"}`,
		}, nil)
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	var raw struct {
		Differences []map[string]json.RawMessage `json:"differences"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	sawSensitive := false
	for _, d := range raw.Differences {
		if _, isSensitive := d["sensitive"]; !isSensitive {
			continue
		}
		sawSensitive = true
		if _, ok := d["expected"]; ok {
			t.Errorf("a sensitive difference carries an \"expected\" property: %v", d)
		}
		if _, ok := d["live"]; ok {
			t.Errorf("a sensitive difference carries a \"live\" property: %v", d)
		}
	}
	if !sawSensitive {
		t.Fatalf("the fixture produced no sensitive difference, so nothing was tested: %s", w.Body.String())
	}
}

// TestConnectionComparison_LogsAndErrorsCarryNoSentinel captures everything the
// handler logs while a comparison runs — including the failure paths, which are
// where an error's own text would leak if it were passed through.
func TestConnectionComparison_LogsAndErrorsCarryNoSentinel(t *testing.T) {
	cases := []struct {
		name  string
		vault providers.ClusterCredentialsProvider
		gpErr error
	}{
		{name: "happy path", vault: comparisonFakeVault{}},
		{
			name: "the secrets backend fails with the value in its error text",
			// The nastiest realistic case: a provider SDK that puts the
			// credential into its own error message.
			vault: comparisonFakeVault{failWith: fmt.Errorf("backend refused while handling value %s", comparisonSentinel)},
		},
		{
			name:  "the git read fails with the value in its error text",
			vault: comparisonFakeVault{},
			gpErr: fmt.Errorf("git read failed for %s", comparisonSentinel),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs strings.Builder
			restore := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(restore) })

			live := liveConnectionSecret(
				map[string]string{"datadog": "enabled"},
				map[string]string{
					"name":   comparisonCluster,
					"server": "https://" + comparisonCluster + ".invalid",
					"config": `{"bearerToken":"` + comparisonSentinel + `"}`,
				}, nil)

			argo := newStubArgoSrv(t, []map[string]interface{}{
				{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
			}, http.StatusOK)
			gp := &comparisonGP{
				managedYAML: []byte(backendManagedYAML),
				headSHA:     "2222222222222222222222222222222222222222",
				readErr:     tc.gpErr,
			}
			srv, router := reconcileTestServer(t, gp, argo.URL)
			recon := clusterreconciler.New(clusterreconciler.Deps{
				GitProvider:  func() gitprovider.GitProvider { return gp },
				ArgoClient:   fake.NewSimpleClientset(live),
				Vault:        tc.vault,
				AuditFn:      func(audit.Entry) {},
				Namespace:    "argocd",
				TickInterval: time.Hour,
			})
			srv.SetClusterReconciler(recon)
			installCredProvider(srv, tc.vault, nil, nil)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, comparisonReq(comparisonCluster))

			assertNoSentinel(t, "response body", w.Body.String())
			assertNoSentinel(t, "log output", logs.String())
		})
	}
}

// TestConnectionComparison_OpenAPIDocCarriesNoSentinel sweeps the generated
// OpenAPI files. A hand-written example in a swagger annotation is an easy
// place for a real-looking value to end up.
func TestConnectionComparison_OpenAPIDocCarriesNoSentinel(t *testing.T) {
	for _, path := range []string{
		"../../docs/swagger/swagger.json",
		"../../docs/swagger/swagger.yaml",
		"../../docs/swagger/docs.go",
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		assertNoSentinel(t, path, string(body))
	}
}

// TestConnectionComparison_MetricsCarryNoSentinel scrapes the metrics endpoint
// after a comparison has run. A label on a metric is a place a value could be
// stamped by mistake, and metrics are usually readable by more people than the
// API is.
func TestConnectionComparison_MetricsCarryNoSentinel(t *testing.T) {
	live := liveConnectionSecret(
		map[string]string{"datadog": "enabled"},
		map[string]string{
			"name":   comparisonCluster,
			"server": "https://" + comparisonCluster + ".invalid",
			"config": `{"bearerToken":"` + comparisonSentinel + `"}`,
		}, nil)
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	m := httptest.NewRecorder()
	router.ServeHTTP(m, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if m.Code != http.StatusOK {
		t.Skipf("metrics endpoint returned %d on this build; nothing to sweep", m.Code)
	}
	assertNoSentinel(t, "metrics output", m.Body.String())
}

// TestConnectionComparison_IsNotAGuessingOracle proves the endpoint cannot be
// steered. A caller may not supply a candidate value or an expected manifest,
// and throwing extra query parameters, headers and a request body at it changes
// nothing about the answer.
func TestConnectionComparison_IsNotAGuessingOracle(t *testing.T) {
	live := liveConnectionSecret(
		map[string]string{"datadog": "enabled"},
		map[string]string{
			"name":   comparisonCluster,
			"server": "https://" + comparisonCluster + ".invalid",
			"config": `{"bearerToken":"` + comparisonSentinel + `"}`,
		}, nil)
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	baseline := comparisonAnswer(t, router, comparisonReq(comparisonCluster))

	attempts := []struct {
		name  string
		build func() *http.Request
	}{
		{"a candidate value in a query parameter", func() *http.Request {
			r := comparisonReq(comparisonCluster)
			r.URL.RawQuery = "expected=" + comparisonSentinel + "&candidate=" + comparisonSentinel
			return r
		}},
		{"a wrong candidate value in a query parameter", func() *http.Request {
			r := comparisonReq(comparisonCluster)
			r.URL.RawQuery = "expected=definitely-not-the-value&token=nope"
			return r
		}},
		{"a namespace and a backend path override", func() *http.Request {
			r := comparisonReq(comparisonCluster)
			r.URL.RawQuery = "namespace=kube-system&secretPath=some/other/path&destination=elsewhere"
			return r
		}},
		{"an expected manifest in the body", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet,
				"/api/v1/clusters/"+comparisonCluster+"/connection-comparison",
				strings.NewReader(`{"expected":{"data":{"config":"`+comparisonSentinel+`"}}}`))
			r.Header.Set("X-Sharko-User", "op")
			r.Header.Set("X-Sharko-Role", "operator")
			r.Header.Set("Content-Type", "application/json")
			return r
		}},
		{"a hash of a candidate value in a header", func() *http.Request {
			r := comparisonReq(comparisonCluster)
			sum := sha256.Sum256([]byte(comparisonSentinel))
			r.Header.Set("X-Expected-Hash", hex.EncodeToString(sum[:]))
			return r
		}},
	}

	for _, a := range attempts {
		t.Run(a.name, func(t *testing.T) {
			got := comparisonAnswer(t, router, a.build())
			if got != baseline {
				t.Fatalf("the answer changed when a caller supplied extra input.\n got %s\nwant %s", got, baseline)
			}
			assertNoSentinel(t, "response body", got)
		})
	}
}

// comparisonAnswer runs a request and returns the answer with the per-call
// timestamp removed, so two runs are comparable.
func comparisonAnswer(t *testing.T, router http.Handler, req *http.Request) string {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	delete(m, "checked_at")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("re-encoding: %v", err)
	}
	return string(out)
}

// assertNoSentinel fails when text contains the sentinel in any form, its byte
// length as a number, or a mask whose length tracks it.
func assertNoSentinel(t *testing.T, where, text string) {
	t.Helper()
	for _, form := range sentinelForms() {
		if strings.Contains(text, form) {
			t.Errorf("%s contains a form of the secret value (%q)", where, form)
		}
	}
	for _, form := range sentinelLengthForms() {
		if strings.Contains(text, form) {
			t.Errorf("%s contains the secret's byte length (%q) — a length narrows a guess", where, form)
		}
	}
	for _, mask := range variableLengthMasks() {
		if strings.Contains(text, mask) {
			t.Errorf("%s contains a mask whose length tracks the secret (%d of %q)", where, len(mask), mask[:1])
		}
	}
}

// --- the endpoint's own behaviour -----------------------------------------

func TestConnectionComparison_SyncedWhenEverythingMatches(t *testing.T) {
	// Build the live Secret from the same canonical builder the comparison
	// uses, so this is a genuine "nothing is wrong" case rather than a
	// hand-tuned one.
	_, router, _ := comparisonFixture(t, backendManagedYAML, expectedLiveSecretForFixture(t), comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.Status != "synced" {
		t.Fatalf("status = %q, want synced (differences %+v)", view.Status, view.Differences)
	}
	if view.Scope != "full" {
		t.Errorf("scope = %q, want full", view.Scope)
	}
	if view.ComparedCommit == "" {
		t.Error("the answer should name the commit everything was read at")
	}
	if view.Branch == "" {
		t.Error("the answer should name the configured branch")
	}
	if view.ComparedPath == "" {
		t.Error("the answer should name the file the desired state came from")
	}
	if view.CredentialSourceType != models.CredsSourceSecretKubeconfig {
		t.Errorf("credential_source_type = %q, want %q", view.CredentialSourceType, models.CredsSourceSecretKubeconfig)
	}
	if !view.RepairAvailable || view.RepairScope != "full_connection" {
		t.Errorf("repair_available=%v repair_scope=%q, want true / full_connection", view.RepairAvailable, view.RepairScope)
	}
}

func TestConnectionComparison_MissingWhenThereIsNoSecret(t *testing.T) {
	_, router, _ := comparisonFixture(t, backendManagedYAML, nil, comparisonFakeVault{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.Status != "missing" {
		t.Fatalf("status = %q, want missing", view.Status)
	}
}

// TestConnectionComparison_BackendReadFailureIsCheckFailed: the backend is
// down, the connection itself is perfect. The answer must be "could not
// finish", never "in sync".
func TestConnectionComparison_BackendReadFailureIsCheckFailed(t *testing.T) {
	live := expectedLiveSecretForFixture(t)
	_, router, _ := comparisonFixture(t, backendManagedYAML, live,
		comparisonFakeVault{failWith: fmt.Errorf("the secrets backend is unreachable")})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.Status != "check_failed" {
		t.Fatalf("status = %q, want check_failed", view.Status)
	}
	if view.FailureReason == "" {
		t.Error("a check_failed answer must say why")
	}
	if strings.Contains(w.Body.String(), "unreachable") {
		t.Error("the backend's own error text was passed through")
	}
}

func TestConnectionComparison_GitReadFailureIsCheckFailed(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &comparisonGP{readErr: fmt.Errorf("git is having a bad day")}
	srv, router := reconcileTestServer(t, gp, argo.URL)
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   fake.NewSimpleClientset(),
		Vault:        comparisonFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
	})
	srv.SetClusterReconciler(recon)
	installCredProvider(srv, comparisonFakeVault{}, nil, nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.Status != "check_failed" {
		t.Fatalf("status = %q, want check_failed", view.Status)
	}
	if strings.Contains(w.Body.String(), "bad day") {
		t.Error("git's own error text was passed through")
	}
}

func TestConnectionComparison_404WhenClusterNotManaged(t *testing.T) {
	_, router, _ := comparisonFixture(t, "clusters: []\n", nil, comparisonFakeVault{})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestConnectionComparison_403ForViewer(t *testing.T) {
	_, router, _ := comparisonFixture(t, backendManagedYAML, expectedLiveSecretForFixture(t), comparisonFakeVault{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+comparisonCluster+"/connection-comparison", nil)
	req.Header.Set("X-Sharko-User", "viewer")
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a viewer, got %d (body=%s)", w.Code, w.Body.String())
	}
}

func TestConnectionComparison_503WhenNoReconciler(t *testing.T) {
	argo := newStubArgoSrv(t, nil, http.StatusOK)
	gp := &comparisonGP{managedYAML: []byte(backendManagedYAML)}
	_, router := reconcileTestServer(t, gp, argo.URL)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestConnectionComparison_WritesNothing pins the read-only promise at the
// endpoint: after a comparison, the live Secret is byte-for-byte what it was.
func TestConnectionComparison_WritesNothing(t *testing.T) {
	// A deliberately drifted Secret, so the endpoint has every reason to
	// "helpfully" fix it.
	live := liveConnectionSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{
			"name":   comparisonCluster,
			"server": "https://wrong-address.invalid",
			"config": `{"bearerToken":"an-old-made-up-value"}`,
		}, nil)
	_, router, argoClient := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	before, err := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the secret before: %v", err)
	}
	beforeJSON, _ := json.Marshal(before)

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, comparisonReq(comparisonCluster))
		if w.Code != http.StatusOK {
			t.Fatalf("run %d: expected 200, got %d (body=%s)", i, w.Code, w.Body.String())
		}
		var view connectionComparisonView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if view.Status != "out_of_sync" {
			t.Fatalf("run %d: status = %q, want out_of_sync", i, view.Status)
		}
	}

	after, err := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the secret after: %v", err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("the comparison changed the connection Secret.\nbefore %s\nafter  %s", beforeJSON, afterJSON)
	}

	// And no write action reached the fake client at all.
	for _, a := range argoClient.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Errorf("the comparison issued a %q on %q", a.GetVerb(), a.GetResource().Resource)
		}
	}
}

// expectedLiveSecretForFixture builds the live Secret the reconciler's own
// write path would produce for the fixture's cluster, so a "nothing is wrong"
// test is genuinely comparing like with like.
func expectedLiveSecretForFixture(t *testing.T) *corev1.Secret {
	t.Helper()
	creds, err := comparisonFakeVault{}.GetCredentials(comparisonCluster)
	if err != nil {
		t.Fatalf("fixture credentials: %v", err)
	}
	labels := map[string]string{"datadog": "enabled"}
	models.ApplyConnectivityCheckLabel(labels, true)
	built, err := argosecretsBuildForTest(comparisonCluster, creds, labels)
	if err != nil {
		t.Fatalf("building the fixture secret: %v", err)
	}
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: built.Name, Namespace: built.Namespace, Labels: map[string]string{}},
		Type:       built.Type,
		Data:       map[string][]byte{},
	}
	for k, v := range built.Labels {
		live.Labels[k] = v
	}
	for k, v := range built.StringData {
		live.Data[k] = []byte(v)
	}
	return live
}

// argosecretsBuildForTest builds the connection Secret the write path would
// produce, through the same canonical builder, so the fixture's "matching"
// Secret is not a hand-tuned guess at the right shape.
func argosecretsBuildForTest(name string, creds *providers.Kubeconfig, labels map[string]string) (*corev1.Secret, error) {
	return argosecrets.BuildClusterSecret(argosecrets.ClusterSecretSpec{
		Name:     name,
		Server:   creds.Server,
		Token:    creds.Token,
		CertData: base64.StdEncoding.EncodeToString(creds.CertData),
		KeyData:  base64.StdEncoding.EncodeToString(creds.KeyData),
		CAData:   base64.StdEncoding.EncodeToString(creds.CAData),
		Labels:   labels,
	}, "argocd")
}

// TestResyncStillBehavesTheSameAfterTheComparisonLanded is the "did step 2
// disturb anything" test.
//
// POST /clusters/{name}/resync is the one existing action that touches a
// cluster's connection labels, and step 2 must have left it exactly as it was.
// The comparison shares its git-reading code (readDesiredState, which now takes
// a ref) and its label computation (desiredAddonLabels), so a mistake in that
// refactor would show up here.
//
// This drives the same drift the existing resync test drives, and checks the
// same three things: a 200, the label converging in the live Secret, and the
// diff the response reports.
func TestResyncStillBehavesTheSameAfterTheComparisonLanded(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{
		{"name": comparisonCluster, "server": "https://" + comparisonCluster + ".invalid"},
	}, http.StatusOK)
	gp := &comparisonGP{
		managedYAML: []byte("clusters:\n- name: " + comparisonCluster + "\n  labels:\n    addon-foo: enabled\n"),
		headSHA:     "3333333333333333333333333333333333333333",
	}
	srv, router := reconcileTestServer(t, gp, argo.URL)

	// Drifted: git wants addon-foo enabled, the live Secret does not have it.
	argoClient := fake.NewSimpleClientset(liveConnectionSecret(nil, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"execProviderConfig":{}}`,
	}, nil))

	settingsClient := fake.NewSimpleClientset()
	settingsStore := settings.NewStore(settingsClient, "sharko")
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   argoClient,
		Vault:        comparisonFakeVault{},
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
		SelfHealFn:   settingsStore.IsManagedClusterSelfHealEnabled,
	})
	srv.SetClusterReconciler(recon)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+comparisonCluster+"/resync", nil)
	req.Header.Set("X-Sharko-User", "op")
	req.Header.Set("X-Sharko-Role", "operator")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp models.ClusterResyncResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp.Outcome != string(clusterreconciler.OutcomeSucceeded) {
		t.Errorf("outcome = %q, want %q (message=%q)", resp.Outcome, clusterreconciler.OutcomeSucceeded, resp.Message)
	}
	if len(resp.LabelDiff.Added) != 1 || resp.LabelDiff.Added[0] != "addon-foo" {
		t.Errorf("label_diff.added = %v, want [addon-foo]", resp.LabelDiff.Added)
	}

	secret, err := argoClient.CoreV1().Secrets("argocd").Get(context.Background(), comparisonCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the secret: %v", err)
	}
	if secret.Labels["addon-foo"] != "enabled" {
		t.Errorf("addon-foo = %q, want enabled", secret.Labels["addon-foo"])
	}
}

// TestConnectionComparison_InlineKubeconfigNeverSyncedThroughTheEndpoint walks
// the pasted-kubeconfig case end to end. This is the case the whole feature is
// shaped around: Sharko CAN read those credentials back out of the connection,
// so the naive build would compare the connection with itself and answer
// "synced" no matter what. It must answer "limited" instead.
func TestConnectionComparison_InlineKubeconfigNeverSyncedThroughTheEndpoint(t *testing.T) {
	inlineYAML := "clusters:\n- name: " + comparisonCluster + "\n  credsSource: inline-kubeconfig\n  labels:\n    datadog: enabled\n"
	live := liveConnectionSecret(map[string]string{"datadog": "enabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"bearerToken":"` + comparisonSentinel + `"}`,
	}, nil)
	_, router, _ := comparisonFixture(t, inlineYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.Status == "synced" {
		t.Fatalf("a pasted-kubeconfig cluster was reported fully in sync — this is the self-comparison bug the feature exists to prevent: %s", w.Body.String())
	}
	if view.Status != "limited" {
		t.Fatalf("status = %q, want limited", view.Status)
	}
	if view.OwnershipMode != "inline_kubeconfig" {
		t.Errorf("ownership_mode = %q, want inline_kubeconfig", view.OwnershipMode)
	}
	if view.RepairScope == "full_connection" {
		t.Error("a pasted-kubeconfig cluster must never be offered a full repair")
	}
	if view.LimitReason == "" {
		t.Error("a limited answer must explain itself")
	}
	if len(view.NotChecked) == 0 {
		t.Error("a limited answer must say which fields were not checked")
	}
	assertNoSentinel(t, "response body", w.Body.String())
}

// TestConnectionComparison_UnknownSourceNeverSyncedThroughTheEndpoint covers
// the seventh mode end to end: a record written before Sharko had a
// credsSource field, which every upgraded install has.
func TestConnectionComparison_UnknownSourceNeverSyncedThroughTheEndpoint(t *testing.T) {
	legacyYAML := "clusters:\n- name: " + comparisonCluster + "\n  labels:\n    datadog: enabled\n"
	live := liveConnectionSecret(map[string]string{"datadog": "enabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"bearerToken":"` + comparisonSentinel + `"}`,
	}, nil)
	_, router, _ := comparisonFixture(t, legacyYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.Status != "limited" {
		t.Fatalf("status = %q, want limited", view.Status)
	}
	if view.OwnershipMode != "unknown_source" {
		t.Errorf("ownership_mode = %q, want unknown_source", view.OwnershipMode)
	}
	if view.RepairScope == "full_connection" {
		t.Error("an unknown credentials source must never be offered a full repair")
	}
	if view.CredentialSourceType != "" {
		t.Errorf("credential_source_type = %q, want empty — Sharko must not guess or backfill it", view.CredentialSourceType)
	}
	assertNoSentinel(t, "response body", w.Body.String())
}

// TestConnectionComparison_ForeignOwnershipThroughTheEndpoint: another tool's
// ownership marker means nothing is compared and nothing is offered.
func TestConnectionComparison_ForeignOwnershipThroughTheEndpoint(t *testing.T) {
	live := liveConnectionSecret(map[string]string{"datadog": "disabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://" + comparisonCluster + ".invalid",
		"config": `{"bearerToken":"` + comparisonSentinel + `"}`,
	}, nil)
	live.Labels[clusterreconciler.LabelManagedBy] = "another-tool"
	_, router, _ := comparisonFixture(t, backendManagedYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.Status != "ownership_conflict" {
		t.Fatalf("status = %q, want ownership_conflict", view.Status)
	}
	if len(view.Differences) != 0 {
		t.Errorf("nothing is compared on another tool's connection, got %+v", view.Differences)
	}
	if view.RepairAvailable {
		t.Error("no repair is offered on another tool's connection")
	}
	if view.CheckedFieldCount != 0 {
		t.Errorf("checked_field_count = %d, want 0", view.CheckedFieldCount)
	}
	assertNoSentinel(t, "response body", w.Body.String())
}

// TestConnectionComparison_SelfManagedThroughTheEndpoint: a connection the
// person maintains. Only the addon labels are checked, and the deliberately
// wrong API address is not reported, because it is not Sharko's.
func TestConnectionComparison_SelfManagedThroughTheEndpoint(t *testing.T) {
	selfYAML := "clusters:\n- name: " + comparisonCluster + "\n  connectionManagedBy: user\n  credsSource: secret-kubeconfig\n  labels:\n    datadog: enabled\n"
	live := liveConnectionSecret(map[string]string{"datadog": "enabled"}, map[string]string{
		"name":   comparisonCluster,
		"server": "https://their-own-address.invalid",
		"config": `{"bearerToken":"` + comparisonSentinel + `"}`,
	}, nil)
	_, router, _ := comparisonFixture(t, selfYAML, live, comparisonFakeVault{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, comparisonReq(comparisonCluster))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var view connectionComparisonView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if view.OwnershipMode != "self_managed" {
		t.Fatalf("ownership_mode = %q, want self_managed", view.OwnershipMode)
	}
	if view.Scope != "addon_labels_only" {
		t.Errorf("scope = %q, want addon_labels_only", view.Scope)
	}
	if view.RepairScope != "addon_labels_only" {
		t.Errorf("repair_scope = %q, want addon_labels_only", view.RepairScope)
	}
	for _, d := range view.Differences {
		if strings.HasPrefix(d.Path, "data.") {
			t.Errorf("a connection the person maintains reported on %q, which is not Sharko's", d.Path)
		}
	}
	assertNoSentinel(t, "response body", w.Body.String())
}

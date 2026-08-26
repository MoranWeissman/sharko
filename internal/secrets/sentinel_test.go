package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// Task #152 story D — put a known fake value through the whole addon-values
// delivery flow (the scheduled engine: fetch from the secrets provider,
// hash, write to the remote cluster — reconcile/SyncOne/CheckOne) and prove
// it appears in NONE of: a log line at any level, an audit callback, a
// Prometheus metric name/label/value, the reconciler's own error/status
// state, or anything written to the remote Secret besides its Data (the one
// place it is SUPPOSED to land — that's the destination, not a leak).
// Failure paths (a write that fails, a fetch that fails partway through, a
// pre-existing foreign Secret) are exercised too, not just the happy path.
//
// sentinelValue is distinctive enough that a plain substring search is
// meaningful. Fake value only — never real credential material.
const sentinelValue = "CANARY-3e81b6f0-do-not-log-me-a274c19d-sentinel"

// captureLogs swaps slog's default logger for a buffer-backed JSON handler
// at Debug level for the duration of fn — the lowest level, so every line
// the code under test emits, at any level, lands in the buffer. Restores
// the previous default when the test ends. Not safe under t.Parallel() (it
// mutates process-global state); no test in this file uses it.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	fn()
	return buf.String()
}

// scrapeMetricsText renders the full Prometheus default registry — the same
// one every sharko_reconciler_*/sharko_managed_secrets_* metric in
// internal/metrics registers against via promauto, and the same one the
// real /metrics endpoint scrapes — as text, so a test can substring-search
// it for a leaked value instead of trusting that every metric's label
// vocabulary was enumerated correctly by hand.
func scrapeMetricsText(t *testing.T) string {
	t.Helper()
	h := promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{})
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}

// sentinelAuditCapture records every argument every AuditFunc/ItemAuditFunc
// call received as plain text, so a test can search it the same way it
// searches logs — catching a leak through the audit path even if it shows
// up somewhere nobody thought to assert on by name.
type sentinelAuditCapture struct {
	lines []string
}

func (c *sentinelAuditCapture) auditFn() AuditFunc {
	return func(clusterName string, created, updated int) {
		c.lines = append(c.lines, fmt.Sprintf("audit cluster=%s created=%d updated=%d", clusterName, created, updated))
	}
}

func (c *sentinelAuditCapture) itemAuditFn() ItemAuditFunc {
	return func(cluster, addon string, outcome ItemOutcome) {
		c.lines = append(c.lines, fmt.Sprintf("item-audit cluster=%s addon=%s outcome=%s", cluster, addon, outcome))
	}
}

func (c *sentinelAuditCapture) text() string {
	return strings.Join(c.lines, "\n")
}

// sentinelValuesProvider returns the sentinel for both keys catalogWithSecrets
// defines, so a full create/update pass genuinely carries the sentinel
// through hashing, the write payload, and provenance — not just one key.
func sentinelValuesProvider() *mockSecretProvider {
	return &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte(sentinelValue),
		"secrets/datadog/app-key": []byte(sentinelValue + "-app"),
	}}
}

// --- Happy path: create, then a second no-op pass ---------------------------

// TestSentinel_Reconcile_HappyPath_NeverLeaksAnywhere is the core proof for
// this story: a full periodic-reconcile pass (create, then a second pass
// that finds nothing changed) with a known fake value moving through it,
// checked against every channel the story lists.
func TestSentinel_Reconcile_HappyPath_NeverLeaksAnywhere(t *testing.T) {
	client := fake.NewSimpleClientset()
	audit := &sentinelAuditCapture{}
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		sentinelValuesProvider(),
		fakeRemoteClientFn(client),
	)
	r.SetAuditFunc(audit.auditFn())
	r.SetItemAuditFunc(audit.itemAuditFn())

	logs := captureLogs(t, func() {
		r.reconcile() // creates
		r.reconcile() // unchanged — the hash-compare, no-write path
	})
	metricsText := scrapeMetricsText(t)

	// The secret's Data IS the intended destination — the value belongs
	// there. Confirm it landed (the flow actually ran), then check every
	// OTHER surface never carries it.
	secret, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected the secret to be created: %v", err)
	}
	if string(secret.Data["api-key"]) != sentinelValue {
		t.Fatalf("secret Data is the one legitimate destination — expected the sentinel there, got %q", secret.Data["api-key"])
	}
	for k, v := range secret.Annotations {
		if strings.Contains(v, sentinelValue) {
			t.Errorf("sentinel leaked into Secret annotation %q: %q", k, v)
		}
	}
	for k, v := range secret.Labels {
		if strings.Contains(v, sentinelValue) {
			t.Errorf("sentinel leaked into Secret label %q: %q", k, v)
		}
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel leaked into logs:\n%s", logs)
	}
	if strings.Contains(metricsText, sentinelValue) {
		t.Fatalf("sentinel leaked into a Prometheus metric name/label/value")
	}
	if strings.Contains(audit.text(), sentinelValue) {
		t.Fatalf("sentinel leaked into an audit callback: %s", audit.text())
	}
	if errs := r.GetErrors(); len(errs) != 0 {
		t.Errorf("happy path should have no errors, got %v", errs)
	}
	if last := r.LastError(); strings.Contains(last, sentinelValue) {
		t.Errorf("sentinel leaked into LastError: %s", last)
	}
	if errMsg, ok := r.LastItemError("prod-cluster", "datadog"); ok && strings.Contains(errMsg, sentinelValue) {
		t.Errorf("sentinel leaked into LastItemError: %s", errMsg)
	}

	// This engine keeps no persisted state of its own (no ConfigMap, no
	// disk file — itemRecords lives in memory only). The remote clientset
	// is the only place this pass could plausibly have written something
	// besides the addon Secret itself; confirm it never wrote a ConfigMap.
	cms, err := client.CoreV1().ConfigMaps("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing configmaps: %v", err)
	}
	if len(cms.Items) != 0 {
		t.Errorf("this engine must never write a ConfigMap; found %d", len(cms.Items))
	}
}

// --- Failure path: the create write itself fails -----------------------------

// TestSentinel_Reconcile_CreateFailure_ErrorNeverLeaksValue exercises a
// failure path, not just the happy one: the value is fetched successfully
// (it exists in memory, ready to write) and then the Kubernetes create call
// itself fails. The reconciler's wrapped error, its logged error line, and
// its recorded per-item error state must never carry the value that was
// sitting in memory when the failure happened.
func TestSentinel_Reconcile_CreateFailure_ErrorNeverLeaksValue(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("simulated apiserver rejection")
	})
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		sentinelValuesProvider(),
		fakeRemoteClientFn(client),
	)

	logs := captureLogs(t, func() { r.reconcile() })

	errsJoined := strings.Join(r.GetErrors(), "\n")
	if !strings.Contains(errsJoined, "simulated apiserver rejection") {
		t.Fatalf("expected the create failure to be recorded, got: %v", r.GetErrors())
	}
	if strings.Contains(errsJoined, sentinelValue) {
		t.Fatalf("sentinel leaked into the create-failure error text:\n%s", errsJoined)
	}
	if errMsg, ok := r.LastItemError("prod-cluster", "datadog"); ok && strings.Contains(errMsg, sentinelValue) {
		t.Fatalf("sentinel leaked into LastItemError on a create failure: %s", errMsg)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel leaked into logs on a create failure:\n%s", logs)
	}
}

// --- Failure path: the update write itself fails -----------------------------

// TestSentinel_Reconcile_UpdateFailure_ErrorNeverLeaksValue is the same
// proof on the rotate/update branch: a Sharko-owned Secret already exists
// with stale content, the new (sentinel-carrying) value is fetched and
// hash-compared, and the Kubernetes update call fails.
func TestSentinel_Reconcile_UpdateFailure_ErrorNeverLeaksValue(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secret",
			Namespace: "monitoring",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "sharko"},
		},
		Data: map[string][]byte{"api-key": []byte("stale-value"), "app-key": []byte("stale-app-value")},
	})
	client.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("simulated apiserver rejection on update")
	})
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		sentinelValuesProvider(),
		fakeRemoteClientFn(client),
	)

	logs := captureLogs(t, func() { r.reconcile() })

	errsJoined := strings.Join(r.GetErrors(), "\n")
	if !strings.Contains(errsJoined, "simulated apiserver rejection on update") {
		t.Fatalf("expected the update failure to be recorded, got: %v", r.GetErrors())
	}
	if strings.Contains(errsJoined, sentinelValue) {
		t.Fatalf("sentinel leaked into the update-failure error text:\n%s", errsJoined)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel leaked into logs on an update failure:\n%s", logs)
	}

	// The failed write must never have landed — the stale content is still
	// what is on the cluster.
	secret, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret should still exist: %v", err)
	}
	if string(secret.Data["api-key"]) != "stale-value" {
		t.Errorf("expected the stale value to remain after a failed update, got %q", secret.Data["api-key"])
	}
}

// --- Failure path: the fetch loop fails partway through ----------------------

// twoCallProvider deterministically simulates a partial fetch: the FIRST
// call to GetSecretValue (whichever key the reconciler asks for first — Go
// map iteration order over ref.Keys is randomized, so this cannot be pinned
// to a named key) succeeds and returns the sentinel; every call after that
// fails. This reliably reproduces "one key's value is already sitting in
// memory when fetching the next key fails" regardless of iteration order.
type twoCallProvider struct {
	calls int
}

func (p *twoCallProvider) GetSecretValue(_ context.Context, _ string) ([]byte, error) {
	p.calls++
	if p.calls == 1 {
		return []byte(sentinelValue), nil
	}
	return nil, errors.New("simulated fetch failure on the second key")
}

// TestSentinel_Reconcile_PartialFetchFailure_AlreadyFetchedValueNeverLeaks
// proves the fetch loop in reconcileSecret (internal/secrets/reconciler.go)
// never lets an already-successfully-fetched value leak into the error it
// returns when a LATER key in the same secret fails to fetch.
func TestSentinel_Reconcile_PartialFetchFailure_AlreadyFetchedValueNeverLeaks(t *testing.T) {
	client := fake.NewSimpleClientset()
	prov := &twoCallProvider{}
	r := newReconciler(
		standardGitReader(catalogWithSecrets), // datadog has two keys: api-key, app-key
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		prov,
		fakeRemoteClientFn(client),
	)

	logs := captureLogs(t, func() { r.reconcile() })

	stats := r.GetStats()
	if stats.Errors == 0 {
		t.Fatal("expected the second key's fetch to fail")
	}
	errsJoined := strings.Join(r.GetErrors(), "\n")
	if strings.Contains(errsJoined, sentinelValue) {
		t.Fatalf("the already-fetched sentinel leaked into the reconcile error text:\n%s", errsJoined)
	}
	if errMsg, ok := r.LastItemError("prod-cluster", "datadog"); ok && strings.Contains(errMsg, sentinelValue) {
		t.Fatalf("the already-fetched sentinel leaked into LastItemError: %s", errMsg)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("the already-fetched sentinel leaked into logs:\n%s", logs)
	}
	// Nothing should have been written at all — the item errored before any
	// Kubernetes write was attempted.
	if _, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); err == nil {
		t.Fatal("no secret should have been written when the fetch loop failed partway through")
	}
}

// --- Boundary path: a Secret that already exists and isn't Sharko's ---------

// TestSentinel_Reconcile_ForeignSecret_ValueFetchedButNeverWrittenOrLogged
// covers the ownership-gate boundary: the value IS fetched (reconcileSecret
// fetches desiredData before it checks who owns the existing Secret), and
// then discarded — never written, never logged — because the Secret
// belongs to someone else.
func TestSentinel_Reconcile_ForeignSecret_ValueFetchedButNeverWrittenOrLogged(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Secret{
		// No app.kubernetes.io/managed-by=sharko label — this is a foreign
		// Secret Sharko did not create.
		ObjectMeta: metav1.ObjectMeta{Name: "datadog-secret", Namespace: "monitoring"},
		Data:       map[string][]byte{"api-key": []byte("someone-elses-value")},
	})
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		sentinelValuesProvider(),
		fakeRemoteClientFn(client),
	)

	logs := captureLogs(t, func() { r.reconcile() })

	outcome, ok := r.LastItemOutcome("prod-cluster", "datadog")
	if !ok || outcome != string(ItemOutcomeForeign) {
		t.Fatalf("expected outcome=foreign, got %q (ok=%v)", outcome, ok)
	}
	if errs := r.GetErrors(); len(errs) != 0 {
		t.Errorf("a foreign secret is a boundary, not a failure — expected no errors, got %v", errs)
	}
	secret, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the foreign secret should still exist, untouched: %v", err)
	}
	if string(secret.Data["api-key"]) != "someone-elses-value" {
		t.Fatalf("the foreign secret's data must be untouched, got %q", secret.Data["api-key"])
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("the sentinel (fetched, then discarded because the secret is foreign) leaked into logs:\n%s", logs)
	}
}

// --- Single-item actions (S4: the row-level Sync / Refresh buttons) --------

// TestSentinel_SyncOneAndCheckOne_NeverLeak covers the two single-item
// entry points (SyncOne writes, CheckOne is read-only) that back the
// per-row "Sync" and "Refresh" buttons — a different code path through the
// same underlying primitives (reconcileSecret / checkWork) than the
// periodic pass above.
func TestSentinel_SyncOneAndCheckOne_NeverLeak(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		sentinelValuesProvider(),
		fakeRemoteClientFn(client),
	)

	var syncOutcome string
	var syncErr error
	logs := captureLogs(t, func() {
		syncOutcome, syncErr = r.SyncOne(context.Background(), "prod-cluster", "datadog")
	})
	if syncErr != nil {
		t.Fatalf("SyncOne: unexpected error: %v", syncErr)
	}
	if syncOutcome != string(ItemOutcomeCreated) {
		t.Fatalf("expected outcome=created, got %q", syncOutcome)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel leaked into SyncOne logs:\n%s", logs)
	}

	var checkOutcome string
	var checkErr error
	logs = captureLogs(t, func() {
		checkOutcome, checkErr = r.CheckOne(context.Background(), "prod-cluster", "datadog")
	})
	if checkErr != nil {
		t.Fatalf("CheckOne: unexpected error: %v", checkErr)
	}
	if checkOutcome != string(ItemOutcomeUnchanged) {
		t.Fatalf("expected outcome=unchanged after a matching sync, got %q", checkOutcome)
	}
	if strings.Contains(logs, sentinelValue) {
		t.Fatalf("sentinel leaked into CheckOne logs:\n%s", logs)
	}
}

package clusterreconciler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/providers"
)

const renewalCluster = "eks-renew"

// renewalT0 is when the first token in each test was made.
var renewalT0 = time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)

// fakeEKSToken builds a token shaped like a real EKS one: the fixed prefix and
// a base64url signed STS address carrying X-Amz-Date. marker makes each token
// unique and easy to look for in logs and audit entries.
func fakeEKSToken(issued time.Time, marker string) string {
	u := "https://sts.us-east-1.amazonaws.com/?Action=GetCallerIdentity&Version=2011-06-15" +
		"&X-Amz-Date=" + issued.UTC().Format("20060102T150405Z") +
		"&X-Amz-Expires=60&X-Amz-Signature=" + marker
	return "k8s-aws-v1." + base64.RawURLEncoding.EncodeToString([]byte(u))
}

// mintingVault stands in for the EKS backend: each GetCredentials is one mint.
type mintingVault struct {
	mu     sync.Mutex
	mints  int
	now    func() time.Time
	fail   bool
	server string
}

func (v *mintingVault) GetCredentials(string) (*providers.Kubeconfig, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.mints++
	if v.fail {
		return nil, errors.New("backend refused: SECRET-BACKEND-WORDS")
	}
	return &providers.Kubeconfig{
		Server: v.server,
		Token:  fakeEKSToken(v.now(), fmt.Sprintf("MINTED-TOKEN-%d", v.mints)),
	}, nil
}
func (v *mintingVault) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (v *mintingVault) SearchSecrets(string) ([]string, error)       { return nil, nil }
func (v *mintingVault) HealthCheck(context.Context) error            { return nil }

func (v *mintingVault) count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.mints
}

func renewalBody(credsSource string) []byte {
	return []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: ` + renewalCluster + `
      credsSource: ` + credsSource + `
      region: us-east-1
      labels:
        datadog: enabled
`)
}

type renewalRig struct {
	r      *Reconciler
	client *fake.Clientset
	vault  *mintingVault
	audits *auditCollector
	clock  *time.Time
}

// newRenewalRig makes a reconciler with a fake clock and lets its first pass
// create the connection Secret (one mint, the token made at renewalT0).
func newRenewalRig(t *testing.T, credsSource string) *renewalRig {
	t.Helper()
	clock := renewalT0
	vault := &mintingVault{now: func() time.Time { return clock }, server: "https://eks-renew.example.invalid"}
	client := fake.NewSimpleClientset()
	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, client, vault, audits, renewalBody(credsSource))
	r.nowFn = func() time.Time { return clock }
	rig := &renewalRig{r: r, client: client, vault: vault, audits: audits, clock: &clock}

	r.pollOnce(context.Background())
	// The fake client does not do what the API server does with StringData
	// (fold it into Data), so do it here, the way a real cluster stores it.
	if s, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), renewalCluster, metav1.GetOptions{}); err == nil && len(s.StringData) > 0 {
		if s.Data == nil {
			s.Data = map[string][]byte{}
		}
		for k, v := range s.StringData {
			s.Data[k] = []byte(v)
		}
		s.StringData = nil
		if _, err := client.CoreV1().Secrets(DefaultArgoCDNamespace).Update(context.Background(), s, metav1.UpdateOptions{}); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	if got := vault.count(); got != 1 {
		t.Fatalf("setup: the first pass minted %d tokens, want 1 (the create)", got)
	}
	if !strings.Contains(rig.config(t), fakeEKSToken(renewalT0, "MINTED-TOKEN-1")) {
		t.Fatal("setup: the first pass did not write the first token, so nothing below proves anything")
	}
	return rig
}

func (g *renewalRig) config(t *testing.T) string {
	t.Helper()
	s, err := g.client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), renewalCluster, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the connection Secret: %v", err)
	}
	return string(s.Data["config"])
}

func (g *renewalRig) advance(d time.Duration) { *g.clock = g.clock.Add(d) }

func (g *renewalRig) updates() int {
	n := 0
	for _, a := range g.client.Actions() {
		if a.GetVerb() == "update" && a.GetResource().Resource == "secrets" {
			n++
		}
	}
	return n
}

func TestTokenRenewal_OldTokenIsRenewedOnce(t *testing.T) {
	var logs bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	g := newRenewalRig(t, "eks-token")
	before := g.config(t)
	updatesBefore := g.updates()

	g.advance(11 * time.Minute)
	g.r.pollOnce(context.Background())

	if got := g.vault.count(); got != 2 {
		t.Fatalf("an 11-minute-old token led to %d mints in total, want 2 (create + one renewal)", got)
	}
	after := g.config(t)
	if !strings.Contains(after, fakeEKSToken(*g.clock, "MINTED-TOKEN-2")) {
		t.Fatalf("the renewal did not write the new token")
	}
	if g.updates() != updatesBefore+1 {
		t.Fatalf("the renewal made %d Secret updates, want exactly 1", g.updates()-updatesBefore)
	}
	// Only the token changed.
	var b, a map[string]any
	_ = json.Unmarshal([]byte(before), &b)
	_ = json.Unmarshal([]byte(after), &a)
	delete(b, "bearerToken")
	delete(a, "bearerToken")
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("the renewal changed more than the token:\nbefore %v\nafter  %v", b, a)
	}

	// The next pass, same minute: the new token is young, so no more mints.
	g.r.pollOnce(context.Background())
	if got := g.vault.count(); got != 2 {
		t.Fatalf("a pass right after a renewal minted again (%d mints in total, want 2)", got)
	}
	if g.r.TokenRenewalFailing(renewalCluster) {
		t.Error("a renewal that worked left the cluster marked as failing")
	}

	// A renewal is not a repair, and the token goes nowhere but the Secret.
	auditJSON, _ := json.Marshal(g.audits.Snapshot())
	for _, e := range g.audits.Snapshot() {
		if e.Event == EventClusterConnectionRepair || strings.Contains(strings.ToLower(e.Detail), "repair") {
			t.Errorf("a routine token renewal wrote a repair activity line: %+v", e)
		}
	}
	for _, needle := range []string{"MINTED-TOKEN", "k8s-aws-v1."} {
		if strings.Contains(string(auditJSON), needle) {
			t.Errorf("the audit trail carries token text %q", needle)
		}
		if strings.Contains(logs.String(), needle) {
			t.Errorf("the log carries token text %q", needle)
		}
	}
	if !strings.Contains(logs.String(), "connection token renewed") {
		t.Error("a renewal that worked left no log line saying so")
	}
	if rec, _ := g.r.LastReconcile(renewalCluster); rec.Outcome == OutcomeFailed {
		t.Errorf("a renewal that worked left the last reconcile failed: %q", rec.Message)
	}
}

func TestTokenRenewal_YoungTokenIsLeftAlone(t *testing.T) {
	g := newRenewalRig(t, "eks-token")
	before := g.config(t)
	updatesBefore := g.updates()

	g.advance(9 * time.Minute)
	g.r.pollOnce(context.Background())

	if got := g.vault.count(); got != 1 {
		t.Fatalf("a 9-minute-old token led to a mint (%d in total, want 1)", got)
	}
	if g.config(t) != before || g.updates() != updatesBefore {
		t.Fatal("a young token was rewritten")
	}
}

func TestTokenRenewal_NonEKSTokenClusterIsLeftAlone(t *testing.T) {
	// The Secret carries an EKS-looking token, but git says this cluster is a
	// stored kubeconfig, so its token is not Sharko's to renew.
	g := newRenewalRig(t, "secret-kubeconfig")
	before := g.config(t)

	g.advance(30 * time.Minute)
	g.r.pollOnce(context.Background())

	if got := g.vault.count(); got != 1 {
		t.Fatalf("a secret-kubeconfig cluster was minted for (%d mints in total, want 1)", got)
	}
	if g.config(t) != before {
		t.Fatal("a secret-kubeconfig cluster's connection was rewritten")
	}
}

func TestTokenRenewal_AdoptedSecretIsLeftAlone(t *testing.T) {
	g := newRenewalRig(t, "eks-token")
	s, _ := g.client.CoreV1().Secrets(DefaultArgoCDNamespace).Get(context.Background(), renewalCluster, metav1.GetOptions{})
	if s.Annotations == nil {
		s.Annotations = map[string]string{}
	}
	s.Annotations["sharko.dev/adopted"] = "true"
	if _, err := g.client.CoreV1().Secrets(DefaultArgoCDNamespace).Update(context.Background(), s, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}

	g.advance(11 * time.Minute)
	g.r.pollOnce(context.Background())
	if got := g.vault.count(); got != 1 {
		t.Fatalf("an adopted connection was minted for (%d mints in total, want 1)", got)
	}
}

func TestTokenRenewal_FailedMintShowsAsAProblem(t *testing.T) {
	var logs bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	g := newRenewalRig(t, "eks-token")
	before := g.config(t)
	g.vault.fail = true

	g.advance(11 * time.Minute)
	g.r.pollOnce(context.Background())

	if got := g.vault.count(); got != 2 {
		t.Fatalf("mints = %d, want 2 (create + one failed try)", got)
	}
	if g.config(t) != before {
		t.Fatal("a failed renewal changed the connection")
	}
	if !g.r.TokenRenewalFailing(renewalCluster) {
		t.Fatal("a failed renewal did not mark the cluster as failing — it would still show as Connected")
	}
	if strings.Contains(logs.String(), "SECRET-BACKEND-WORDS") {
		t.Error("the backend's own error text was logged")
	}
	auditJSON, _ := json.Marshal(g.audits.Snapshot())
	if strings.Contains(string(auditJSON), "SECRET-BACKEND-WORDS") {
		t.Error("the backend's own error text reached the audit trail")
	}
	failures := 0
	for _, e := range g.audits.Snapshot() {
		if e.Event == "cluster_connection_token_renewal_failed" {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("got %d renewal-failed audit entries, want 1", failures)
	}

	// 30 s later: still waiting, no new mint, no new audit line.
	g.advance(30 * time.Second)
	g.r.pollOnce(context.Background())
	if got := g.vault.count(); got != 2 {
		t.Fatalf("a failed renewal was retried on the very next pass (%d mints, want 2)", got)
	}

	// After the wait: one more try, still one audit line for the whole run.
	g.advance(2 * time.Minute)
	g.r.pollOnce(context.Background())
	if got := g.vault.count(); got != 3 {
		t.Fatalf("mints = %d after the wait, want 3", got)
	}
	failures = 0
	for _, e := range g.audits.Snapshot() {
		if e.Event == "cluster_connection_token_renewal_failed" {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("a run of failures wrote %d audit entries, want 1", failures)
	}

	// The backend works again: the next try renews and clears the mark.
	g.vault.fail = false
	g.advance(2*time.Minute + time.Second)
	g.r.pollOnce(context.Background())
	if g.r.TokenRenewalFailing(renewalCluster) {
		t.Fatal("a renewal that worked did not clear the failing mark")
	}
	if g.config(t) == before {
		t.Fatal("the renewal after recovery did not write a new token")
	}
}

func TestTokenRenewal_NoMintOnReadPaths(t *testing.T) {
	g := newRenewalRig(t, "eks-token")
	g.advance(14 * time.Minute)

	// The check pass and every read the API makes of the reconciler.
	g.r.checkOnce(context.Background())
	_, _ = g.r.LastReconcile(renewalCluster)
	_ = g.r.TokenRenewalFailing(renewalCluster)
	_, _, _, _ = g.r.LastError()

	if got := g.vault.count(); got != 1 {
		t.Fatalf("a read path minted a token (%d mints in total, want 1)", got)
	}
}

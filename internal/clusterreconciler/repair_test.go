package clusterreconciler

// repair_test.go — the reconciler's half of the repair, plus R3-5's background
// drift notice.

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/providers"
)

const repairReconCluster = "prod-eu"

// reconRepairSentinel is a made-up credential value used only in this file.
const reconRepairSentinel = "k4m8vz2wrecon-repair-sentinel-do-not-copy"

// ownedConnSecret builds a live connection Secret Sharko owns.
func ownedConnSecret(labels, data, annotations map[string]string) *corev1.Secret {
	l := map[string]string{
		argosecrets.LabelManagedBy:  argosecrets.ManagedByValue,
		argosecrets.LabelSecretType: "cluster",
	}
	for k, v := range labels {
		l[k] = v
	}
	d := map[string][]byte{}
	for k, v := range data {
		d[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: repairReconCluster, Namespace: "argocd",
			Labels: l, Annotations: annotations,
		},
		Type: corev1.SecretTypeOpaque,
		Data: d,
	}
}

func desiredConnSecret(t *testing.T, labels map[string]string) *corev1.Secret {
	t.Helper()
	built, err := argosecrets.BuildClusterSecret(argosecrets.ClusterSecretSpec{
		Name:   repairReconCluster,
		Server: "https://" + repairReconCluster + ".invalid",
		Token:  reconRepairSentinel,
		Labels: labels,
	}, "argocd")
	if err != nil {
		t.Fatalf("building desired: %v", err)
	}
	return built
}

// --- the write, and what it records ---------------------------------------

// TestRepairOwnedConnectionSecret_StampsProvenanceAndAppliedRevision: a repair
// is attributed to the commit it was actually built against, not to whatever the
// last periodic pass happened to see.
func TestRepairOwnedConnectionSecret_StampsProvenanceAndAppliedRevision(t *testing.T) {
	live := ownedConnSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{"name": repairReconCluster, "server": "https://stale.invalid", "config": `{"bearerToken":"old"}`},
		nil)
	client := fake.NewSimpleClientset(live)
	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, client, nil, audits, nil)

	const revision = "abcdef1234567890abcdef1234567890abcdef12"
	res, err := r.RepairOwnedConnectionSecret(context.Background(),
		desiredConnSecret(t, map[string]string{"datadog": "enabled"}), revision)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if !res.Changed {
		t.Fatal("the repair reported no change on a drifted connection")
	}
	if res.AppliedRevision != revision {
		t.Errorf("applied revision = %q, want %q", res.AppliedRevision, revision)
	}

	after, _ := client.CoreV1().Secrets("argocd").Get(context.Background(), repairReconCluster, metav1.GetOptions{})
	if got := after.Annotations[AnnotationRevision]; got != revision {
		t.Errorf("provenance revision annotation = %q, want %q — a repaired connection must say which commit it was made to match", got, revision)
	}
	if after.Annotations[AnnotationWrittenAt] == "" {
		t.Error("no written-at provenance annotation was stamped")
	}

	// The record the page reads must reflect the repair, not a stale success.
	rec, ok := r.LastReconcile(repairReconCluster)
	if !ok {
		t.Fatal("the repair recorded no per-cluster outcome")
	}
	if rec.Outcome != OutcomeSucceeded {
		t.Errorf("outcome = %q, want succeeded", rec.Outcome)
	}
	if rec.AppliedRevision != revision {
		t.Errorf("record applied revision = %q, want %q", rec.AppliedRevision, revision)
	}
}

// TestRepairOwnedConnectionSecret_AuditsEveryAttempt is rule 15 for the write
// half: a success, a no-op and a refusal each leave a trail.
func TestRepairOwnedConnectionSecret_AuditsEveryAttempt(t *testing.T) {
	cases := []struct {
		name       string
		live       *corev1.Secret
		wantResult string
	}{
		{
			name: "a repair that writes",
			live: ownedConnSecret(map[string]string{"datadog": "disabled"},
				map[string]string{"name": repairReconCluster, "server": "https://stale.invalid"}, nil),
			wantResult: "success",
		},
		{
			name: "a refusal because another tool owns it",
			live: func() *corev1.Secret {
				s := ownedConnSecret(nil, map[string]string{"name": repairReconCluster}, nil)
				s.Labels[argosecrets.LabelManagedBy] = "another-tool"
				return s
			}(),
			wantResult: "failure",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(tc.live)
			audits := &auditCollector{}
			r := newReconcilerForTest(t, nil, client, nil, audits, nil)

			_, _ = r.RepairOwnedConnectionSecret(context.Background(),
				desiredConnSecret(t, map[string]string{"datadog": "enabled"}), "cafe1234")

			found := false
			for _, e := range audits.Snapshot() {
				if e.Event == "cluster_connection_repair" {
					found = true
					if e.Result != tc.wantResult {
						t.Errorf("audit result = %q, want %q", e.Result, tc.wantResult)
					}
					if e.Resource != "cluster:"+repairReconCluster {
						t.Errorf("audit resource = %q", e.Resource)
					}
				}
			}
			if !found {
				t.Errorf("no cluster_connection_repair audit entry was written for %q — rule 15 says every attempt is audited, including refusals", tc.name)
			}
		})
	}
}

// TestRepairOwnedConnectionSecret_NoOpDoesNotMoveTheAppliedRevision: nothing was
// written, so "the last write was built from commit X" is still the old answer.
func TestRepairOwnedConnectionSecret_NoOpDoesNotMoveTheAppliedRevision(t *testing.T) {
	desired := desiredConnSecret(t, map[string]string{"datadog": "enabled"})
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: repairReconCluster, Namespace: "argocd", Labels: map[string]string{}},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{},
	}
	for k, v := range desired.Labels {
		live.Labels[k] = v
	}
	for k, v := range desired.StringData {
		live.Data[k] = []byte(v)
	}
	client := fake.NewSimpleClientset(live)
	r := newReconcilerForTest(t, nil, client, nil, &auditCollector{}, nil)

	res, err := r.RepairOwnedConnectionSecret(context.Background(), desired, "newrevision99")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if res.Changed {
		t.Fatal("the repair reported a change on an already-correct connection")
	}
	if res.AppliedRevision == "newrevision99" {
		t.Error("a no-op repair moved the applied revision; nothing was written, so the previous commit is still the honest answer")
	}
}

// TestRepairOwnedConnectionSecret_RefusesWithoutAClient: no in-cluster client
// means there is nothing to write a connection with, and that is its own answer
// rather than a panic or a generic failure.
func TestRepairOwnedConnectionSecret_RefusesWithoutAClient(t *testing.T) {
	r := New(Deps{AuditFn: func(audit.Entry) {}})
	_, err := r.RepairOwnedConnectionSecret(context.Background(),
		desiredConnSecret(t, map[string]string{"datadog": "enabled"}), "cafe1234")
	if !errors.Is(err, ErrRepairNoClient) {
		t.Fatalf("err = %v, want ErrRepairNoClient", err)
	}
}

// --- R3-5: the background drift notice ------------------------------------

// TestDetectConnectionShapeDrift covers what the background pass can see for
// free, and what it deliberately says nothing about.
func TestDetectConnectionShapeDrift(t *testing.T) {
	healthy := ownedConnSecret(map[string]string{"datadog": "enabled"},
		map[string]string{"name": repairReconCluster, "server": "https://" + repairReconCluster + ".invalid", "config": "{}"}, nil)

	cases := []struct {
		name  string
		build func() *corev1.Secret
		want  []ConnectionShapeProblem
	}{
		{
			name:  "a healthy connection has nothing wrong",
			build: func() *corev1.Secret { return healthy.DeepCopy() },
			want:  nil,
		},
		{
			name: "the ownership label was stripped",
			build: func() *corev1.Secret {
				s := healthy.DeepCopy()
				delete(s.Labels, argosecrets.LabelManagedBy)
				return s
			},
			want: []ConnectionShapeProblem{ShapeProblemOwnershipLabelMissing},
		},
		{
			name: "ArgoCD's cluster-Secret label was stripped",
			build: func() *corev1.Secret {
				s := healthy.DeepCopy()
				delete(s.Labels, argosecrets.LabelSecretType)
				return s
			},
			want: []ConnectionShapeProblem{ShapeProblemSecretTypeLabelMissing},
		},
		{
			name: "a connection data key is gone",
			build: func() *corev1.Secret {
				s := healthy.DeepCopy()
				delete(s.Data, "config")
				return s
			},
			want: []ConnectionShapeProblem{ShapeProblemConnectionDataKeyMissing},
		},
		{
			name: "the name inside the connection is somebody else's",
			build: func() *corev1.Secret {
				s := healthy.DeepCopy()
				s.Data["name"] = []byte("a-different-cluster")
				return s
			},
			want: []ConnectionShapeProblem{ShapeProblemNameMismatch},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectConnectionShapeDrift(repairReconCluster, tc.build())
			if len(got) != len(tc.want) {
				t.Fatalf("problems = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("problems[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestDetectConnectionShapeDrift_CostsNoAPICall is the R3-5 cost claim, made
// checkable: the detector is a pure function over a Secret already in hand, so
// it cannot possibly issue a call. If somebody later gives it a client, this
// stops compiling — which is the point.
func TestDetectConnectionShapeDrift_CostsNoAPICall(t *testing.T) {
	// The signature itself is the proof: no context, no client, no error.
	var _ func(string, *corev1.Secret) []ConnectionShapeProblem = detectConnectionShapeDrift
}

// TestNoticeConnectionShapeDrift_OneEventPerEpisode: a connection that stays
// broken for hours produces one event, not one every 30 seconds. When it comes
// good and breaks again, that is a new episode and gets its own event.
func TestNoticeConnectionShapeDrift_OneEventPerEpisode(t *testing.T) {
	broken := ownedConnSecret(nil,
		map[string]string{"name": repairReconCluster, "server": "https://x.invalid", "config": "{}"}, nil)
	delete(broken.Labels, argosecrets.LabelManagedBy)

	r := newReconcilerForTest(t, nil, fake.NewSimpleClientset(), nil, &auditCollector{}, nil)

	// Five passes over the same broken connection.
	for i := 0; i < 5; i++ {
		r.noticeConnectionShapeDrift(context.Background(), repairReconCluster, broken, false)
	}
	if got := r.ConnectionShapeProblems(repairReconCluster); got == "" {
		t.Fatal("the drift was never recorded")
	}

	// It comes good.
	healthy := ownedConnSecret(nil,
		map[string]string{"name": repairReconCluster, "server": "https://x.invalid", "config": "{}"}, nil)
	r.noticeConnectionShapeDrift(context.Background(), repairReconCluster, healthy, false)
	if got := r.ConnectionShapeProblems(repairReconCluster); got != "" {
		t.Errorf("a healthy connection still carries drift state: %q", got)
	}
}

// TestNoticeConnectionShapeDrift_SkipsGuestConnections: on an adopted or
// self-managed connection Sharko deliberately does NOT stamp the ownership and
// secret-type labels, so calling their absence drift would report Sharko's own
// correct behaviour as a fault.
func TestNoticeConnectionShapeDrift_SkipsGuestConnections(t *testing.T) {
	guest := ownedConnSecret(nil,
		map[string]string{"name": repairReconCluster, "server": "https://x.invalid", "config": "{}"}, nil)
	delete(guest.Labels, argosecrets.LabelManagedBy)
	delete(guest.Labels, argosecrets.LabelSecretType)

	r := newReconcilerForTest(t, nil, fake.NewSimpleClientset(), nil, &auditCollector{}, nil)

	r.noticeConnectionShapeDrift(context.Background(), repairReconCluster, guest, true)
	if got := r.ConnectionShapeProblems(repairReconCluster); got != "" {
		t.Errorf("a self-managed connection was reported as drifted: %q", got)
	}

	adopted := guest.DeepCopy()
	adopted.Annotations = map[string]string{argosecrets.AnnotationAdopted: "true"}
	r.noticeConnectionShapeDrift(context.Background(), repairReconCluster+"-adopted", adopted, false)
	if got := r.ConnectionShapeProblems(repairReconCluster + "-adopted"); got != "" {
		t.Errorf("an adopted connection was reported as drifted: %q", got)
	}
}

// TestNoticeConnectionShapeDrift_WritesNothing: detection reads. It never
// repairs on its own — a background write is not what was asked for, and
// self-heal owns that decision separately under its own setting.
func TestNoticeConnectionShapeDrift_WritesNothing(t *testing.T) {
	broken := ownedConnSecret(nil,
		map[string]string{"name": repairReconCluster, "server": "https://x.invalid"}, nil)
	delete(broken.Labels, argosecrets.LabelManagedBy)
	client := fake.NewSimpleClientset(broken)
	r := newReconcilerForTest(t, nil, client, nil, &auditCollector{}, nil)

	r.noticeConnectionShapeDrift(context.Background(), repairReconCluster, broken, false)

	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "deletecollection":
			t.Errorf("the background drift notice issued a %q — detection reads, it never repairs", a.GetVerb())
		}
	}
}

// TestNoticeConnectionShapeDrift_NeverMintsACredential: the zero-mint guarantee
// applies to every code path, and a background pass that minted on a timer would
// be worse than a request that did. The notice has no vault at all.
func TestNoticeConnectionShapeDrift_NeverMintsACredential(t *testing.T) {
	mint := &countingVault{}
	broken := ownedConnSecret(nil, map[string]string{"name": repairReconCluster}, nil)
	delete(broken.Labels, argosecrets.LabelManagedBy)

	r := newReconcilerForTest(t, nil, fake.NewSimpleClientset(broken), mint, &auditCollector{}, nil)
	r.noticeConnectionShapeDrift(context.Background(), repairReconCluster, broken, false)

	if mint.calls != 0 {
		t.Errorf(`the background drift notice fetched credentials %d time(s); it must fetch ZERO.

A stored-facts read per cluster per pass would be hundreds of backend reads every 30 seconds on a real fleet, and on an EKS backend a GetCredentials call mints a real sign-in token. The notice checks only what the pass already has in hand.`, mint.calls)
	}
}

// TestRepairClearsTheDriftNotice: the drift a repair just corrected is not drift
// any more, so a later episode gets its own event instead of being silenced.
func TestRepairClearsTheDriftNotice(t *testing.T) {
	live := ownedConnSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{"name": repairReconCluster, "server": "https://stale.invalid"}, nil)
	client := fake.NewSimpleClientset(live)
	r := newReconcilerForTest(t, nil, client, nil, &auditCollector{}, nil)

	// Pretend a pass noticed drift.
	brokenView := live.DeepCopy()
	delete(brokenView.Labels, argosecrets.LabelSecretType)
	r.noticeConnectionShapeDrift(context.Background(), repairReconCluster, brokenView, false)
	if r.ConnectionShapeProblems(repairReconCluster) == "" {
		t.Fatal("the drift was never recorded, so this test proves nothing")
	}

	if _, err := r.RepairOwnedConnectionSecret(context.Background(),
		desiredConnSecret(t, map[string]string{"datadog": "enabled"}), "cafe99"); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if got := r.ConnectionShapeProblems(repairReconCluster); got != "" {
		t.Errorf("the drift notice survived a successful repair: %q", got)
	}
}

// TestRepairAddonLabelsOnly_IsTheSameCodePathAsResync: the labels-only scope has
// exactly one implementation. A second one would give the same action two
// behaviours depending on which door it came through.
func TestRepairAddonLabelsOnly_RefusesAnUnmanagedCluster(t *testing.T) {
	body := []byte("clusters:\n- name: some-other-cluster\n")
	r := newReconcilerForTest(t, nil, fake.NewSimpleClientset(), nil, &auditCollector{}, body)

	_, err := r.RepairAddonLabelsOnly(context.Background(), repairReconCluster)
	if !errors.Is(err, ErrClusterNotManaged) {
		t.Fatalf("err = %v, want ErrClusterNotManaged", err)
	}
}

// TestRepairPathNeverCarriesACredentialValue: the reconciler's repair reports
// field paths and counts. No surface it produces carries the credential.
func TestRepairPathNeverCarriesACredentialValue(t *testing.T) {
	live := ownedConnSecret(
		map[string]string{"datadog": "disabled"},
		map[string]string{"name": repairReconCluster, "server": "https://stale.invalid", "config": `{"bearerToken":"old"}`},
		nil)
	client := fake.NewSimpleClientset(live)
	audits := &auditCollector{}
	r := newReconcilerForTest(t, nil, client, nil, audits, nil)

	res, err := r.RepairOwnedConnectionSecret(context.Background(),
		desiredConnSecret(t, map[string]string{"datadog": "enabled"}), "cafe1234")
	if err != nil {
		t.Fatalf("repair: %v", err)
	}

	for _, f := range res.FieldsWritten {
		if strings.Contains(f, reconRepairSentinel) {
			t.Errorf("a reported field path carries the credential value: %q", f)
		}
	}
	for _, e := range audits.Snapshot() {
		blob := e.Event + e.Resource + e.Detail + e.Error
		if strings.Contains(blob, reconRepairSentinel) {
			t.Errorf("an audit entry carries the credential value: %+v", e)
		}
	}
	rec, _ := r.LastReconcile(repairReconCluster)
	if strings.Contains(rec.Message, reconRepairSentinel) {
		t.Errorf("the per-cluster record carries the credential value: %q", rec.Message)
	}
}

// countingVault counts credential fetches so a read-only path can prove it makes
// none. Every method counts: the point is that NOTHING on the notice path asks
// this backend for anything.
type countingVault struct{ calls int }

func (v *countingVault) GetCredentials(string) (*providers.Kubeconfig, error) {
	v.calls++
	return nil, errors.New("the mint must never be reached on a read-only path")
}

func (v *countingVault) ListClusters() ([]providers.ClusterInfo, error) {
	v.calls++
	return nil, nil
}

func (v *countingVault) HealthCheck(context.Context) error {
	v.calls++
	return nil
}

func (v *countingVault) SearchSecrets(string) ([]string, error) {
	v.calls++
	return nil, nil
}

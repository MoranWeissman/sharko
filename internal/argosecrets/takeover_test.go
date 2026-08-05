package argosecrets

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

const takeoverNS = "argocd"

// legacyKey is the classifier the API layer passes in: on a v4 repo,
// anything that is not ArgoCD's own marker and not one of Sharko's own keys
// belonged to whoever managed the cluster before.
func legacyKey(key string) bool {
	switch key {
	case LabelSecretType, LabelManagedBy,
		models.LabelConnectivityCheck, models.LabelConnectivityCheckLegacy:
		return false
	}
	return !models.IsV4AddonLabelKey(key)
}

// brownfieldSecret is a cluster Secret somebody else set up: ArgoCD's
// marker, their own labels, real connection data, no Sharko anywhere.
func brownfieldSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eu",
			Namespace: takeoverNS,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				"env":           "prod",
				"team":          "platform",
			},
			Annotations: map[string]string{"note": "created by hand"},
		},
		Data: map[string][]byte{
			"name":   []byte("prod-eu"),
			"server": []byte("https://prod-eu.example.org"),
			"config": []byte(`{"bearerToken":"super-secret","tlsClientConfig":{}}`),
		},
	}
}

// newTakeoverManager builds a Manager over a fake clientset seeded through
// Create, so the objects land the way a real API server would return them.
func newTakeoverManager(objs ...*corev1.Secret) (*Manager, *fake.Clientset) {
	client := fake.NewSimpleClientset()
	for _, o := range objs {
		_, _ = client.CoreV1().Secrets(o.Namespace).Create(context.Background(), o, metav1.CreateOptions{})
	}
	return NewManager(client, takeoverNS), client
}

func getSecret(t *testing.T, client *fake.Clientset, name string) *corev1.Secret {
	t.Helper()
	s, err := client.CoreV1().Secrets(takeoverNS).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading back %q: %v", name, err)
	}
	return s
}

func TestGetClusterSecretDetail_ReadsMetadataAndServerButNotCredentials(t *testing.T) {
	m, _ := newTakeoverManager(brownfieldSecret())

	detail, err := m.GetClusterSecretDetail(context.Background(), "prod-eu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !detail.Found {
		t.Fatal("Found = false for a Secret that exists")
	}
	if detail.Server != "https://prod-eu.example.org" {
		t.Errorf("Server = %q", detail.Server)
	}
	if detail.ManagedBy != "" {
		t.Errorf("ManagedBy = %q, want empty for an unclaimed connection", detail.ManagedBy)
	}
	if detail.Labels["env"] != "prod" {
		t.Errorf("labels not returned: %v", detail.Labels)
	}
	// Mutating the returned maps must not touch the live object.
	detail.Labels["env"] = "mutated"
	again, _ := m.GetClusterSecretDetail(context.Background(), "prod-eu")
	if again.Labels["env"] != "prod" {
		t.Error("the returned label map is not a copy — a caller mutated the cached object")
	}
}

func TestGetClusterSecretDetail_MissingIsNotAnError(t *testing.T) {
	m, _ := newTakeoverManager()
	detail, err := m.GetClusterSecretDetail(context.Background(), "nope")
	if err != nil {
		t.Fatalf("a missing Secret must not be an error: %v", err)
	}
	if detail.Found {
		t.Error("Found = true for a Secret that does not exist")
	}
}

// TestTakeOverClusterSecret_NoWindowWithoutASecret is the ordering
// guarantee: the swap is one update on the existing object, so the Secret
// exists before, during and after — and its connection data is byte-for-byte
// what it was.
func TestTakeOverClusterSecret_NoWindowWithoutASecret(t *testing.T) {
	before := brownfieldSecret()
	m, client := newTakeoverManager(before)

	var deletes int
	var creates int
	client.PrependReactor("delete", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		deletes++
		return false, nil, nil
	})
	client.PrependReactor("create", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		creates++
		return false, nil, nil
	})

	res, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "2026-07-30T10:00:00Z", legacyKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deletes != 0 {
		t.Errorf("the swap deleted something (%d deletes) — the Secret must never be absent", deletes)
	}
	if creates != 0 {
		t.Errorf("the swap created a second Secret (%d creates) — it must be an in-place update", creates)
	}
	if !res.Changed || res.AlreadyOwned {
		t.Errorf("res = %+v, want a change and not already-owned", res)
	}

	after := getSecret(t, client, "prod-eu")
	if string(after.Data["config"]) != string(before.Data["config"]) {
		t.Error("the credential material changed — the swap must never touch config")
	}
	if string(after.Data["server"]) != "https://prod-eu.example.org" {
		t.Errorf("server address changed: %q", after.Data["server"])
	}
	if string(after.Data["name"]) != "prod-eu" {
		t.Errorf("cluster name changed: %q", after.Data["name"])
	}
	if after.Name != "prod-eu" {
		t.Errorf("the Secret name changed: %q", after.Name)
	}
}

func TestTakeOverClusterSecret_PreservesLegacyLabelsAndRecordsThem(t *testing.T) {
	m, client := newTakeoverManager(brownfieldSecret())

	res, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "2026-07-30T10:00:00Z", legacyKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.PreservedLabels) != 2 || res.PreservedLabels["env"] != "prod" || res.PreservedLabels["team"] != "platform" {
		t.Fatalf("PreservedLabels = %v, want env+team", res.PreservedLabels)
	}
	if len(res.DroppedLabels) != 0 {
		t.Errorf("nothing should have been dropped: %v", res.DroppedLabels)
	}

	after := getSecret(t, client, "prod-eu")
	if after.Labels["env"] != "prod" || after.Labels["team"] != "platform" {
		t.Errorf("legacy labels were not carried over: %v", after.Labels)
	}
	if after.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("ownership label not stamped: %v", after.Labels)
	}
	if after.Labels[LabelSecretType] != "cluster" {
		t.Errorf("ArgoCD's cluster marker went missing: %v", after.Labels)
	}
	if !IsAdopted(after.Annotations) {
		t.Error("the taken-over Secret must be marked adopted so the sweeps leave it alone")
	}
	if after.Annotations[AnnotationTakenOverAt] != "2026-07-30T10:00:00Z" {
		t.Errorf("takeover time not recorded: %v", after.Annotations)
	}
	if got := after.Annotations[AnnotationTakeoverPreservedLabels]; got != "env,team" {
		t.Errorf("preserved-label record = %q, want %q (sorted)", got, "env,team")
	}
	if after.Annotations["note"] != "created by hand" {
		t.Error("an unrelated annotation was lost")
	}
}

func TestTakeOverClusterSecret_CanDropLegacyLabelsWhenAsked(t *testing.T) {
	m, client := newTakeoverManager(brownfieldSecret())

	res, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", false, "2026-07-30T10:00:00Z", legacyKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.DroppedLabels) != 2 {
		t.Fatalf("DroppedLabels = %v, want env+team", res.DroppedLabels)
	}
	after := getSecret(t, client, "prod-eu")
	if _, ok := after.Labels["env"]; ok {
		t.Error("env should have been removed")
	}
	if _, ok := after.Annotations[AnnotationTakeoverPreservedLabels]; ok {
		t.Error("nothing was preserved, so there must be no preserved-label record")
	}
	if after.Labels[LabelSecretType] != "cluster" {
		t.Error("ArgoCD's cluster marker must survive even a full label drop")
	}
}

func TestTakeOverClusterSecret_AlreadyOwnedWithTheRecordInPlaceIsANoOp(t *testing.T) {
	s := brownfieldSecret()
	s.Labels[LabelManagedBy] = ManagedByValue
	s.Annotations[AnnotationTakeoverPreservedLabels] = "env,team"
	m, client := newTakeoverManager(s)

	res, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "2026-07-30T10:00:00Z", legacyKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.AlreadyOwned || res.Changed || res.ProtectionRepaired {
		t.Errorf("res = %+v, want already-owned, unchanged, nothing repaired", res)
	}
	if IsAdopted(getSecret(t, client, "prod-eu").Annotations) {
		t.Error("a no-op must not write anything at all")
	}
}

// A cluster brought in by the older adopt path carries Sharko's ownership
// label and the adopted marker, but nothing ever wrote down WHICH of its
// labels came from the previous owner. Without that record the next label
// sync reads those labels as Sharko's own and a self-heal run strips them.
// Re-running the takeover is what repairs it.
func TestTakeOverClusterSecret_AlreadyOwnedRepairsAMissingPreservedLabelsRecord(t *testing.T) {
	s := brownfieldSecret()
	s.Labels[LabelManagedBy] = ManagedByValue
	s.Annotations[AnnotationAdopted] = "true"
	// No AnnotationTakeoverPreservedLabels — this is the hole being repaired.
	m, client := newTakeoverManager(s)

	res, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "2026-07-30T10:00:00Z", legacyKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.AlreadyOwned {
		t.Errorf("AlreadyOwned = false, want true (res = %+v)", res)
	}
	if !res.ProtectionRepaired {
		t.Errorf("ProtectionRepaired = false — the missing record was not written back (res = %+v)", res)
	}
	if res.Changed {
		t.Errorf("Changed = true — the connection's owner did not change, only the record was filled in")
	}
	if len(res.PreservedLabels) != 2 || res.PreservedLabels["env"] != "prod" || res.PreservedLabels["team"] != "platform" {
		t.Errorf("PreservedLabels = %v, want env+team", res.PreservedLabels)
	}

	after := getSecret(t, client, "prod-eu")
	if got := after.Annotations[AnnotationTakeoverPreservedLabels]; got != "env,team" {
		t.Fatalf("preserved-label record = %q, want %q", got, "env,team")
	}
	// Metadata only: the labels, the connection data and the credentials are
	// exactly as they were.
	if after.Labels["env"] != "prod" || after.Labels["team"] != "platform" {
		t.Errorf("the repair changed the labels: %v", after.Labels)
	}
	if string(after.Data["server"]) != "https://prod-eu.example.org" {
		t.Errorf("the repair changed the connection address: %q", after.Data["server"])
	}
	if !strings.Contains(string(after.Data["config"]), "super-secret") {
		t.Errorf("the repair touched the credentials: %q", after.Data["config"])
	}

	// Running it a second time writes nothing more.
	res2, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "2026-07-30T10:00:00Z", legacyKey)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res2.ProtectionRepaired {
		t.Error("the repair ran again on a Secret that already has the record")
	}
}

// Sharko already owns it and there is nothing from a previous owner on it —
// there is no record to write, so nothing is written.
func TestTakeOverClusterSecret_AlreadyOwnedWithNoForeignLabelsWritesNothing(t *testing.T) {
	s := brownfieldSecret()
	delete(s.Labels, "env")
	delete(s.Labels, "team")
	s.Labels[LabelManagedBy] = ManagedByValue
	m, client := newTakeoverManager(s)

	res, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "2026-07-30T10:00:00Z", legacyKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProtectionRepaired {
		t.Errorf("nothing to record, but ProtectionRepaired = true (res = %+v)", res)
	}
	if _, ok := getSecret(t, client, "prod-eu").Annotations[AnnotationTakeoverPreservedLabels]; ok {
		t.Error("an empty record was written")
	}
}

func TestTakeOverClusterSecret_MissingSecretIsReportedNotErrored(t *testing.T) {
	m, _ := newTakeoverManager()
	res, err := m.TakeOverClusterSecret(context.Background(), "ghost", true, "", legacyKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Found {
		t.Error("Found = true for a Secret that does not exist")
	}
}

func TestTakeOverClusterSecret_StripsTheConnectivityCheckLabel(t *testing.T) {
	s := brownfieldSecret()
	s.Labels[models.LabelConnectivityCheck] = "enabled"
	m, client := newTakeoverManager(s)

	if _, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "", legacyKey); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if models.HasConnectivityCheckLabel(getSecret(t, client, "prod-eu").Labels) {
		t.Error("a cluster Sharko did not set up must never carry the connectivity-check label")
	}
}

func TestPreservedLabelKeysRoundTrip(t *testing.T) {
	if got := PreservedLabelKeys(nil); got != nil {
		t.Errorf("nil annotations = %v, want nil", got)
	}
	if got := PreservedLabelKeys(map[string]string{AnnotationTakeoverPreservedLabels: "  "}); got != nil {
		t.Errorf("blank annotation = %v, want nil", got)
	}
	encoded := EncodePreservedLabelKeys([]string{"team", "env", "zone"})
	if encoded != "env,team,zone" {
		t.Errorf("EncodePreservedLabelKeys = %q, want sorted", encoded)
	}
	got := PreservedLabelKeys(map[string]string{AnnotationTakeoverPreservedLabels: " env , team ,,zone"})
	if len(got) != 3 || got[0] != "env" || got[2] != "zone" {
		t.Errorf("PreservedLabelKeys = %v", got)
	}
}

func TestDropLabels_RemovesOnlyWhatWasAskedAndTrimsTheRecord(t *testing.T) {
	m, client := newTakeoverManager(brownfieldSecret())
	if _, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "", legacyKey); err != nil {
		t.Fatalf("takeover failed: %v", err)
	}

	removed, found, err := m.DropLabels(context.Background(), "prod-eu", []string{"env"})
	if err != nil || !found {
		t.Fatalf("DropLabels: err=%v found=%v", err, found)
	}
	if len(removed) != 1 || removed[0] != "env" {
		t.Fatalf("removed = %v, want [env]", removed)
	}

	after := getSecret(t, client, "prod-eu")
	if _, ok := after.Labels["env"]; ok {
		t.Error("env is still there")
	}
	if after.Labels["team"] != "platform" {
		t.Error("team was removed but was not asked for")
	}
	if got := after.Annotations[AnnotationTakeoverPreservedLabels]; got != "team" {
		t.Errorf("the record should now list only team, got %q", got)
	}
	if string(after.Data["config"]) == "" {
		t.Error("credential material was touched by a label drop")
	}

	// Dropping the last one clears the record entirely.
	if _, _, err := m.DropLabels(context.Background(), "prod-eu", []string{"team"}); err != nil {
		t.Fatalf("second drop failed: %v", err)
	}
	if _, ok := getSecret(t, client, "prod-eu").Annotations[AnnotationTakeoverPreservedLabels]; ok {
		t.Error("with nothing left preserved, the record must be gone")
	}
}

func TestDropLabels_IsIdempotent(t *testing.T) {
	m, _ := newTakeoverManager(brownfieldSecret())
	if _, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "", legacyKey); err != nil {
		t.Fatalf("takeover failed: %v", err)
	}
	if _, _, err := m.DropLabels(context.Background(), "prod-eu", []string{"env"}); err != nil {
		t.Fatalf("first drop: %v", err)
	}
	removed, found, err := m.DropLabels(context.Background(), "prod-eu", []string{"env"})
	if err != nil || !found {
		t.Fatalf("second drop: err=%v found=%v", err, found)
	}
	if len(removed) != 0 {
		t.Errorf("a repeat drop removed %v, want nothing", removed)
	}
}

func TestDropLabels_RefusesTheLabelsThatMustNeverGo(t *testing.T) {
	m, _ := newTakeoverManager(brownfieldSecret())

	for _, key := range []string{LabelSecretType, LabelManagedBy, models.V4AddonLabelPrefix + "datadog", ""} {
		_, _, err := m.DropLabels(context.Background(), "prod-eu", []string{key})
		if err == nil {
			t.Errorf("dropping %q was allowed — it must be refused", key)
			continue
		}
		if !strings.Contains(err.Error(), "refusing to remove label") {
			t.Errorf("dropping %q: error should say it is refusing, got %v", key, err)
		}
	}
}

func TestDropLabels_RefusesASecretSharkoDoesNotOwn(t *testing.T) {
	m, _ := newTakeoverManager(brownfieldSecret())

	_, found, err := m.DropLabels(context.Background(), "prod-eu", []string{"env"})
	if err == nil {
		t.Fatal("dropping labels off a connection Sharko does not own must be refused")
	}
	if !found {
		t.Error("found should be true — the Secret exists, it is just not ours")
	}
	if !strings.Contains(err.Error(), "take it over first") {
		t.Errorf("the error should say what to do about it: %v", err)
	}
}

func TestDropLabels_MissingSecretIsReportedNotErrored(t *testing.T) {
	m, _ := newTakeoverManager()
	_, found, err := m.DropLabels(context.Background(), "ghost", []string{"env"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("found = true for a Secret that does not exist")
	}
}

// TestSyncManagedClusterLabels_NeverEatsPreservedLabels is the regression
// guard for the trap this epic walked into: an unqualified legacy label like
// "env" looks exactly like a Sharko addon label to the self-heal loop, so
// without the preserved-labels record the first tick after a takeover would
// silently delete it.
func TestSyncManagedClusterLabels_NeverEatsPreservedLabels(t *testing.T) {
	m, client := newTakeoverManager(brownfieldSecret())
	if _, err := m.TakeOverClusterSecret(context.Background(), "prod-eu", true, "", legacyKey); err != nil {
		t.Fatalf("takeover failed: %v", err)
	}

	// Git declares one addon and knows nothing about env/team.
	if _, err := m.SyncManagedClusterLabels(context.Background(), "prod-eu",
		map[string]string{"datadog": "enabled"}, nil); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	after := getSecret(t, client, "prod-eu")
	if after.Labels["env"] != "prod" {
		t.Errorf("self-heal ate a preserved legacy label: %v", after.Labels)
	}
	if after.Labels["team"] != "platform" {
		t.Errorf("self-heal ate a preserved legacy label: %v", after.Labels)
	}
	if after.Labels["datadog"] != "enabled" {
		t.Errorf("the git-declared addon label was not applied: %v", after.Labels)
	}

	// A stale addon key that was never preserved still gets converged away.
	if _, err := m.SyncManagedClusterLabels(context.Background(), "prod-eu",
		map[string]string{}, nil); err != nil {
		t.Fatalf("second sync failed: %v", err)
	}
	after = getSecret(t, client, "prod-eu")
	if _, ok := after.Labels["datadog"]; ok {
		t.Error("a genuine addon label git no longer declares must still be removed")
	}
	if after.Labels["env"] != "prod" {
		t.Error("the preserved label must survive convergence forever, until it is dropped explicitly")
	}
}

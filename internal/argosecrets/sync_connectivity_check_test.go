package argosecrets

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TestSyncConnectivityCheckLabel_ZeroAddons_StampsLabel verifies the walk
// finding "bare spoke" fix: a managed Secret that predates the label (or
// otherwise never got it) with zero enabled addons has the label stamped —
// both key spellings — on the very next sync.
func TestSyncConnectivityCheckLabel_ZeroAddons_StampsLabel(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelManagedBy:  ManagedByValue,
				LabelSecretType: "cluster",
				// no connectivity-check label — the bare-spoke bug
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncConnectivityCheckLabel(context.Background(), "spoke-us", map[string]string{}, true)
	if err != nil {
		t.Fatalf("SyncConnectivityCheckLabel: %v", err)
	}
	if !found || !changed {
		t.Fatalf("expected found=true changed=true, got found=%v changed=%v", found, changed)
	}

	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "spoke-us", metav1.GetOptions{})
	if got.Labels[models.LabelConnectivityCheck] != models.LabelEnabled {
		t.Error("canonical connectivity-check label not stamped")
	}
	if got.Labels[models.LabelConnectivityCheckLegacy] != models.LabelEnabled {
		t.Error("legacy connectivity-check label not stamped")
	}
	if got.Labels[LabelManagedBy] != ManagedByValue {
		t.Error("managed-by must be untouched")
	}
}

// TestSyncConnectivityCheckLabel_FirstAddonEnabled_RemovesLabel verifies the
// other direction: once the git-desired set shows an enabled addon, a
// previously-stamped label is removed (both spellings).
func TestSyncConnectivityCheckLabel_FirstAddonEnabled_RemovesLabel(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelManagedBy:                      ManagedByValue,
				LabelSecretType:                     "cluster",
				models.LabelConnectivityCheck:       models.LabelEnabled,
				models.LabelConnectivityCheckLegacy: models.LabelEnabled,
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncConnectivityCheckLabel(context.Background(), "spoke-us", map[string]string{"datadog": models.LabelEnabled}, true)
	if err != nil {
		t.Fatalf("SyncConnectivityCheckLabel: %v", err)
	}
	if !found || !changed {
		t.Fatalf("expected found=true changed=true, got found=%v changed=%v", found, changed)
	}

	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "spoke-us", metav1.GetOptions{})
	if models.HasConnectivityCheckLabel(got.Labels) {
		t.Error("connectivity-check label should be removed once an addon is enabled")
	}
}

// TestSyncConnectivityCheckLabel_AllAddonsDisabled_LabelReturns verifies the
// self-healing round trip: disabling the last enabled addon brings the label
// back.
func TestSyncConnectivityCheckLabel_AllAddonsDisabled_LabelReturns(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelManagedBy:  ManagedByValue,
				LabelSecretType: "cluster",
				"datadog":       models.LabelEnabled,
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncConnectivityCheckLabel(context.Background(), "spoke-us", map[string]string{"datadog": models.LabelDisabled}, true)
	if err != nil {
		t.Fatalf("SyncConnectivityCheckLabel: %v", err)
	}
	if !found || !changed {
		t.Fatalf("expected found=true changed=true, got found=%v changed=%v", found, changed)
	}

	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "spoke-us", metav1.GetOptions{})
	if !models.HasConnectivityCheckLabel(got.Labels) {
		t.Error("connectivity-check label should return once all addons are disabled")
	}
}

// TestSyncConnectivityCheckLabel_FeatureOff_StripsLabel verifies the
// feature-toggle escape hatch: featureOn=false always strips the label,
// regardless of the enabled-addon count.
func TestSyncConnectivityCheckLabel_FeatureOff_StripsLabel(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelManagedBy:                ManagedByValue,
				LabelSecretType:               "cluster",
				models.LabelConnectivityCheck: models.LabelEnabled,
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncConnectivityCheckLabel(context.Background(), "spoke-us", map[string]string{}, false)
	if err != nil {
		t.Fatalf("SyncConnectivityCheckLabel: %v", err)
	}
	if !found || !changed {
		t.Fatalf("expected found=true changed=true, got found=%v changed=%v", found, changed)
	}

	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "spoke-us", metav1.GetOptions{})
	if models.HasConnectivityCheckLabel(got.Labels) {
		t.Error("feature off must strip the connectivity-check label")
	}
}

// TestSyncConnectivityCheckLabel_Adopted_NeverStamped verifies the adopted
// gate: an adopted (guest) Secret with zero enabled addons never gets the
// label, even with featureOn=true.
func TestSyncConnectivityCheckLabel_Adopted_NeverStamped(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				// no managed-by — adopted secrets are guests
			},
			Annotations: map[string]string{AnnotationAdopted: "true"},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncConnectivityCheckLabel(context.Background(), "adopted", map[string]string{}, true)
	if err != nil {
		t.Fatalf("SyncConnectivityCheckLabel: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if changed {
		t.Error("adopted secret without the label should need no write (never stamped)")
	}

	got, _ := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "adopted", metav1.GetOptions{})
	if models.HasConnectivityCheckLabel(got.Labels) {
		t.Error("adopted secret must never carry the connectivity-check label")
	}
}

// TestSyncConnectivityCheckLabel_NoChurn verifies no K8s write when the
// label already matches the derived desired state.
func TestSyncConnectivityCheckLabel_NoChurn(t *testing.T) {
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelManagedBy:                      ManagedByValue,
				LabelSecretType:                     "cluster",
				models.LabelConnectivityCheck:       models.LabelEnabled,
				models.LabelConnectivityCheckLegacy: models.LabelEnabled,
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncConnectivityCheckLabel(context.Background(), "spoke-us", map[string]string{}, true)
	if err != nil {
		t.Fatalf("SyncConnectivityCheckLabel: %v", err)
	}
	if !found || changed {
		t.Errorf("expected found=true changed=false (no churn), got found=%v changed=%v", found, changed)
	}
}

// TestSyncConnectivityCheckLabel_NotFound verifies the not-found contract
// shared with SyncManagedClusterLabels / SyncLabelsOnly.
func TestSyncConnectivityCheckLabel_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncConnectivityCheckLabel(context.Background(), "missing", map[string]string{}, true)
	if err != nil {
		t.Fatalf("not-found must not be an error: %v", err)
	}
	if found || changed {
		t.Errorf("expected found=false changed=false for a missing secret, got found=%v changed=%v", found, changed)
	}
}

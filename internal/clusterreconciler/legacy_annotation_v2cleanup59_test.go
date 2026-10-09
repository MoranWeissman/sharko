package clusterreconciler

// V2-cleanup-59 regression tests for the orphan sweep's handling of
// PRE-RENAME annotation keys (sharko.io — the domain the project never
// owned). A live cluster upgraded across the rename still carries the old
// keys on its ArgoCD Secrets; the sweep must keep honouring them:
//
//   - legacy adopted annotation  → Secret is delete-proof (the annotation
//     exists precisely to protect it from orphan deletion)
//   - legacy registration-pending annotation → grace window still honoured;
//     the clear pass removes the OLD key once the cluster is in git
//
// Writers stamp only the new sharko.dev keys — pinned elsewhere.

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// TestPollOnce_LegacyAdoptedOrphan_NeverDeleted: a Secret carrying ONLY the
// pre-rename adopted annotation is still recognised as adopted and survives
// the orphan sweep. This is the single most dangerous regression of the
// rename — getting it wrong deletes a real user's adopted cluster Secret.
func TestPollOnce_LegacyAdoptedOrphan_NeverDeleted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	legacyAdopted := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "legacy-adopted-cluster",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
			Annotations: map[string]string{
				// ONLY the pre-rename key — no sharko.dev annotation at all.
				argosecrets.AnnotationAdoptedLegacy: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"server": []byte("https://legacy-adopted.example.com"),
			"config": []byte(`{"bearerToken":"foreign-tok"}`),
		},
	}

	// Git says zero clusters — the Secret is an orphan candidate.
	body := envelopedManagedClusters()
	k8sClient := fake.NewSimpleClientset(legacyAdopted)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	got, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "legacy-adopted-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("legacy-adopted Secret must survive the orphan sweep: %v", err)
	}
	if string(got.Data["config"]) != `{"bearerToken":"foreign-tok"}` {
		t.Errorf("legacy-adopted Secret Data was mutated: %q", string(got.Data["config"]))
	}
	// The skip must be audited exactly like a new-key adopted skip.
	if !hasEventForResource(audits.Snapshot(), "cluster_secret_skip_adopted", "cluster:legacy-adopted-cluster") {
		t.Fatalf("expected cluster_secret_skip_adopted audit; got %v", audits.Snapshot())
	}
	for _, a := range k8sClient.Actions() {
		if a.GetVerb() == "delete" {
			t.Errorf("unexpected delete action on legacy-adopted Secret: %s", actionTarget(a))
		}
	}
}

// TestPollOnce_LegacyRegistrationPending_GraceWindowHonoured: an in-flight
// registration stamped with the OLD pending key at upgrade time keeps its
// grace window — the sweep must not delete the Secret mid-registration.
func TestPollOnce_LegacyRegistrationPending_GraceWindowHonoured(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pending := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mid-registration",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
			Annotations: map[string]string{
				// OLD key, freshly stamped (well inside the grace window).
				models.AnnotationRegistrationPendingLegacy: models.RegistrationPendingTimestamp(time.Now()),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"server": []byte("https://mid.example.com")},
	}

	// Not in git yet — its registration PR has not merged.
	body := envelopedManagedClusters()
	k8sClient := fake.NewSimpleClientset(pending)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "mid-registration", metav1.GetOptions{}); err != nil {
		t.Fatalf("Secret with legacy registration-pending annotation must survive the sweep inside the grace window: %v", err)
	}
	for _, a := range k8sClient.Actions() {
		if a.GetVerb() == "delete" {
			t.Errorf("unexpected delete during legacy pending grace window: %s", actionTarget(a))
		}
	}
}

// TestPollOnce_LegacyRegistrationPending_ClearedOnceInGit: once the cluster
// IS in git, the clear pass must strip the LEGACY pending key too — leaving
// it behind would keep the Secret sweep-immune under the old spelling.
func TestPollOnce_LegacyRegistrationPending_ClearedOnceInGit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pending := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "merged-cluster",
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
			Annotations: map[string]string{
				models.AnnotationRegistrationPendingLegacy: models.RegistrationPendingTimestamp(time.Now()),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"server": []byte("https://merged.example.com")},
	}

	// The registration PR has merged: the cluster IS in git now.
	body := envelopedManagedClusters("merged-cluster")
	k8sClient := fake.NewSimpleClientset(pending)
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, body)
	r.pollOnce(ctx)

	got, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "merged-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Secret must still exist after the clear pass: %v", err)
	}
	if _, has := models.RegistrationPendingValue(got.Annotations); has {
		t.Errorf("registration-pending annotation (either key) must be cleared once the cluster is in git; annotations=%v", got.Annotations)
	}
}

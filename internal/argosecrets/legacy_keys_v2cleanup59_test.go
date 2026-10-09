package argosecrets

// V2-cleanup-59 regression tests: the API-group / annotation rename from
// sharko.io (a domain the project never owned) to the maintainer-owned
// sharko.dev is READ-BOTH / EMIT-NEW. A live cluster upgraded across the
// rename still carries the OLD keys on its ArgoCD Secrets; these tests pin
// that such Secrets keep being recognised (adopted protection, check-label
// strips) and that Sharko's own writers migrate labels to the new key.
//
// V2-cleanup-60.5 (L10) widened this to READ-ALL-THREE: the V2-cleanup-59
// canonical spelling itself ("sharko.sharko.dev/adopted") carried a
// historical doubled "sharko." prefix, corrected here to "sharko.dev/adopted"
// while adoption was still at zero adopters. Every read-both case below now
// has a doubled-prefix-key sibling.

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
)

func TestIsAdopted_RecognisesAllThreeKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ann  map[string]string
		want bool
	}{
		{"nil annotations", nil, false},
		{"empty annotations", map[string]string{}, false},
		{"canonical key true", map[string]string{AnnotationAdopted: "true"}, true},
		{"doubled-prefix legacy key true", map[string]string{AnnotationAdoptedDoubledPrefixLegacy: "true"}, true},
		{"pre-rename legacy key true", map[string]string{AnnotationAdoptedLegacy: "true"}, true},
		{"all three keys true", map[string]string{
			AnnotationAdopted:                    "true",
			AnnotationAdoptedDoubledPrefixLegacy: "true",
			AnnotationAdoptedLegacy:              "true",
		}, true},
		{"canonical key non-true", map[string]string{AnnotationAdopted: "false"}, false},
		{"doubled-prefix legacy key non-true", map[string]string{AnnotationAdoptedDoubledPrefixLegacy: "no"}, false},
		{"pre-rename legacy key non-true", map[string]string{AnnotationAdoptedLegacy: "yes"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsAdopted(tc.ann); got != tc.want {
				t.Errorf("IsAdopted(%v) = %v, want %v", tc.ann, got, tc.want)
			}
		})
	}
}

// TestEnsure_LegacyAdoptedAnnotation_TakesLabelsOnlyPath is THE dangerous
// regression: a Secret adopted before the rename carries ONLY the legacy
// adopted key. Ensure must still route it to the labels-only adopted path —
// NEVER the full-rewrite path that would clobber the user's connection data.
func TestEnsure_LegacyAdoptedAnnotation_TakesLabelsOnlyPath(t *testing.T) {
	t.Parallel()
	foreignData := map[string][]byte{
		"config": []byte(`{"bearerToken":"user-token","tlsClientConfig":{"insecure":false}}`),
		"server": []byte("https://legacy-adopted.example.com"),
		"name":   []byte("legacy-adopted"),
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "legacy-adopted",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
			},
			Annotations: map[string]string{
				// ONLY the pre-rename key.
				AnnotationAdoptedLegacy: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: foreignData,
	}

	spec := ClusterSecretSpec{
		Name:   "legacy-adopted",
		Server: "https://somewhere-else.example.com", // would rewrite config on the non-adopted path
		Labels: map[string]string{"addon-datadog": "enabled"},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "legacy-adopted", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	// Connection data must be preserved verbatim — labels-only path.
	for k, want := range foreignData {
		if gotV := string(got.Data[k]); gotV != string(want) {
			t.Errorf("Data[%q] = %q, want %q (legacy-adopted Secret's connection data must never be rewritten)", k, gotV, string(want))
		}
	}
	// Desired addon label converged.
	if got.Labels["addon-datadog"] != "enabled" {
		t.Errorf("addon label not converged; labels=%v", got.Labels)
	}
}

// TestEnsure_DoubledPrefixLegacyAdoptedAnnotation_TakesLabelsOnlyPath is the
// doubled-prefix-key sibling of TestEnsure_LegacyAdoptedAnnotation_TakesLabelsOnlyPath
// (V2-cleanup-60.5 L10): a Secret adopted while "sharko.sharko.dev/adopted"
// was still canonical carries ONLY that key. Ensure must still route it to
// the labels-only adopted path.
func TestEnsure_DoubledPrefixLegacyAdoptedAnnotation_TakesLabelsOnlyPath(t *testing.T) {
	t.Parallel()
	foreignData := map[string][]byte{
		"config": []byte(`{"bearerToken":"user-token","tlsClientConfig":{"insecure":false}}`),
		"server": []byte("https://doubled-prefix-adopted.example.com"),
		"name":   []byte("doubled-prefix-adopted"),
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "doubled-prefix-adopted",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
			},
			Annotations: map[string]string{
				// ONLY the short-lived doubled-prefix key.
				AnnotationAdoptedDoubledPrefixLegacy: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: foreignData,
	}

	spec := ClusterSecretSpec{
		Name:   "doubled-prefix-adopted",
		Server: "https://somewhere-else.example.com", // would rewrite config on the non-adopted path
		Labels: map[string]string{"addon-datadog": "enabled"},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if _, err := mgr.Ensure(context.Background(), spec); err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}

	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "doubled-prefix-adopted", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	// Connection data must be preserved verbatim — labels-only path.
	for k, want := range foreignData {
		if gotV := string(got.Data[k]); gotV != string(want) {
			t.Errorf("Data[%q] = %q, want %q (doubled-prefix-adopted Secret's connection data must never be rewritten)", k, gotV, string(want))
		}
	}
	// Desired addon label converged.
	if got.Labels["addon-datadog"] != "enabled" {
		t.Errorf("addon label not converged; labels=%v", got.Labels)
	}
}

// TestEnsure_SharkoCreated_LegacyCheckLabelMigrates pins the automatic label
// migration for Sharko-CREATED Secrets: the full-label-replace update path
// swaps the legacy connectivity-check key for the new one on the first
// reconcile pass after the upgrade (write-new + remove-old).
func TestEnsure_SharkoCreated_LegacyCheckLabelMigrates(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zero-addon-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType:                     "cluster",
				LabelManagedBy:                      ManagedByValue,
				models.LabelConnectivityCheckLegacy: models.LabelEnabled, // pre-rename stamp
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte("zero-addon-cluster"),
			"server": []byte("https://zero.example.com"),
			"config": []byte("{}"),
		},
	}

	// Desired labels as the post-rename reconciler computes them for a
	// zero-addon cluster: W4b (V3 RW1.8) stamps BOTH keys transitionally.
	desiredLabels := map[string]string{}
	models.ApplyConnectivityCheckLabel(desiredLabels, true)

	spec := ClusterSecretSpec{
		Name:   "zero-addon-cluster",
		Server: "https://zero.example.com",
		Labels: desiredLabels,
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, err := mgr.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	if !changed {
		t.Fatal("Ensure() should report a change when migrating the check label key")
	}

	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "zero-addon-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if got.Labels[models.LabelConnectivityCheck] != models.LabelEnabled {
		t.Errorf("canonical check label missing after migration; labels=%v", got.Labels)
	}
	// W4b (V3 RW1.8): legacy key is now ALSO stamped transitionally so ANY
	// ApplicationSet selector (old or new) matches.
	if got.Labels[models.LabelConnectivityCheckLegacy] != models.LabelEnabled {
		t.Errorf("legacy check label must ALSO be stamped (W4b transitional); labels=%v", got.Labels)
	}
}

// TestEnsure_AdoptedSteadyState_LegacyCheckLabelStripped: an adopted Secret
// that somehow carries the LEGACY check label (stamped before the rename,
// before the adopted gate existed, or by a racing old-version reconciler)
// must have it stripped on the next converge — under either key spelling.
func TestEnsure_AdoptedSteadyState_LegacyCheckLabelStripped(t *testing.T) {
	t.Parallel()
	foreignData := map[string][]byte{
		"config": []byte(`{"bearerToken":"tok"}`),
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "adopted-legacy-check",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType:                     "cluster",
				LabelManagedBy:                      ManagedByValue,
				models.LabelConnectivityCheckLegacy: models.LabelEnabled,
			},
			Annotations: map[string]string{
				AnnotationAdopted: "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: foreignData,
	}

	spec := ClusterSecretSpec{
		Name:   "adopted-legacy-check",
		Server: "https://adopted-legacy-check.example.com",
		Labels: map[string]string{},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, err := mgr.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure() returned error: %v", err)
	}
	if !changed {
		t.Fatal("Ensure() must write when a lingering check label needs stripping")
	}

	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "adopted-legacy-check", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if models.HasConnectivityCheckLabel(got.Labels) {
		t.Errorf("adopted Secret must carry NO check label under either key; labels=%v", got.Labels)
	}
	// Connection data untouched.
	if string(got.Data["config"]) != `{"bearerToken":"tok"}` {
		t.Errorf("adopted Secret Data mutated: %q", string(got.Data["config"]))
	}
}

// TestSyncLabelsOnly_LegacyCheckLabelStripped: self-managed (guest) Secrets
// must have a lingering pre-rename check label stripped too.
func TestSyncLabelsOnly_LegacyCheckLabelStripped(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType:                     "cluster",
				models.LabelConnectivityCheckLegacy: models.LabelEnabled,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"config": []byte(`{"bearerToken":"users-own"}`)},
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	changed, found, err := mgr.SyncLabelsOnly(context.Background(), "user-cluster", map[string]string{"addon-x": "enabled"})
	if err != nil {
		t.Fatalf("SyncLabelsOnly() returned error: %v", err)
	}
	if !found || !changed {
		t.Fatalf("SyncLabelsOnly() = (changed=%v, found=%v), want a write on a found Secret", changed, found)
	}

	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "user-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if models.HasConnectivityCheckLabel(got.Labels) {
		t.Errorf("self-managed Secret must carry NO check label under either key; labels=%v", got.Labels)
	}
	if got.Labels["addon-x"] != "enabled" {
		t.Errorf("addon label not merged; labels=%v", got.Labels)
	}
	// The user's connection data must be untouched.
	if string(got.Data["config"]) != `{"bearerToken":"users-own"}` {
		t.Errorf("user Secret Data mutated: %q", string(got.Data["config"]))
	}
}

// TestUnadopt_RemovesAllLegacyAnnotationsToo: Unadopt on a Secret carrying
// EVERY annotation spelling (canonical + both legacy) must remove ALL
// THREE, or the Secret would remain orphan-sweep-immune forever under
// whichever key survived (V2-cleanup-59 read-both, widened to
// read-all-three by V2-cleanup-60.5 L10).
func TestUnadopt_RemovesAllLegacyAnnotationsToo(t *testing.T) {
	t.Parallel()
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "legacy-unadopt",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
			},
			Annotations: map[string]string{
				AnnotationAdopted:                    "true",
				AnnotationAdoptedDoubledPrefixLegacy: "true",
				AnnotationAdoptedLegacy:              "true",
			},
		},
		Type: corev1.SecretTypeOpaque,
	}

	client := fake.NewSimpleClientset(existing)
	mgr := NewManager(client, testNamespace)

	if err := mgr.Unadopt(context.Background(), "legacy-unadopt"); err != nil {
		t.Fatalf("Unadopt() returned error: %v", err)
	}

	got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "legacy-unadopt", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if IsAdopted(got.Annotations) {
		t.Errorf("Secret still reads as adopted after Unadopt; annotations=%v", got.Annotations)
	}
	for _, key := range []string{AnnotationAdopted, AnnotationAdoptedDoubledPrefixLegacy, AnnotationAdoptedLegacy} {
		if _, has := got.Annotations[key]; has {
			t.Errorf("Unadopt must remove annotation %q; annotations=%v", key, got.Annotations)
		}
	}
	if _, has := got.Labels[LabelManagedBy]; has {
		t.Errorf("managed-by label must be removed by Unadopt; labels=%v", got.Labels)
	}
}

// TestReconcileOnce is covered elsewhere; here we pin the argosecrets
// reconciler's orphan sweep specifically for a legacy-only adopted Secret:
// it must be skipped, not deleted — this is the annotation that protects a
// pre-rename adopted cluster from orphan deletion.
func TestEnsure_TakeoverNormalisesLegacyAdoptedKey(t *testing.T) {
	t.Parallel()
	// A foreign Secret that (unusually) already carries EITHER older adopted
	// key but not the managed-by label — the takeover path stamps the new
	// canonical key and drops BOTH older spellings while it is writing
	// anyway (V2-cleanup-59, widened to all three by V2-cleanup-60.5 L10).
	cases := []struct {
		name       string
		legacyKey  string
		secretName string
	}{
		{"pre-rename legacy key", AnnotationAdoptedLegacy, "takeover-legacy"},
		{"doubled-prefix legacy key", AnnotationAdoptedDoubledPrefixLegacy, "takeover-doubled-prefix-legacy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			existing := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tc.secretName,
					Namespace: testNamespace,
					Labels: map[string]string{
						LabelSecretType: "cluster",
					},
					Annotations: map[string]string{
						tc.legacyKey: "true",
					},
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{"config": []byte(`{"bearerToken":"x"}`)},
			}

			client := fake.NewSimpleClientset(existing)
			mgr := NewManager(client, testNamespace)

			if _, err := mgr.Ensure(context.Background(), ClusterSecretSpec{
				Name:   tc.secretName,
				Server: "https://" + tc.secretName + ".example.com",
			}); err != nil {
				t.Fatalf("Ensure() returned error: %v", err)
			}

			got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), tc.secretName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("secret not found: %v", err)
			}
			if got.Annotations[AnnotationAdopted] != "true" {
				t.Errorf("takeover must stamp the new adopted key; annotations=%v", got.Annotations)
			}
			for _, key := range []string{AnnotationAdoptedDoubledPrefixLegacy, AnnotationAdoptedLegacy} {
				if _, still := got.Annotations[key]; still {
					t.Errorf("takeover write should normalise away older adopted key %q; annotations=%v", key, got.Annotations)
				}
			}
			// Connection data preserved (takeover never touches Data).
			if string(got.Data["config"]) != `{"bearerToken":"x"}` {
				t.Errorf("takeover mutated Data: %q", string(got.Data["config"]))
			}
		})
	}
}

package argosecrets

// builder_test.go — byte-for-byte proof that BuildClusterSecret is the ONE
// canonical builder and that routing the existing writers through it changed
// nothing.
//
// The proof compares MARSHALLED BYTES of whole Secret objects, never a
// field-by-field spot check:
//
//   - the builder vs a replica of manager.go's pre-refactor inline literal
//     (Ensure's create path before it was routed through the builder);
//   - the builder vs a replica of clusterreconciler's pre-refactor
//     buildClusterSecret (the duplicate this refactor removed);
//   - the builder vs what Manager.Ensure actually writes through a fake
//     clientset, on both its create and its full-update path;
//   - the builder against itself twice (purity: same input, same bytes).
//
// The config-JSON shapes themselves stay pinned by cert_shape_test.go's
// golden tests, which this refactor must not touch.

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// builderSpecs covers every connection shape the builder supports: the three
// config shapes (cert pair > token > exec), --role-arn present and absent,
// CA present and absent (the bearer shape flips insecure on a missing CA),
// plus addon labels and Sharko annotations.
func builderSpecs() map[string]ClusterSecretSpec {
	return map[string]ClusterSecretSpec{
		"eks exec with role and CA": {
			Name:    "eks-cluster",
			Server:  "https://ABC123.gr7.us-east-1.eks.amazonaws.com",
			Region:  "us-east-1",
			RoleARN: "arn:aws:iam::123456789012:role/argocd-manager",
			CAData:  "ZmFrZS1jYS1jZXJ0",
			Labels:  map[string]string{"datadog": "enabled"},
		},
		"eks exec without role, without CA": {
			Name:   "eks-plain",
			Server: "https://DEF456.gr7.eu-west-1.eks.amazonaws.com",
			Region: "eu-west-1",
		},
		"bearer token with CA": {
			Name:   "token-cluster",
			Server: "https://10.0.0.2:6443",
			Token:  "sha256~fake-bearer-token",
			CAData: "ZmFrZS1jYS1jZXJ0",
			Labels: map[string]string{"addon-monitoring": "enabled", "env": "prod"},
		},
		"bearer token without CA (insecure)": {
			Name:   "token-insecure",
			Server: "https://10.0.0.3:6443",
			Token:  "sha256~another-fake-token",
		},
		"cert pair": {
			Name:     "kind-onprem",
			Server:   "https://10.0.0.1:6443",
			CertData: "ZmFrZS1jZXJ0",
			KeyData:  "ZmFrZS1rZXk=",
			CAData:   "ZmFrZS1jYS1jZXJ0",
			Labels:   map[string]string{"addon-monitoring": "enabled"},
		},
		"cert pair with annotations": {
			Name:     "kind-annotated",
			Server:   "https://10.0.0.4:6443",
			CertData: "ZmFrZS1jZXJ0",
			KeyData:  "ZmFrZS1rZXk=",
			Labels:   map[string]string{"grafana": "disabled"},
			Annotations: map[string]string{
				"sharko.dev/source-file": "configuration/managed-clusters.yaml",
			},
		},
	}
}

// marshalSecret is the byte-level view the comparisons run on. Go's
// encoding/json sorts map keys, so the output is deterministic.
func marshalSecret(t *testing.T, s *corev1.Secret) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshalling secret: %v", err)
	}
	return b
}

// legacyEnsureCreateLiteral replicates, exactly, the inline corev1.Secret
// literal Ensure's create path carried before it was routed through
// BuildClusterSecret. It is the pre-refactor writer, pinned in test form.
func legacyEnsureCreateLiteral(t *testing.T, spec ClusterSecretSpec, namespace string) *corev1.Secret {
	t.Helper()
	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		t.Fatalf("buildSecretConfig() error: %v", err)
	}
	desiredLabels := buildLabels(spec)
	desiredStringData := map[string]string{
		"name":   spec.Name,
		"server": spec.Server,
		"config": configJSON,
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   namespace,
			Labels:      desiredLabels,
			Annotations: spec.Annotations,
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: desiredStringData,
	}
}

// legacyReconcilerLiteral replicates, exactly, the buildClusterSecret helper
// internal/clusterreconciler carried before it was removed — the duplicate
// builder that assembled its own literal from the shared public wrappers.
func legacyReconcilerLiteral(t *testing.T, spec ClusterSecretSpec, namespace string) *corev1.Secret {
	t.Helper()
	configJSON, err := BuildSecretConfigJSON(spec)
	if err != nil {
		t.Fatalf("BuildSecretConfigJSON() error: %v", err)
	}
	labels := BuildClusterSecretLabels(spec)
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: spec.Annotations,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   spec.Name,
			"server": spec.Server,
			"config": configJSON,
		},
	}
}

// TestBuildClusterSecret_ByteIdentical_ToLegacyEnsureLiteral proves the
// canonical builder emits the exact bytes manager.go's pre-refactor create
// literal emitted, for every connection shape.
func TestBuildClusterSecret_ByteIdentical_ToLegacyEnsureLiteral(t *testing.T) {
	for name, spec := range builderSpecs() {
		t.Run(name, func(t *testing.T) {
			got, err := BuildClusterSecret(spec, testNamespace)
			if err != nil {
				t.Fatalf("BuildClusterSecret() error: %v", err)
			}
			want := legacyEnsureCreateLiteral(t, spec, testNamespace)
			gotB, wantB := marshalSecret(t, got), marshalSecret(t, want)
			if !bytes.Equal(gotB, wantB) {
				t.Errorf("builder output differs from the pre-refactor Ensure literal.\ngot:\n%s\nwant:\n%s", gotB, wantB)
			}
		})
	}
}

// TestBuildClusterSecret_ByteIdentical_ToLegacyReconcilerLiteral proves the
// canonical builder emits the exact bytes clusterreconciler's removed
// duplicate builder emitted, for every connection shape.
func TestBuildClusterSecret_ByteIdentical_ToLegacyReconcilerLiteral(t *testing.T) {
	for name, spec := range builderSpecs() {
		t.Run(name, func(t *testing.T) {
			got, err := BuildClusterSecret(spec, testNamespace)
			if err != nil {
				t.Fatalf("BuildClusterSecret() error: %v", err)
			}
			want := legacyReconcilerLiteral(t, spec, testNamespace)
			gotB, wantB := marshalSecret(t, got), marshalSecret(t, want)
			if !bytes.Equal(gotB, wantB) {
				t.Errorf("builder output differs from the pre-refactor reconciler literal.\ngot:\n%s\nwant:\n%s", gotB, wantB)
			}
		})
	}
}

// TestEnsure_Create_WritesCanonicalBuilderBytes proves Manager.Ensure's
// create path (post-refactor) stores exactly the canonical builder's object:
// the Secret read back from the fake clientset marshals to the same bytes.
func TestEnsure_Create_WritesCanonicalBuilderBytes(t *testing.T) {
	for name, spec := range builderSpecs() {
		t.Run(name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			mgr := NewManager(client, testNamespace)
			changed, err := mgr.Ensure(context.Background(), spec)
			if err != nil {
				t.Fatalf("Ensure() error: %v", err)
			}
			if !changed {
				t.Fatal("Ensure() should report changed=true on create")
			}

			got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("reading back created secret: %v", err)
			}
			want, err := BuildClusterSecret(spec, testNamespace)
			if err != nil {
				t.Fatalf("BuildClusterSecret() error: %v", err)
			}
			// The fake clientset stores what it was given plus its own
			// object-tracker bookkeeping (resourceVersion). That bookkeeping
			// is not part of the Secret payload — blank it before comparing.
			got = got.DeepCopy()
			got.ResourceVersion = ""
			gotB, wantB := marshalSecret(t, got), marshalSecret(t, want)
			if !bytes.Equal(gotB, wantB) {
				t.Errorf("Ensure create wrote different bytes than the canonical builder.\ngot:\n%s\nwant:\n%s", gotB, wantB)
			}
		})
	}
}

// TestEnsure_Update_WritesCanonicalBuilderBytes proves Manager.Ensure's
// full-update path (Sharko-created Secret, hashes differ) converges the live
// object to exactly the canonical builder's payload: after the update, the
// stored Secret marshals to the same bytes as the builder's output.
func TestEnsure_Update_WritesCanonicalBuilderBytes(t *testing.T) {
	for name, spec := range builderSpecs() {
		t.Run(name, func(t *testing.T) {
			// A stale Sharko-created Secret (managed-by label, NO adopted
			// annotation) with wrong labels and wrong data — forces the
			// full-update branch, never adopt/skip.
			stale := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      spec.Name,
					Namespace: testNamespace,
					Labels: map[string]string{
						LabelSecretType: "cluster",
						LabelManagedBy:  ManagedByValue,
						"stale-label":   "old",
					},
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"name":   []byte(spec.Name),
					"server": []byte("https://old-address.example.com"),
					"config": []byte(`{"old":"config"}`),
				},
			}
			client := fake.NewSimpleClientset(stale)
			mgr := NewManager(client, testNamespace)
			changed, err := mgr.Ensure(context.Background(), spec)
			if err != nil {
				t.Fatalf("Ensure() error: %v", err)
			}
			if !changed {
				t.Fatal("Ensure() should report changed=true on the full-update path")
			}

			got, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), spec.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("reading back updated secret: %v", err)
			}
			want, err := BuildClusterSecret(spec, testNamespace)
			if err != nil {
				t.Fatalf("BuildClusterSecret() error: %v", err)
			}
			got = got.DeepCopy()
			got.ResourceVersion = ""
			gotB, wantB := marshalSecret(t, got), marshalSecret(t, want)
			if !bytes.Equal(gotB, wantB) {
				t.Errorf("Ensure full-update wrote different bytes than the canonical builder.\ngot:\n%s\nwant:\n%s", gotB, wantB)
			}
		})
	}
}

// TestBuildClusterSecret_Pure proves the builder is deterministic (same
// input, same bytes) and does not mutate its input spec.
func TestBuildClusterSecret_Pure(t *testing.T) {
	for name, spec := range builderSpecs() {
		t.Run(name, func(t *testing.T) {
			specBefore, err := json.Marshal(spec)
			if err != nil {
				t.Fatalf("marshalling spec: %v", err)
			}

			first, err := BuildClusterSecret(spec, testNamespace)
			if err != nil {
				t.Fatalf("BuildClusterSecret() error: %v", err)
			}
			// Mutating the first result must not leak into a second build —
			// the builder shares no state between calls.
			first.Labels["mutated-after-build"] = "x"
			first.StringData["config"] = "clobbered"

			second, err := BuildClusterSecret(spec, testNamespace)
			if err != nil {
				t.Fatalf("BuildClusterSecret() second call error: %v", err)
			}
			want := legacyEnsureCreateLiteral(t, spec, testNamespace)
			secondB, wantB := marshalSecret(t, second), marshalSecret(t, want)
			if !bytes.Equal(secondB, wantB) {
				t.Errorf("second build differs after mutating the first result — builder is not pure.\ngot:\n%s\nwant:\n%s", secondB, wantB)
			}

			specAfter, err := json.Marshal(spec)
			if err != nil {
				t.Fatalf("marshalling spec after build: %v", err)
			}
			if !bytes.Equal(specBefore, specAfter) {
				t.Errorf("BuildClusterSecret mutated its input spec.\nbefore:\n%s\nafter:\n%s", specBefore, specAfter)
			}
		})
	}
}

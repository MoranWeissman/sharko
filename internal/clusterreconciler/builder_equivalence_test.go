package clusterreconciler

// builder_equivalence_test.go — byte-for-byte proof that the Secret this
// reconciler's createOne writes IS the canonical builder's object
// (argosecrets.BuildClusterSecret) plus exactly the two per-write additions
// createOne makes on top: the defensive ownership-label re-apply (a no-op —
// the builder already set it) and the provenance annotations. Compared as
// MARSHALLED BYTES of the whole object, not field by field.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

func marshalWholeSecret(t *testing.T, s *corev1.Secret) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshalling secret: %v", err)
	}
	return b
}

// TestPollOnce_CreatedSecret_IsCanonicalBuilderBytes runs a real reconcile
// tick against fakes for the three connection shapes and asserts the stored
// Secret marshals to the same bytes as the canonical builder's output (plus
// createOne's provenance annotations, computed here from the same
// deterministic inputs: the pass's compared revision/path and a pinned
// clock).
func TestPollOnce_CreatedSecret_IsCanonicalBuilderBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		creds *providers.Kubeconfig
	}{
		{
			name: "bearer token",
			creds: &providers.Kubeconfig{
				Server: "https://prod-eu.example.com",
				CAData: []byte("fake-ca-bytes"),
				Token:  "fake-token",
			},
		},
		{
			name: "cert pair",
			creds: &providers.Kubeconfig{
				Server:   "https://10.0.0.1:6443",
				CAData:   []byte("fake-ca-bytes"),
				CertData: []byte("fake-cert-bytes"),
				KeyData:  []byte("fake-key-bytes"),
			},
		},
		{
			name: "eks exec (no token, no cert)",
			creds: &providers.Kubeconfig{
				Server: "https://ABC123.gr7.us-east-1.eks.amazonaws.com",
				CAData: []byte("fake-ca-bytes"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			const clusterName = "prod-eu"

			body := envelopedManagedClusters(clusterName)
			vault := &fakeVault{
				creds: map[string]*providers.Kubeconfig{clusterName: tt.creds},
			}
			k8sClient := fake.NewSimpleClientset()
			audits := &auditCollector{}

			r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
			// Pin the clock so the written-at provenance annotation is
			// reproducible below.
			fixedNow := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
			r.nowFn = func() time.Time { return fixedNow }

			r.pollOnce(ctx)

			got, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, clusterName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("expected secret %q after reconcile: %v", clusterName, err)
			}

			// Rebuild the EXACT spec createOne derives for this entry (no
			// git labels, no v4 assignments, no roleArn) and hand it to the
			// canonical builder.
			labels := map[string]string{}
			models.ApplyConnectivityCheckLabel(labels, !r.effectiveDisableConnectivityCheck(ctx))
			spec := argosecrets.ClusterSecretSpec{
				Name:     clusterName,
				Server:   tt.creds.Server,
				Token:    tt.creds.Token,
				CertData: base64.StdEncoding.EncodeToString(tt.creds.CertData),
				KeyData:  base64.StdEncoding.EncodeToString(tt.creds.KeyData),
				CAData:   base64.StdEncoding.EncodeToString(tt.creds.CAData),
				Labels:   labels,
			}
			want, buildErr := argosecrets.BuildClusterSecret(spec, DefaultArgoCDNamespace)
			if buildErr != nil {
				t.Fatalf("BuildClusterSecret() error: %v", buildErr)
			}
			// createOne's two per-write additions on top of the builder:
			ApplyManagedBySharkoLabel(want) // defensive re-apply — a no-op
			revision, path := r.currentPassCompared()
			if want.Annotations == nil {
				want.Annotations = make(map[string]string, 3)
			}
			for k, v := range connectionProvenanceAnnotations(path, revision, fixedNow) {
				want.Annotations[k] = v
			}

			// The fake clientset's resourceVersion bookkeeping is not part
			// of the Secret payload — blank it before comparing bytes.
			got = got.DeepCopy()
			got.ResourceVersion = ""
			gotB, wantB := marshalWholeSecret(t, got), marshalWholeSecret(t, want)
			if !bytes.Equal(gotB, wantB) {
				t.Errorf("reconciler-created Secret differs from the canonical builder's bytes.\ngot:\n%s\nwant:\n%s", gotB, wantB)
			}
		})
	}
}

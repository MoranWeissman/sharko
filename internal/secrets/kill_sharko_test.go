package secrets

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/remoteclient"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// kill_sharko_test.go — task #152 story E, cases 3 and 4: the "kill Sharko
// and nothing breaks" promise applied to Secrets specifically. Turning an
// addon off must not take its Secret with it, and Sharko itself going away
// must not touch a remote cluster at all.

// datadogSecretWithProvenance is what Sharko itself would have created
// while datadog was still enabled on prod-cluster — the managed-by label
// AND the addon provenance annotation, exactly what the periodic pass
// writes (see reconcileSecret's provenance calls).
func datadogSecretWithProvenance() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secret",
			Namespace: "monitoring",
			Labels:    map[string]string{managedByLabelKey: managedByLabelValue},
			Annotations: map[string]string{
				remoteclient.AnnotationAddon: "datadog",
			},
		},
		Data: map[string][]byte{
			"api-key": []byte("the-api-key"),
			"app-key": []byte("the-app-key"),
		},
		Type: corev1.SecretTypeOpaque,
	}
}

// TestReconcile_DisabledAddonKeepsItsSecret is case 3: datadog is still
// defined in the catalog (nothing was deleted from Git) but prod-cluster's
// labels no longer enable it — exactly what DisableAddon's Git changes
// leave behind. The periodic pass, run against this state, must not delete
// the Secret it previously wrote; it may only report it as a leftover.
func TestReconcile_DisabledAddonKeepsItsSecret(t *testing.T) {
	client := fake.NewSimpleClientset(datadogSecretWithProvenance())

	gr := &mockGitReader{files: map[string][]byte{
		"configuration/addons-catalog.yaml":   []byte(catalogWithSecrets),
		"configuration/managed-clusters.yaml": []byte(clusterAddonsNoMatch), // datadog no longer enabled here
	}}
	r := newReconciler(
		gr,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)

	r.reconcile()

	live, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the secret for a disabled addon was removed by the periodic pass: %v", err)
	}
	if string(live.Data["api-key"]) != "the-api-key" || string(live.Data["app-key"]) != "the-app-key" {
		t.Errorf("the secret's content changed even though nothing should have written it: %v", live.Data)
	}

	for _, a := range client.Actions() {
		if a.GetVerb() == "delete" {
			t.Fatalf("the periodic pass deleted something for a disabled addon: %+v", a)
		}
	}

	// The engine noticed it — that's the orphan-report half of the promise
	// (case 5): visible, not silently forgotten, but never auto-deleted.
	orphans := r.OrphanedSecrets()
	if len(orphans) != 1 || orphans[0].Name != "datadog-secret" || orphans[0].Cluster != "prod-cluster" {
		t.Errorf("OrphanedSecrets() = %+v, want the disabled addon's secret reported as a leftover, still there", orphans)
	}
}

// TestReconcile_DisabledAddonKeepsItsSecret_EvenAcrossManyPasses proves the
// promise holds under repetition, not just once: three periodic passes in a
// row over the same disabled-addon state must never turn into a delete.
func TestReconcile_DisabledAddonKeepsItsSecret_EvenAcrossManyPasses(t *testing.T) {
	client := fake.NewSimpleClientset(datadogSecretWithProvenance())

	gr := &mockGitReader{files: map[string][]byte{
		"configuration/addons-catalog.yaml":   []byte(catalogWithSecrets),
		"configuration/managed-clusters.yaml": []byte(clusterAddonsNoMatch),
	}}
	r := newReconciler(
		gr,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)

	for i := 0; i < 3; i++ {
		r.reconcile()
		if _, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); err != nil {
			t.Fatalf("pass %d: the secret disappeared: %v", i+1, err)
		}
	}
}

// TestReconciler_StopMakesNoKubernetesCallsAtAll is case 4: Sharko going
// away — Stop() is what a graceful shutdown calls, and it's also, by
// construction, everything an uninstall leaves behind once the process
// exits. Stop() must not reach out to any remote cluster at all: it is
// nothing more than closing a channel to end the reconcile goroutine's
// select loop (see Start()/Stop() in reconciler.go) — there is no
// shutdown hook here that touches a Secret. The fake clientset, seeded
// with a secret Sharko itself created, must record ZERO actions of any
// kind.
func TestReconciler_StopMakesNoKubernetesCallsAtAll(t *testing.T) {
	client := fake.NewSimpleClientset(datadogSecretWithProvenance())

	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)

	r.Stop()
	r.Stop() // Stop is documented safe to call more than once — uninstall/shutdown can race a second signal.

	if got := client.Actions(); len(got) != 0 {
		t.Fatalf("Stop() touched the remote cluster: %v — uninstalling Sharko must never reach out to a managed cluster", got)
	}
	if _, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); err != nil {
		t.Fatalf("the secret is gone after Stop(): %v", err)
	}
}

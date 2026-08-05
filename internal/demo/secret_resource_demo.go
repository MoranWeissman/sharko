// secret_resource_demo.go — the demo answer to "show me the actual Secret
// on the cluster" (S3). Demo mode has no real clusters and no real vault,
// so the live read that internal/api/secret_resource.go performs is pointed
// at an in-memory fake Kubernetes clientset per cluster instead of a real
// dial (the demo's fake kubeconfigs name addresses nothing answers — the
// real path would sit there until the client's 30-second timeout).
//
// Two rules the fixtures below follow, in this order:
//
//  1. Obviously fake. Every value here says so in the value itself. Nobody
//     should ever look at a demo secret and wonder whether it is real.
//  2. Still blanked. The demo goes through the exact same handler, and
//     therefore the exact same server-side blanking, as production — the
//     values below never reach the browser. That matters: a demo that
//     showed values would teach the maintainer to expect something the
//     real product must never do.
package demo

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// demoSecretValue is what every fake secret value in demo mode is set to.
// It never leaves the server (the handler blanks it), but if it somehow
// did, it would read as exactly what it is.
const demoSecretValue = "demo-value-not-a-real-secret"

// demoSecretAgeOffsets spreads the fake creationTimestamps so the panel's
// age line reads as a real estate rather than "every secret created at the
// same second". Relative to the "now" passed in, never a calendar date.
var demoSecretAgeOffsets = []time.Duration{
	3 * 24 * time.Hour,
	11 * 24 * time.Hour,
	27 * 24 * time.Hour,
	64 * 24 * time.Hour,
	119 * 24 * time.Hour,
}

// seededItems reports every cluster+addon pair this demo reconciler treats
// as a real row, with the outcome currently on file ("" for the one pair
// deliberately left never-checked). Used to build the fake remote clusters
// below so the two agree: a row the page calls "missing" has no Secret on
// its cluster, and clicking it gets the honest "this does not exist"
// answer instead of a fixture that contradicts the row.
func (r *demoAddonValuesReconciler) seededItems() map[demoAddonValueKey]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[demoAddonValueKey]string, len(r.valid))
	for key := range r.valid {
		out[key] = "" // never checked, unless the line below overwrites it
	}
	for key, rec := range r.items {
		out[key] = rec.outcome
	}
	return out
}

// newDemoRemoteClusterClients builds one fake Kubernetes clientset per
// cluster, holding that cluster's addon-values Secrets. Returns the
// resolver internal/api's live-Secret read calls in demo mode.
//
// One clientset PER CLUSTER, not one shared: the addon-values Secret for
// a given addon has the same name and namespace on every cluster, so a
// single shared clientset would make a Secret that is genuinely missing on
// cluster A readable through cluster B's read. Per-cluster keeps the
// "missing" rows honestly missing.
func newDemoRemoteClusterClients(
	recon *demoAddonValuesReconciler,
	defs map[string]orchestrator.AddonSecretDefinition,
	now time.Time,
) func(ctx context.Context, cluster string) (kubernetes.Interface, error) {
	byCluster := make(map[string][]runtime.Object)

	items := recon.seededItems()
	keys := make([]demoAddonValueKey, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Cluster != keys[j].Cluster {
			return keys[i].Cluster < keys[j].Cluster
		}
		return keys[i].Addon < keys[j].Addon
	})

	i := 0
	for _, key := range keys {
		def, ok := defs[key.Addon]
		if !ok || def.SecretName == "" || def.Namespace == "" {
			continue
		}
		if items[key] == "missing" {
			// The page says this secret does not exist on this cluster.
			// Leave it out so a click agrees with the row.
			continue
		}
		byCluster[key.Cluster] = append(byCluster[key.Cluster],
			buildDemoAddonValuesSecret(key.Cluster, key.Addon, def, now, i))
		i++
	}

	clients := make(map[string]kubernetes.Interface, len(byCluster))
	for cluster, objs := range byCluster {
		clients[cluster] = k8sfake.NewSimpleClientset(objs...)
	}

	return func(_ context.Context, cluster string) (kubernetes.Interface, error) {
		if client, ok := clients[cluster]; ok {
			return client, nil
		}
		// A cluster with no addon-values Secret at all still gets an empty
		// fake cluster rather than an error — the read then fails with the
		// honest "this secret does not exist" answer, which is the truth,
		// instead of a made-up connection failure.
		return k8sfake.NewSimpleClientset(), nil
	}
}

// buildDemoAddonValuesSecret renders one fake addon-values Secret as it
// would look on a remote cluster: Sharko's own managed-by label, the key
// names the catalog definition actually declares, and a creation time
// spread across the last few months.
//
// Every fifth secret also carries a kubectl last-applied-configuration
// annotation. That is not decoration: it is the one annotation whose value
// is a full copy of the object it sits on — values included — and it is
// on real, hand-applied Secrets all the time. Having it in the demo means
// the panel's "this annotation was blanked too" path is actually walked
// when the maintainer clicks around, rather than only in a test.
func buildDemoAddonValuesSecret(
	cluster, addon string,
	def orchestrator.AddonSecretDefinition,
	now time.Time,
	index int,
) *corev1.Secret {
	data := make(map[string][]byte, len(def.Keys))
	for key := range def.Keys {
		data[key] = []byte(demoSecretValue)
	}

	createdAt := now.Add(-demoSecretAgeOffsets[index%len(demoSecretAgeOffsets)])
	annotations := map[string]string{
		"sharko.io/pushed-by": "the addon values engine",
		// P2-C5/C4: the same provenance annotations a real push stamps —
		// which addon this secret belongs to, what store it was compared
		// against, and when Sharko last wrote it.
		"sharko.dev/addon":      addon,
		"sharko.dev/source":     "a demo secrets store",
		"sharko.dev/written-at": createdAt.UTC().Format(time.RFC3339),
	}
	if index%5 == 4 {
		annotations["kubectl.kubernetes.io/last-applied-configuration"] =
			fmt.Sprintf(`{"apiVersion":"v1","kind":"Secret","metadata":{"name":%q,"namespace":%q},"data":{"a-key":%q}}`,
				def.SecretName, def.Namespace, demoSecretValue)
	}

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      def.SecretName,
			Namespace: def.Namespace,
			Labels: map[string]string{
				clusterreconciler.LabelManagedBy: clusterreconciler.LabelValueSharko,
				"sharko.io/addon":                addon,
				"sharko.io/cluster":              cluster,
			},
			Annotations:       annotations,
			CreationTimestamp: metav1.NewTime(createdAt),
		},
		Data: data,
		Type: corev1.SecretTypeOpaque,
	}
}

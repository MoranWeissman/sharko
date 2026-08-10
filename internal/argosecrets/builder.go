package argosecrets

// builder.go — the ONE canonical builder for ArgoCD cluster connection
// Secrets.
//
// Before this file existed there were two writers producing the same object
// by two routes: manager.go's inline corev1.Secret literal (Ensure's create
// path) and clusterreconciler's buildClusterSecret. They were deliberately
// kept in step through the shared BuildSecretConfigJSON /
// BuildClusterSecretLabels wrappers, but the Secret literal itself was
// duplicated. BuildClusterSecret collapses that duplication: every writer
// that constructs a FULL connection Secret from a spec goes through here.

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BuildClusterSecret returns the complete, finished ArgoCD cluster
// connection Secret for spec, written into namespace.
//
// Pure by construction: no Kubernetes client, no network, no clock, no
// randomness — the same input yields the same bytes, every time. Anything
// per-write (provenance timestamps, annotations merged from a live object)
// is the caller's to add on top; nothing here reads the world.
//
// It covers every connection shape the code supports today, delegating to
// the two shared builders this package already exposes:
//
//   - stringData.config via buildSecretConfig — precedence cert pair >
//     token > exec (V2-cleanup-56.1); the exec shape carries --role-arn only
//     when spec.RoleARN is set and never an env block (ArgoCD v2.14 cannot
//     unmarshal env in execProviderConfig).
//   - labels via buildLabels — caller-supplied addon labels merged with the
//     system labels (secret-type + managed-by), system labels winning.
//   - name/server stringData keys, Secret name = spec.Name, type always
//     Opaque, annotations = spec.Annotations verbatim.
//
// The metadata-only writers deliberately do NOT use this builder: Ensure's
// adopt and adopted-labels branches, SyncLabelsOnly,
// SyncManagedClusterLabels, TakeOverClusterSecret,
// repairPreservedLabelsRecord, DropLabels, and clusterreconciler's
// clearRegistrationPending all mutate a DeepCopy of the LIVE Secret
// precisely because they must preserve Data/StringData and foreign metadata
// this builder cannot know about. Routing them through a from-scratch
// builder would flatten those deliberate differences.
func BuildClusterSecret(spec ClusterSecretSpec, namespace string) (*corev1.Secret, error) {
	configJSON, err := buildSecretConfig(spec)
	if err != nil {
		return nil, fmt.Errorf("building secret config for cluster %q: %w", spec.Name, err)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   namespace,
			Labels:      buildLabels(spec),
			Annotations: spec.Annotations,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   spec.Name,
			"server": spec.Server,
			"config": configJSON,
		},
	}, nil
}

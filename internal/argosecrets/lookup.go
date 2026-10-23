package argosecrets

// lookup.go — find ArgoCD's connection for a cluster by the name ArgoCD
// shows for it, not by the Secret's Kubernetes name.
//
// ArgoCD names a cluster by the `name` field inside its Secret. The Secret's
// own Kubernetes name is something else entirely: `argocd cluster add` makes
// names like `cluster-10.0.0.5-1234567890`. Sharko used to Get the Secret by
// the cluster name, so every cluster ArgoCD already knew under such a name
// looked like "no connection saved" (episode 3 dry run, finding 5).
//
// Every path that reads, changes or deletes a cluster's connection goes
// through GetClusterSecret. It never renames the user's Secret.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

// ErrAmbiguousClusterSecret is returned when more than one cluster Secret
// carries the same ArgoCD cluster name. Sharko will not guess which one is
// meant; the error message names every Secret it found.
var ErrAmbiguousClusterSecret = errors.New("more than one ArgoCD cluster Secret has this cluster name")

// ClusterNameOf returns the cluster name ArgoCD shows for a cluster Secret:
// the `name` field inside it, or the Secret's Kubernetes name when that
// field is empty.
func ClusterNameOf(s *corev1.Secret) string {
	if s == nil {
		return ""
	}
	if v := strings.TrimSpace(string(s.Data["name"])); v != "" {
		return v
	}
	// A Secret built with StringData and never round-tripped through the
	// API server (fake clients do not convert) still carries its name here.
	if v := strings.TrimSpace(s.StringData["name"]); v != "" {
		return v
	}
	return s.Name
}

// isNotAClusterSecret reports a Secret that ArgoCD's type label marks as
// something other than a cluster.
func isNotAClusterSecret(s *corev1.Secret) bool {
	v, ok := s.Labels[LabelSecretType]
	return ok && v != "cluster"
}

// GetClusterSecret finds the ArgoCD cluster Secret for clusterName in
// namespace.
//
// A Secret matches when the `name` inside it equals clusterName, or when its
// Kubernetes name equals clusterName and it has no different `name` inside.
// A Secret found by its Kubernetes name whose ArgoCD type label says it is
// something else (a repository, repo-creds, ...) never matches. One with no
// type label at all still matches: a cluster Secret whose labels were all
// wiped must still be found, or the reconciler could not see or fix it.
// No match returns a Kubernetes NotFound error, so callers can keep using
// apierrors.IsNotFound. Two or more matches return an error wrapping
// ErrAmbiguousClusterSecret that names each Secret.
func GetClusterSecret(ctx context.Context, client kubernetes.Interface, namespace, clusterName string) (*corev1.Secret, error) {
	secrets := client.CoreV1().Secrets(namespace)

	var matches []*corev1.Secret
	direct, err := secrets.Get(ctx, clusterName, metav1.GetOptions{})
	switch {
	case err == nil:
		// A repository Secret or any other typed ArgoCD Secret that happens
		// to have this Kubernetes name is not a cluster's connection (EP3
		// dry run round R, R13).
		if isNotAClusterSecret(direct) {
			break
		}
		if ClusterNameOf(direct) == clusterName {
			matches = append(matches, direct)
		}
	case apierrors.IsNotFound(err):
		// Not under that Kubernetes name — look for it by ArgoCD's name.
	default:
		return nil, err
	}

	list, listErr := secrets.List(ctx, metav1.ListOptions{LabelSelector: LabelSecretType + "=cluster"})
	if listErr != nil {
		if len(matches) == 1 {
			// The Kubernetes name matched exactly. Not being able to list
			// only means Sharko cannot double-check for a twin, and the
			// direct match is the answer Sharko has always given.
			return matches[0], nil
		}
		return nil, fmt.Errorf("listing ArgoCD cluster Secrets in namespace %q to find cluster %q: %w", namespace, clusterName, listErr)
	}
	for i := range list.Items {
		s := &list.Items[i]
		if direct != nil && err == nil && s.Name == direct.Name {
			continue // already considered above
		}
		if ClusterNameOf(s) == clusterName {
			matches = append(matches, s)
		}
	}

	switch len(matches) {
	case 0:
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, clusterName)
	case 1:
		return matches[0], nil
	}
	names := make([]string, 0, len(matches))
	for _, s := range matches {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return nil, fmt.Errorf(
		"%w: ArgoCD has %d connections called %q, in the Secrets %s in namespace %q. Sharko will not guess which one you mean — rename the cluster in one of them (the name field inside the Secret), then try again",
		ErrAmbiguousClusterSecret, len(matches), clusterName, strings.Join(names, ", "), namespace)
}

// getClusterSecret is the Manager's own way in to GetClusterSecret.
func (m *Manager) getClusterSecret(ctx context.Context, clusterName string) (*corev1.Secret, error) {
	return GetClusterSecret(ctx, m.client, m.namespace, clusterName)
}

// GetClusterSecret finds the ArgoCD cluster Secret for clusterName in the
// Manager's namespace. See the package-level GetClusterSecret.
func (m *Manager) GetClusterSecret(ctx context.Context, clusterName string) (*corev1.Secret, error) {
	return m.getClusterSecret(ctx, clusterName)
}

// ClusterSecretNames lists every ArgoCD cluster Secret in the Manager's
// namespace once and returns, for each cluster name ArgoCD shows, the
// Kubernetes name of its Secret. A cluster name that two Secrets share is
// left out, because Sharko will not guess which one is meant.
func (m *Manager) ClusterSecretNames(ctx context.Context) (map[string]string, error) {
	list, err := m.client.CoreV1().Secrets(m.namespace).List(ctx, metav1.ListOptions{LabelSelector: LabelSecretType + "=cluster"})
	if err != nil {
		return nil, fmt.Errorf("listing ArgoCD cluster Secrets in namespace %q: %w", m.namespace, err)
	}
	out := make(map[string]string, len(list.Items))
	shared := map[string]bool{}
	for i := range list.Items {
		name := ClusterNameOf(&list.Items[i])
		if _, seen := out[name]; seen {
			shared[name] = true
			continue
		}
		out[name] = list.Items[i].Name
	}
	for name := range shared {
		delete(out, name)
	}
	return out, nil
}

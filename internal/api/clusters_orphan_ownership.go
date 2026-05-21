package api

import (
	"context"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
)

// V125-1-8.2 / V125-1-7 tightening — ownership-label gate helpers shared
// between the orphan resolver (clusters_orphans.go) and the orphan-delete
// handler (clusters_orphan_delete.go).
//
// The canonical "Sharko owns this Secret" signal is the
// `app.kubernetes.io/managed-by: sharko` label written by the Story 8.1
// reconciler on every Secret it creates. Story 8.2 closes the V125-1-7
// safety gap where the orphan surfaces double-claimed externally-created
// Secrets that V125-2's Adopt flow will own.
//
// Two helpers live here:
//
//  1. listSharkoOwnedSecretNames — read the set of sharko-labeled Secret
//     names from the argocd namespace via the k8s client wired into the
//     Server at startup. The orphan list resolver uses this to filter its
//     output: only Secrets carrying the sharko label may surface as
//     orphans (unlabeled = V125-2 Adopt territory).
//
//  2. getSecretIfPresent — fetch a named Secret so the orphan-delete
//     handler can re-check the ownership label one final time before
//     mutating live state. Returns (nil, nil) for NotFound so callers can
//     branch cleanly on "Secret is gone" without unwrapping apierrors.
//
// Both helpers are k8s-client-availability-aware: when the server has no
// wired k8s client (test fixtures without argoReconcilerConfig, or a dev
// mode running without an in-cluster K8s connection), they degrade in the
// safest direction. The list helper returns (nil, nil) with a warn log so
// the resolver returns an empty orphan list; the get helper returns
// (nil, errNoK8sClient) so the delete handler fails closed rather than
// silently bypassing the label gate.

// errNoK8sClient is returned by getSecretIfPresent when the Server has no
// k8s client wired. The orphan-delete handler maps this to a 503
// (service-unavailable) response with a clear remediation hint, rather
// than proceeding with the delete sans label check.
var errNoK8sClient = fmt.Errorf("no k8s client wired (argoReconcilerConfig is nil); cannot verify ownership label")

// k8sClientAndNamespace pulls the K8s client + argocd namespace out of the
// Server's argoReconcilerConfig. Returns (nil, "", false) when the config
// is not wired — callers must branch on the returned bool.
//
// The reason for the indirection: argoReconcilerConfig is the existing
// startup-time wiring (see SetArgoReconcilerConfig + cmd/sharko/serve.go)
// that holds both the k8s clientset and the argocd namespace. Story 8.2
// deliberately reuses that wiring rather than introducing a new Server
// field — fewer plumbing changes, and the two fields naturally co-vary.
func (s *Server) k8sClientAndNamespace() (kubernetes.Interface, string, bool) {
	if s.argoReconcilerConfig == nil || s.argoReconcilerConfig.K8sClient == nil {
		return nil, "", false
	}
	ns := s.argoReconcilerConfig.ArgocdNamespace
	if ns == "" {
		ns = "argocd"
	}
	return s.argoReconcilerConfig.K8sClient, ns, true
}

// listSharkoOwnedSecretNames returns the set of cluster names whose
// backing Secret in the argocd namespace carries the
// app.kubernetes.io/managed-by=sharko label.
//
// Returns nil (NOT empty map) when the k8s client is not wired so the
// caller can distinguish "no k8s available" from "k8s available, zero
// labeled Secrets". The orphan-list resolver treats both nil and empty
// the same way (no surfaceable orphans), but the distinction matters for
// the log line so operators see WHY the surface is empty.
//
// A list-API error degrades to nil + warn log (V125-1.5 dignified-
// degrade pattern) — a transient k8s blip MUST NOT 500 the entire
// /clusters page.
func listSharkoOwnedSecretNames(ctx context.Context, k8sClient kubernetes.Interface, namespace string) map[string]struct{} {
	if k8sClient == nil {
		slog.Warn("[orphan] no k8s client wired — cannot enumerate sharko-owned Secrets",
			"namespace", namespace,
		)
		return nil
	}
	selector := clusterreconciler.LabelManagedBy + "=" + clusterreconciler.LabelValueSharko
	list, err := k8sClient.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		slog.Warn("[orphan] listing sharko-owned Secrets failed — degrading to empty",
			"namespace", namespace, "selector", selector, "err", err.Error(),
		)
		return nil
	}
	out := make(map[string]struct{}, len(list.Items))
	for _, s := range list.Items {
		out[s.Name] = struct{}{}
	}
	return out
}

// getSecretIfPresent fetches a single Secret by name from the argocd
// namespace. Returns (nil, nil) when the Secret does not exist (so the
// orphan-delete caller can distinguish "missing Secret" from "k8s error"
// without unwrapping apierrors). Returns (nil, errNoK8sClient) when no
// k8s client is wired.
//
// The Secret is returned by value (pointer to the listed item) so the
// caller can pass it directly to clusterreconciler.IsManagedBySharko.
func (s *Server) getSecretIfPresent(ctx context.Context, name string) (*corev1.Secret, error) {
	k8sClient, namespace, ok := s.k8sClientAndNamespace()
	if !ok {
		return nil, errNoK8sClient
	}
	secret, err := k8sClient.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting secret %q in namespace %q: %w", name, namespace, err)
	}
	return secret, nil
}

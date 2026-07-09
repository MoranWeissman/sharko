package orchestrator

import (
	"context"
	"fmt"
)

// foreignOwnerDetector is the OPTIONAL capability adopt/register use to
// warn when a user-managed connection's ArgoCD cluster Secret is itself
// rendered by ANOTHER ArgoCD Application (V2-cleanup-89.5). Declared as a
// separate single-method interface (asserted at the call site) rather than
// added to ArgoSecretManager so existing implementations and test mocks
// keep compiling unchanged — same pattern as ownershipLabelStripper in
// remove.go. The production adapter in internal/api (argo_adapter.go)
// implements it by delegating to argosecrets.Manager.GetTrackingOwner.
type foreignOwnerDetector interface {
	// GetTrackingOwner inspects the named ArgoCD cluster Secret for ArgoCD
	// tracking markers (the tracking-id annotation or the
	// app.kubernetes.io/instance label) that another Application's sync
	// would stamp if it renders this Secret from Git. Returns
	// (appName, found, error); found=false and no error when the Secret
	// carries no marker OR does not exist yet.
	GetTrackingOwner(ctx context.Context, name string) (string, bool, error)
}

// foreignOwnerWarning is the plain-English warning surfaced when a
// user-managed connection's cluster Secret is discovered to carry a
// foreign ArgoCD tracking marker. Names only the owning application — this
// never fails the calling operation (adopt / register); the caller only
// attaches this string to the result's Warnings.
func foreignOwnerWarning(appName string) string {
	return fmt.Sprintf(
		"this cluster's connection secret is managed by ArgoCD application %q — make sure that app's manifest doesn't define Sharko's addon labels and doesn't use the Replace sync option, or they will fight over this secret. See docs/site/operator/self-managed-connections.md.",
		appName,
	)
}

// detectForeignOwnerWarnings checks whether the named cluster's ArgoCD
// cluster Secret carries a foreign ArgoCD tracking marker (another
// Application renders it from Git) and, if so, returns a one-element slice
// with the plain-English warning text — nil otherwise, including when
// o.argoSecretManager is nil, does not implement the optional capability,
// the Secret does not exist yet, or it carries no marker. A non-nil error
// means the detection attempt itself failed (e.g. a K8s API error); callers
// MUST treat that as advisory-only and never fail the enclosing operation
// on it — only log and proceed.
func (o *Orchestrator) detectForeignOwnerWarnings(ctx context.Context, clusterName string) ([]string, error) {
	if o.argoSecretManager == nil {
		return nil, nil
	}
	detector, ok := o.argoSecretManager.(foreignOwnerDetector)
	if !ok {
		return nil, nil
	}
	appName, found, err := detector.GetTrackingOwner(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return []string{foreignOwnerWarning(appName)}, nil
}

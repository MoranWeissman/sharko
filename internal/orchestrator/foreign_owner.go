package orchestrator

import (
	"context"
	"fmt"
)

// selfManagedConnectionsDocURL is the public, clickable location of the
// self-managed-connections operator guide. Both foreign-owner warning texts
// below (and the doctor's matching check-5 fix text in internal/api) point
// here instead of the repo-relative path "docs/site/operator/
// self-managed-connections.md" — that path is not a URL a user reading a
// warning in the UI or CLI can click (V2-cleanup-90.1, review finding part
// of M6/L4). Matches the base every other in-app readthedocs link uses
// (see e.g. ui/src/components/ClusterIdentityPanel.tsx).
const selfManagedConnectionsDocURL = "https://sharko.readthedocs.io/en/latest/operator/self-managed-connections/"

// foreignOwnerConfidence mirrors argosecrets.OwnerConfidence's two string
// values locally so this package never imports internal/argosecrets (see
// the import-cycle note on ArgoSecretManager in orchestrator.go). The
// production adapter (internal/api/argo_adapter.go) converts the real
// argosecrets.OwnerConfidence to one of these two literal strings.
type foreignOwnerConfidence string

const (
	foreignOwnerConfidenceHard foreignOwnerConfidence = "hard"
	foreignOwnerConfidenceSoft foreignOwnerConfidence = "soft"
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
	// (appName, confidence, found, error); confidence is "hard" when the
	// tracking-id annotation's own namespace/name suffix matches this
	// Secret, "soft" for a mismatched annotation or a label-only match
	// (V2-cleanup-90.1 — a label-only match is also what a plain Helm
	// release stamps, with no ArgoCD involved). found=false and no error
	// when the Secret carries no marker OR does not exist yet.
	GetTrackingOwner(ctx context.Context, name string) (string, string, bool, error)
}

// foreignOwnerWarningHard is surfaced when the tracking-id annotation's own
// "<namespace>/<name>" suffix matches the Secret being inspected — ArgoCD
// really does render this exact Secret from Git under the named
// Application. Names the owning application with confidence; this never
// fails the calling operation (adopt / register), the caller only attaches
// this string to the result's Warnings.
func foreignOwnerWarningHard(appName string) string {
	return fmt.Sprintf(
		"this cluster's connection secret is managed by ArgoCD application %q — make sure that app's manifest doesn't define Sharko's addon labels and doesn't use the Replace sync option, or they will fight over this secret. See %s.",
		appName, selfManagedConnectionsDocURL,
	)
}

// foreignOwnerWarningSoft is surfaced for a weaker signal: either the
// tracking-id annotation's own-resource suffix does not match this Secret
// (very likely copied or cloned from elsewhere, so the name found may not
// really render THIS Secret), or only the app.kubernetes.io/instance label
// is present — the same label a plain Helm release stamps on everything it
// installs, with no ArgoCD involvement at all. Says "may be" rather than
// asserting ownership, and names the Helm possibility so a Helm-only user
// is not scared by an ArgoCD-flavored warning (V2-cleanup-90.1, review
// finding H1).
func foreignOwnerWarningSoft(appName string) string {
	return fmt.Sprintf(
		"this cluster's connection secret may be managed by ArgoCD application or Helm release %q — if an ArgoCD application renders this secret from Git, make sure its manifest doesn't define Sharko's addon labels and doesn't use the Replace sync option. See %s.",
		appName, selfManagedConnectionsDocURL,
	)
}

// detectForeignOwnerWarnings checks whether the named cluster's ArgoCD
// cluster Secret carries a tracking marker that may belong to another
// application (V2-cleanup-90.1: hard or soft confidence — see
// foreignOwnerDetector) and, if so, returns a one-element slice with the
// matching plain-English warning text — nil otherwise, including when
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
	appName, confidence, found, err := detector.GetTrackingOwner(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	if foreignOwnerConfidence(confidence) == foreignOwnerConfidenceHard {
		return []string{foreignOwnerWarningHard(appName)}, nil
	}
	return []string{foreignOwnerWarningSoft(appName)}, nil
}

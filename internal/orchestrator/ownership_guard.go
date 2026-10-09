package orchestrator

// ownership_guard.go — the server-side refusal that stops a plain cluster
// registration from silently stealing another tool's ArgoCD connection
// Secret (secret-ownership hardening, task #150 lane A).
//
// The hole this closes: RegisterCluster's inline-credentials branch writes
// the ArgoCD cluster Secret directly via argoSecretManager.Ensure, and
// Ensure's adoption path used to stamp Sharko's ownership label onto ANY
// existing same-name Secret — including one Terraform, ESO, Helm, another
// controller or another ArgoCD application put there. Registering a new
// cluster whose name matched an existing foreign connection silently stole
// it. Now the registration checks who owns the same-name Secret BEFORE
// writing, and refuses the whole operation when the answer is "someone
// else" — the takeover door (with its preflight and explicit approval) is
// the one and only way to change a connection's owner.
//
// Stance on doubt: a failed ownership read REFUSES too, mirroring
// remove.go's ownership gate ("Any doubt … means refuse"). Guessing "it is
// probably fine" is exactly the failure mode this guard exists to remove.

import (
	"context"
	"errors"
	"fmt"
)

// ConnectionOwnedByAnotherToolError is returned by RegisterCluster when the
// cluster's ArgoCD connection Secret already exists and belongs to another
// tool — either its ownership marker names a rival, or an ArgoCD
// application demonstrably renders it from Git. The API layer maps this to
// a 409 so the caller knows the operation conflicts with existing state
// rather than having failed transiently.
type ConnectionOwnedByAnotherToolError struct {
	// Cluster is the cluster name being registered.
	Cluster string
	// Owner is who holds the connection today: the rival tool's
	// ownership-marker value, or an ArgoCD application name.
	Owner string
	// OwnerIsArgoApp is true when Owner names an ArgoCD application that
	// renders the Secret from Git (a hard tracking match), false when it is
	// a rival ownership-marker value.
	OwnerIsArgoApp bool
}

func (e *ConnectionOwnedByAnotherToolError) Error() string {
	if e.OwnerIsArgoApp {
		return fmt.Sprintf(
			"cluster %q already has an ArgoCD connection, and the ArgoCD application %q deploys it from Git. If Sharko wrote over it, that application would put its own version back on its next sync. Remove the connection from what %q deploys (or stop it syncing), then use the takeover flow to make Sharko the owner",
			e.Cluster, e.Owner, e.Owner)
	}
	return fmt.Sprintf(
		"cluster %q already has an ArgoCD connection, and it is owned by %q. Sharko will not take ownership of another tool's connection during registration. Stop %q from managing this connection and remove its ownership marker from the connection — the marker does not come off by itself when the tool is stopped — then use the takeover flow to make Sharko the owner",
		e.Cluster, e.Owner, e.Owner)
}

// IsConnectionOwnedByAnotherTool reports whether err is (or wraps) a
// *ConnectionOwnedByAnotherToolError. The API layer uses this to choose a
// 409 instead of the generic 502.
func IsConnectionOwnedByAnotherTool(err error) bool {
	var target *ConnectionOwnedByAnotherToolError
	return errors.As(err, &target)
}

// refuseWhenSecretOwnedByAnotherTool is the pre-write ownership check the
// inline-credentials registration path runs before its direct Ensure write.
// One GetClusterSecretDetail call answers everything at once (found /
// ownership marker / ArgoCD tracking owner), so the normal fresh-register
// path — no existing Secret — costs exactly one extra Get.
//
// Returns nil when writing is safe: no Secret exists yet, Sharko already
// owns it, or the only foreign signal is a soft one (a mismatched tracking
// marker or a bare Helm-style instance label — not proof of ownership, and
// the advisory foreign-owner warning path already surfaces it). Returns
// *ConnectionOwnedByAnotherToolError for a rival ownership marker or a hard
// tracking match, and a plain error when the ownership read itself failed —
// doubt refuses, same stance as remove.go's ownership gate.
func (o *Orchestrator) refuseWhenSecretOwnedByAnotherTool(ctx context.Context, name string) error {
	detail, err := o.argoSecretManager.GetClusterSecretDetail(ctx, name)
	if err != nil {
		return fmt.Errorf(
			"cluster %q may already have an ArgoCD connection, but Sharko could not read who owns it: %w. Sharko will not write over a connection whose owner it cannot confirm — fix the read problem and try again",
			name, err)
	}
	if !detail.Found {
		return nil
	}
	if detail.ManagedBy != "" && detail.ManagedBy != sharkoManagedByValue {
		return &ConnectionOwnedByAnotherToolError{Cluster: name, Owner: detail.ManagedBy}
	}
	if detail.ForeignOwnerFound && foreignOwnerConfidence(detail.ForeignOwnerConfidence) == foreignOwnerConfidenceHard {
		return &ConnectionOwnedByAnotherToolError{Cluster: name, Owner: detail.ForeignOwnerAppName, OwnerIsArgoApp: true}
	}
	return nil
}

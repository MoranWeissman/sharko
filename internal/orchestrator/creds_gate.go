// Package orchestrator — addon-enable credentials gate (V2-cleanup-88.3,
// "lazy credentials").
//
// Sharko's ONE ongoing need for its own spoke-cluster credentials is
// pushing addon secrets: catalog entries with a non-empty `secrets:` block
// (see internal/models/addon.go AddonSecretRef; pushed by
// internal/secrets/reconciler.go + createAddonSecrets in secrets.go). Addon
// WORKLOADS deploy via Git -> ArgoCD and need no Sharko credentials at all.
// Registration (RegisterCluster / AdoptClusters) therefore succeeds with
// zero credentials for every cluster and every connection mode — see the
// Step 3 comment in cluster.go and the Phase 1 comment in adopt.go.
//
// The enforcement moment is HERE: EnableAddon, for a secret-bearing addon,
// refuses to open a Git PR when Sharko cannot currently resolve credentials
// for the target cluster. A secret-less addon enables on a cred-less
// cluster with zero friction — this gate is a no-op for it.
package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/models"
)

// MissingClusterCredentialsError is returned by EnableAddon when the target
// addon declares secrets but Sharko has no resolvable credentials for the
// target cluster. It is a CALLER-actionable error (add credentials to the
// cluster, or pick an addon with no secrets) — the API layer maps it to a
// 4xx, never a 500/502, mirroring the sibling *AddonNotInCatalogError
// contract.
type MissingClusterCredentialsError struct {
	Addon       string
	Cluster     string
	SecretCount int
}

// Error names exactly what is missing and how to fix it, e.g.:
//
//	addon "datadog" needs 2 secrets pushed to the cluster, but Sharko has no
//	credentials for cluster "prod-1" — add connection credentials (secret
//	path or EKS role) to the cluster, or choose an addon without secrets
func (e *MissingClusterCredentialsError) Error() string {
	if e == nil {
		return "addon needs secrets pushed to the cluster, but Sharko has no credentials for it"
	}
	unit := "secret"
	if e.SecretCount != 1 {
		unit = "secrets"
	}
	return fmt.Sprintf(
		"addon %q needs %d %s pushed to the cluster, but Sharko has no credentials for cluster %q — add connection credentials (secret path or EKS role) to the cluster, or choose an addon without secrets",
		e.Addon, e.SecretCount, unit, e.Cluster,
	)
}

// IsMissingClusterCredentials reports whether err is (or wraps) a
// *MissingClusterCredentialsError. The API layer uses this to choose a 4xx
// status.
func IsMissingClusterCredentials(err error) bool {
	var target *MissingClusterCredentialsError
	return errors.As(err, &target)
}

// addonSecretRequirement reports whether the named addon's catalog entry
// declares any Secrets, and how many distinct Kubernetes Secrets it needs
// pushed to a target cluster. catalog is the already-parsed entry list —
// EnableAddon already read it for the referential-integrity check and
// values generation, so this never triggers a second Git read.
func addonSecretRequirement(catalog []models.AddonCatalogEntry, addonName string) (needsSecrets bool, secretCount int) {
	for _, entry := range catalog {
		if entry.Name == addonName {
			return len(entry.Secrets) > 0, len(entry.Secrets)
		}
	}
	return false, 0
}

// requireClusterCredentialsForAddon is EnableAddon's pre-flight gate
// (V2-cleanup-88.3). Call BEFORE any Git write. Returns nil immediately for
// a secret-less addon — zero friction, the whole point of lazy credentials.
// For a secret-bearing addon it performs a REAL credential fetch attempt
// (the same credsRouter-aware path createAddonSecrets itself uses via
// fetchClusterCredentials), so a nil return here means the subsequent
// secret push can actually proceed — not just that the cluster record
// LOOKS configured.
func (o *Orchestrator) requireClusterCredentialsForAddon(ctx context.Context, catalog []models.AddonCatalogEntry, clusterName, addonName string) error {
	needsSecrets, secretCount := addonSecretRequirement(catalog, addonName)
	if !needsSecrets {
		return nil
	}
	if o.clusterHasResolvableCredentials(ctx, clusterName) {
		return nil
	}
	return &MissingClusterCredentialsError{
		Addon:       addonName,
		Cluster:     clusterName,
		SecretCount: secretCount,
	}
}

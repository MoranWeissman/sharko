package api

import (
	"net/url"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
)

// Target-cluster platform values (V2-cleanup-88.1). See
// models.Cluster.TargetPlatform.
const (
	targetPlatformEKS     = "eks"
	targetPlatformUnknown = "unknown"
)

// eksServerURLSuffix is the hostname suffix ArgoCD-registered EKS API
// server URLs carry, e.g. "https://ABCDEF.gr7.us-east-1.eks.amazonaws.com".
// Same pattern the UI's ClusterTypeBadge classifies on
// (ui/src/components/ClusterTypeBadge.tsx) — kept in lockstep so the
// backend-derived field and the frontend's own cosmetic badge never
// disagree about what "looks like EKS" means.
const eksServerURLSuffix = ".eks.amazonaws.com"

// computeTargetPlatform derives whether a registered cluster looks like
// EKS. It is a pure, cheap, string-only computation with NO network calls
// — safe to run on every cluster read (list + detail + comparison),
// mirroring the derived_health_status precedent (see
// connectivity_status.go's computeDerivedHealth).
//
// First match wins:
//
//  1. serverURL's hostname ends in ".eks.amazonaws.com" — the
//     ArgoCD-registered API server URL for an EKS cluster always has this
//     shape.
//  2. credsSource == models.CredsSourceEKSToken (V2-cleanup-60.4) — the
//     cluster's credentials are minted via the EKS STS token path, which
//     only ever applies to EKS clusters. This is also the "structured-EKS
//     secret shape" signal from the design: a structured AWS Secrets
//     Manager secret only sets credsSource to eks-token when it carried an
//     EKS-specific shape (role_arn / cluster_name) at registration time
//     (see internal/providers/aws_sm.go's format auto-detection and
//     models.ManagedClusterEntry.CredsSource).
//  3. otherwise -> "unknown". A cluster Sharko has no signal for reads as
//     "unknown", never as a false-positive "eks".
func computeTargetPlatform(serverURL, credsSource string) string {
	if hasEKSServerURL(serverURL) {
		return targetPlatformEKS
	}
	if credsSource == models.CredsSourceEKSToken {
		return targetPlatformEKS
	}
	return targetPlatformUnknown
}

// hasEKSServerURL reports whether serverURL's hostname ends in
// eksServerURLSuffix. Parses via net/url (falling back to the raw string
// on a malformed URL) so a coincidental substring elsewhere in the URL
// (query string, path) never produces a false positive.
func hasEKSServerURL(serverURL string) bool {
	if serverURL == "" {
		return false
	}
	host := serverURL
	if u, err := url.Parse(serverURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	return strings.HasSuffix(strings.ToLower(host), eksServerURLSuffix)
}

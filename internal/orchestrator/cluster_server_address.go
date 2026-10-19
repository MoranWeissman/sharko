package orchestrator

// cluster_server_address.go — LO-9 R11 (epic video-leftovers, 2026-10-18).
//
// Registering a cluster used to take the cluster address from the
// credentials as it was, even with a user name, a password, a query or a
// fragment in it. That address then goes into the ArgoCD connection, into
// the answer (cluster.server) and into log lines, and a password or token
// can hide in exactly those parts. So registration now refuses it.
//
// The decision is credsafe's, the same one the log sink and
// internal/argocd/endpoint.go use (credsafe.ClassifyAddress, and
// credsafe.SafeRepoURL for the part that may be shown). Nothing here has a
// rule of its own about addresses.

import (
	"errors"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

const (
	// clusterServerCarriesCredentialFmt: the address has a user part, a
	// query or a fragment. Argument: the address with those parts taken off
	// (credsafe.SafeRepoURL), so the user sees what to use instead.
	clusterServerCarriesCredentialFmt = "The cluster address in these credentials has a user name, password, query or fragment in it. Sharko does not register a cluster with an address like that, because a password or token can hide in those parts. Use the address without them: %s"

	// clusterServerUnreadable: Sharko could not read the address well
	// enough to show any of it.
	clusterServerUnreadable = "Sharko could not read the cluster address in these credentials, so it cannot check that there is no user name, password, query or fragment in it. Fix the address (for example https://my-cluster.example.com:6443) and try again."
)

// ClusterServerAddressError is the refusal of a cluster address that has, or
// may have, a credential in it. The API answers it with 400. Its text never
// holds the user part, query or fragment.
type ClusterServerAddressError struct {
	msg string
}

func (e *ClusterServerAddressError) Error() string {
	if e == nil || e.msg == "" {
		return clusterServerUnreadable
	}
	return e.msg
}

// IsClusterServerAddressRefused reports whether err is (or wraps) a
// *ClusterServerAddressError.
func IsClusterServerAddressRefused(err error) bool {
	var e *ClusterServerAddressError
	return errors.As(err, &e)
}

// checkClusterServerAddress refuses a cluster address that credsafe does not
// call credential-free. An empty address is left to the code that already
// handles it.
func checkClusterServerAddress(server string) error {
	if server == "" {
		return nil
	}
	switch credsafe.ClassifyAddress(server) {
	case credsafe.AddressCredentialFree:
		return nil
	case credsafe.AddressCarriesCredential:
		if safe := credsafe.SafeRepoURL(server); safe != "" {
			return &ClusterServerAddressError{msg: fmt.Sprintf(clusterServerCarriesCredentialFmt, safe)}
		}
	}
	return &ClusterServerAddressError{msg: clusterServerUnreadable}
}

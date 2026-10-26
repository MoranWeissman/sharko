package api

import (
	"time"

	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/notifications"
)

// credentialCheckBell is the part of the notification store the credential
// check uses. It is a field on each connectionCredentialCheckStore, so a test
// gives its own store its own bell.
type credentialCheckBell interface {
	Add(notifications.Notification)
	ResolveFor(notifications.Code, notifications.Params)
}

// ringBell keeps the bell in step with one cluster's latest check (ep4,
// serious 5).
//
//   - The check could not read the cluster's login from the secrets store:
//     one bell entry for this cluster. The store keeps one entry per id, so
//     calling this on every failing check adds nothing new, and the entry
//     survives a restart without a second one.
//   - The check finished: that cluster's entry, if there is one, is cleared.
//     This runs on every finished check, not just on the change, so an
//     entry left from before a restart is cleared too.
//   - Any other way of not finishing (Git could not be read, and so on)
//     leaves the bell as it is: it says nothing about the secrets store.
//
// The entry carries only the cluster's name, which the notification
// boundary checks again. No error text reaches it.
func (st *connectionCredentialCheckStore) ringBell(cluster string, status string, failure connectioncompare.CheckFailure, at time.Time) {
	if st.bell == nil {
		return
	}
	params := notifications.Params{Cluster: cluster}
	switch {
	case status == credentialCheckFailed && failure == connectioncompare.CheckFailureBackendRead:
		st.bell.Add(notifications.New(notifications.CodeClusterCredentialCheckFailed, notifications.ReasonCredentials, params, at))
	case status != credentialCheckFailed:
		st.bell.ResolveFor(notifications.CodeClusterCredentialCheckFailed, params)
	}
}

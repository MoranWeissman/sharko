package orchestrator

// register_messages.go — the "message" of a cluster registration that
// stopped part-way (status "partial").
//
// Each message says what Sharko did and what it did not do, built from what
// really happened on this run: the steps that ran, how many addon secrets
// were created or failed, and whether the pull request is merged. Before
// (LO-9 R8), the messages were fixed strings that guessed: "ArgoCD
// registration not started" when the ArgoCD connection was already saved,
// and "Secrets created" when no secret was made.
//
// Every sentence here is pinned by exact text in register_messages_test.go.

import (
	"fmt"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
)

// stepWriteArgoSecret is the completed step that says the cluster's
// connection was written straight into ArgoCD (pasted kubeconfig path).
const stepWriteArgoSecret = "write_argocd_secret"

const (
	// registerMsgConnectFailed: the addon secrets step could not connect to
	// the cluster at all. createAddonSecrets only fails this way before it
	// creates anything, so "no addon secrets were created" is always true.
	registerMsgConnectFailed = "Sharko could not connect to the cluster to create its addon secrets, so it stopped before opening a pull request. No addon secrets were created and nothing was committed to Git."

	// registerMsgRegistryReadFailed: the cluster registry could not be read
	// from Git (not a "file not found").
	registerMsgRegistryReadFailed = "Sharko could not read the cluster registry from Git, so it stopped before opening a pull request — writing one built from an empty file would have dropped every other cluster out of the registry. Nothing was committed."

	// registerMsgCommitFailed: Sharko could not open the pull request.
	registerMsgCommitFailed = "Sharko could not open the pull request that registers this cluster."

	// registerMsgMergeFailed: the pull request is open but auto-merge failed.
	registerMsgMergeFailed = "Sharko opened the pull request that registers this cluster, but could not merge it. Merge it by hand to finish the registration."

	// registerMsgSecretsFailedMergedFmt / registerMsgSecretsFailedOpenFmt:
	// everything ran, but some addon secrets could not be created.
	// Argument: an addon secret count phrase ("1 addon secret").
	registerMsgSecretsFailedMergedFmt = "The cluster is registered, but %s could not be created on it."
	registerMsgSecretsFailedOpenFmt   = "Sharko opened the pull request that registers this cluster, but %s could not be created on it. The registration finishes when the pull request is merged."

	// registerMsgSecretsCreatedFmt / registerMsgSecretsNotCreatedFmt: what
	// the addon secrets step did before the registration stopped.
	registerMsgSecretsCreatedFmt    = "Sharko created %s on the cluster."
	registerMsgSecretsNotCreatedFmt = "%s could not be created on the cluster."

	// registerMsgArgoSavedFmt: the connection was written straight into
	// ArgoCD with the registration-pending mark. The cluster reconciler
	// removes it once the grace window ends if the cluster is still not in
	// the cluster registry in Git. Argument: the window in minutes.
	registerMsgArgoSavedFmt = "Sharko had already saved the cluster's connection in ArgoCD. If the cluster is not in the cluster registry in Git within about %d minutes, Sharko removes that connection again."

	registerMsgTryAgainGit     = "Try again once Git is reachable."
	registerMsgTryAgainCluster = "Check that Sharko can reach the cluster, then try again."
	registerMsgPRLinkFmt       = "Pull request: %s"
)

// addonSecretCount is "1 addon secret" or "N addon secrets".
func addonSecretCount(n int) string {
	if n == 1 {
		return "1 addon secret"
	}
	return fmt.Sprintf("%d addon secrets", n)
}

// registerOutcome is what a registration had done when it stopped.
type registerOutcome struct {
	steps          []string
	secretsCreated int
	secretsFailed  int
	// prMerged is true only when the registration pull request is merged.
	prMerged bool
}

func (r registerOutcome) argoSecretWritten() bool {
	for _, s := range r.steps {
		if s == stepWriteArgoSecret {
			return true
		}
	}
	return false
}

// doneSoFar returns the sentences that say what was already changed.
// withFailed adds the "could not be created" sentence; leave it off when the
// first sentence of the message already says it.
func (r registerOutcome) doneSoFar(withFailed bool) []string {
	var out []string
	if r.secretsCreated > 0 {
		out = append(out, fmt.Sprintf(registerMsgSecretsCreatedFmt, addonSecretCount(r.secretsCreated)))
	}
	if withFailed && r.secretsFailed > 0 {
		out = append(out, fmt.Sprintf(registerMsgSecretsNotCreatedFmt, addonSecretCount(r.secretsFailed)))
	}
	if r.argoSecretWritten() && !r.prMerged {
		out = append(out, fmt.Sprintf(registerMsgArgoSavedFmt, int(models.RegistrationPendingGraceWindow.Minutes())))
	}
	return out
}

func joinSentences(parts ...[]string) string {
	var all []string
	for _, p := range parts {
		all = append(all, p...)
	}
	return strings.Join(all, " ")
}

// registerConnectFailedMessage: the addon secrets step could not reach the
// cluster. No secret was created at this point.
func registerConnectFailedMessage(r registerOutcome) string {
	return joinSentences([]string{registerMsgConnectFailed}, r.doneSoFar(false), []string{registerMsgTryAgainCluster})
}

// registerRegistryReadFailedMessage: the cluster registry could not be read.
func registerRegistryReadFailedMessage(r registerOutcome) string {
	return joinSentences([]string{registerMsgRegistryReadFailed}, r.doneSoFar(true), []string{registerMsgTryAgainGit})
}

// registerCommitFailedMessage: the pull request could not be opened.
func registerCommitFailedMessage(r registerOutcome) string {
	return joinSentences([]string{registerMsgCommitFailed}, r.doneSoFar(true), []string{registerMsgTryAgainGit})
}

// registerMergeFailedMessage: the pull request is open, the merge failed.
// The pull request address stays at the end of the message.
func registerMergeFailedMessage(r registerOutcome, prURL string) string {
	r.prMerged = false
	return joinSentences([]string{registerMsgMergeFailed}, r.doneSoFar(true), []string{fmt.Sprintf(registerMsgPRLinkFmt, prURL)})
}

// registerSecretsFailedMessage: everything ran, some addon secrets failed.
func registerSecretsFailedMessage(r registerOutcome) string {
	lead := fmt.Sprintf(registerMsgSecretsFailedOpenFmt, addonSecretCount(r.secretsFailed))
	if r.prMerged {
		lead = fmt.Sprintf(registerMsgSecretsFailedMergedFmt, addonSecretCount(r.secretsFailed))
	}
	return joinSentences([]string{lead}, r.doneSoFar(false))
}

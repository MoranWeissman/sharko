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

	// registerMsgArgoSavedNoPRFmt / registerMsgArgoSavedPROpen: the
	// connection was written straight into ArgoCD with the
	// registration-pending mark. The cluster reconciler keeps it for the
	// grace window, then for as long as an open pull request registers the
	// cluster, and removes it once none does (LO-10 R13). NoPR is for a
	// registration that stopped before any pull request was opened;
	// argument: the window in minutes. PROpen is for one whose pull request
	// is open and not merged.
	registerMsgArgoSavedNoPRFmt = "Sharko had already saved the cluster's connection in ArgoCD. If no open pull request registers this cluster after about %d minutes, Sharko removes that connection again."
	registerMsgArgoSavedPROpen  = "Sharko had already saved the cluster's connection in ArgoCD. Sharko keeps it while this pull request is open, and removes it if the pull request is closed without being merged."

	// registerMsgArgoWriteFailed: the pasted-kubeconfig connection could not
	// be written into ArgoCD. The registration stops right there (LO-10
	// R14): the pasted credentials live in no secrets backend, so a merged
	// pull request alone would leave a cluster ArgoCD cannot reach.
	registerMsgArgoWriteFailed = "Sharko could not save the cluster's connection in ArgoCD, so it stopped before creating addon secrets or opening a pull request. Nothing was changed on the cluster and nothing was committed to Git. Check that Sharko can write Secrets in the ArgoCD namespace, then try again."

	registerMsgTryAgainGit     = "Try again once Git is reachable."
	registerMsgTryAgainCluster = "Check that Sharko can reach the cluster, then try again."
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
	// prOpen is true when the registration pull request is open and not
	// merged.
	prOpen bool
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
		if r.prOpen {
			out = append(out, registerMsgArgoSavedPROpen)
		} else {
			out = append(out, fmt.Sprintf(registerMsgArgoSavedNoPRFmt, int(models.RegistrationPendingGraceWindow.Minutes())))
		}
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

// registerArgoWriteFailedMessage: the connection could not be written into
// ArgoCD; nothing else was done.
func registerArgoWriteFailedMessage() string {
	return registerMsgArgoWriteFailed
}

// registerMergeFailedMessage: the pull request is open, the merge failed.
// The pull request address is not in the message: the answer carries it in
// git.pr_url, and the UI and CLI show it from there (LO-10 R16). Before, it
// was in both places, so the dialog showed it twice.
func registerMergeFailedMessage(r registerOutcome) string {
	r.prMerged = false
	r.prOpen = true
	return joinSentences([]string{registerMsgMergeFailed}, r.doneSoFar(true))
}

// registerSecretsFailedMessage: everything ran, some addon secrets failed.
func registerSecretsFailedMessage(r registerOutcome) string {
	lead := fmt.Sprintf(registerMsgSecretsFailedOpenFmt, addonSecretCount(r.secretsFailed))
	r.prOpen = !r.prMerged
	if r.prMerged {
		lead = fmt.Sprintf(registerMsgSecretsFailedMergedFmt, addonSecretCount(r.secretsFailed))
	}
	return joinSentences([]string{lead}, r.doneSoFar(false))
}

package credsafe

// argotext.go — the third kind of credential material this package owns: text
// that ArgoCD wrote, which Sharko copies onto a response a person reads (B7,
// B8).
//
// # Why this is worse than the error paths credsafe already covered
//
// credsafe.go is about a credentials backend's error text. connmessage.go is
// about what Sharko says when it cannot build a client. Both are error paths:
// something has to go wrong first.
//
// This file is about the ordinary, successful, 200-response path. GET
// /api/v1/clusters/{name}/comparison copies ArgoCD's own strings into its JSON
// body every time it is called, whether or not anything is broken. Nothing has
// to fail for the data to travel, which makes it a wider hole than either of
// the error paths, not a milder one.
//
// # The three shapes, and why each one is handled the way it is
//
// 1. A repository URL (spec.source.repoURL). Handled by SafeRepoURL in
//    repourl.go — the whole userinfo section comes off. That one is easy
//    because a repo URL has a grammar: net/url can say which part is the
//    credential.
//
// 2. A closed set of ArgoCD status words (status.sync.status,
//    status.health.status, operationState.phase, a cluster's connectionState).
//    Each of these is an enum in ArgoCD's own Go types. Sharko echoes a value
//    only when it is one Sharko knows; anything else is reported as
//    "unrecognised" rather than repeated. This is classification by TYPE — the
//    value either IS one of the known members or it is not — never a search
//    through the text for something that looks like a secret.
//
// 3. Free-form prose (operationState.message, a cluster's connectionMessage,
//    an application condition's message). This one has no grammar at all. It
//    is whatever ArgoCD, Helm, the Kubernetes API server or a Git transport
//    said, quoted verbatim, and it routinely embeds the repository ArgoCD was
//    syncing from — token and all — anywhere inside a paragraph.
//
// # Why shape 3 is replaced rather than redacted
//
// The obvious idea is to scan the message for URLs and strip their userinfo.
// That idea is banned here and it should be. A redaction that works by
// searching text for things that look like secrets fails on the first format
// nobody predicted: a token split across a line break, a URL written with an
// escaped @, a Helm error quoting a values file that happens to hold the
// credential, a base64 blob in a Kubernetes apply error. Every one of those is
// a rule somebody has to think of in advance, and the ones nobody thought of
// are exactly the ones that ship.
//
// So Sharko does not pass the prose through at all. The field carries a fixed
// sentence Sharko wrote, plus the facts from shapes 1 and 2, which ARE safe
// because each has a grammar or a known set. That is the same trade B4 and B1
// made for error text, applied to a success path.
//
// The operator does not lose the ability to fix their cluster: the sentence
// says which application, which phase, which repository, and where the full
// text lives — in ArgoCD, where they have to go to fix it anyway.

import (
	"fmt"
	"strings"
)

// Unrecognised is what Sharko says instead of echoing an ArgoCD status word it
// does not know. It is deliberately not empty: "ArgoCD said something Sharko
// does not recognise" and "ArgoCD said nothing" are two different facts and an
// operator needs to be able to tell them apart.
const Unrecognised = "unrecognised"

// knownSyncStatuses is ArgoCD's SyncStatusCode set.
var knownSyncStatuses = map[string]bool{
	"Synced":    true,
	"OutOfSync": true,
	"Unknown":   true,
}

// knownHealthStatuses is ArgoCD's HealthStatusCode set, plus one word Sharko
// writes itself.
//
// "Error" is not one of ArgoCD's own health codes. Sharko's ArgoCD client
// assigns it (internal/argocd/client.go) whenever the application carries a
// condition whose type ends in "Error" — a ComparisonError, a SyncError — and
// ArgoCD's own health said "Healthy" or said nothing. So it arrives on this
// field from Sharko's own hand, out of a fixed set of two possibilities, and
// leaving it out of this list was a real mistake: an application with a sync
// error is the commonest failure there is, and every one of them came back to
// the cluster-comparison screen as "unrecognised" (B7/B8 shipped that; B10
// fixes it). The browser also reads this word — ui/src/hooks/useAddonStates.tsx
// maps "error" to the red degraded badge — so dropping it would have turned a
// real failure grey.
var knownHealthStatuses = map[string]bool{
	"Healthy":     true,
	"Progressing": true,
	"Degraded":    true,
	"Suspended":   true,
	"Missing":     true,
	"Unknown":     true,
	"Error":       true,
}

// knownConditionTypes is ArgoCD's ApplicationConditionType set. A condition's
// TYPE is a closed enum in ArgoCD's Go types; its message is not, which is why
// only the type is ever echoed.
var knownConditionTypes = map[string]bool{
	"DeletionError":           true,
	"InvalidSpecError":        true,
	"ComparisonError":         true,
	"SyncError":               true,
	"UnknownError":            true,
	"SharedResourceWarning":   true,
	"RepeatedResourceWarning": true,
	"ExcludedResourceWarning": true,
	"OrphanedResourceWarning": true,
	"OrphanedResourceIgnored": true,
}

// knownAppSetConditionTypes is ArgoCD's ApplicationSetConditionType set.
var knownAppSetConditionTypes = map[string]bool{
	"ErrorOccurred":       true,
	"ParametersGenerated": true,
	"ResourcesUpToDate":   true,
	"RolloutProgressing":  true,
}

// knownConditionStatuses is ArgoCD's ApplicationSetConditionStatus set, which
// is the ordinary Kubernetes True/False/Unknown triple.
var knownConditionStatuses = map[string]bool{
	"True":    true,
	"False":   true,
	"Unknown": true,
}

// knownOperationPhases is ArgoCD's OperationPhase set (which is Argo Workflows'
// NodePhase reused).
var knownOperationPhases = map[string]bool{
	"Running":     true,
	"Terminating": true,
	"Failed":      true,
	"Error":       true,
	"Succeeded":   true,
}

// knownConnectionStates is ArgoCD's ConnectionStatus set for a cluster.
var knownConnectionStates = map[string]bool{
	"Successful": true,
	"Failed":     true,
	"Unknown":    true,
}

func echoKnown(known map[string]bool, v string) string {
	if v == "" {
		return ""
	}
	if known[v] {
		return v
	}
	return Unrecognised
}

// SafeSyncStatus returns ArgoCD's sync status when it is one Sharko knows, ""
// when ArgoCD reported nothing, and Unrecognised otherwise.
func SafeSyncStatus(v string) string { return echoKnown(knownSyncStatuses, v) }

// SafeHealthStatus is the same for status.health.status.
func SafeHealthStatus(v string) string { return echoKnown(knownHealthStatuses, v) }

// SafeOperationPhase is the same for operationState.phase.
func SafeOperationPhase(v string) string { return echoKnown(knownOperationPhases, v) }

// SafeConnectionState is the same for an ArgoCD cluster's connectionState.
func SafeConnectionState(v string) string { return echoKnown(knownConnectionStates, v) }

// SafeConditionType is the same for an application condition's type.
func SafeConditionType(v string) string { return echoKnown(knownConditionTypes, v) }

// SafeAppSetConditionType is the same for an ApplicationSet condition's type.
func SafeAppSetConditionType(v string) string { return echoKnown(knownAppSetConditionTypes, v) }

// SafeConditionStatus is the same for a condition's True/False/Unknown status.
func SafeConditionStatus(v string) string { return echoKnown(knownConditionStatuses, v) }

// ArgocdSyncFailureMessage is the fixed sentence that replaces ArgoCD's
// operationState.message wherever Sharko puts that message on a response, in a
// log line or into the AI assistant's context.
//
// It is a constant, not a template, for the same reason the two sentences in
// connmessage.go are: a sentence that varied with the cause would be a channel
// back to the cause. What varies is appended separately by SafeOperationDetail,
// and every piece of that comes from a known set or from SafeRepoURL.
const ArgocdSyncFailureMessage = "ArgoCD could not finish syncing this addon. Sharko does not repeat ArgoCD's own message here: that message quotes whatever ArgoCD was working on, which includes the repository address with its access token inside it. Open this application in ArgoCD to read the full error."

// ArgocdSyncFailureShort is the badge-sized form of the same fact, for the
// issues[] array the UI renders as a list of one-liners. It exists so the
// short/long split the UI already relies on survives the fix: the collapsed
// row stays readable and the expanded row still carries the facts.
const ArgocdSyncFailureShort = "ArgoCD could not finish syncing this addon — open it in ArgoCD for the full error."

// ArgocdClusterConnectionFailureMessage is the fixed sentence that replaces the
// connectionMessage ArgoCD attaches to a cluster it cannot reach. Same reason:
// that message is whatever the Kubernetes client, the cloud provider's IAM
// layer or the transport said, quoted in full.
const ArgocdClusterConnectionFailureMessage = "ArgoCD cannot reach this cluster. Sharko does not repeat ArgoCD's own message here, because it quotes the credentials layer's words in full. Open the cluster in ArgoCD to read the connection error."

// ArgocdCheckFailureMessage is the fixed sentence for Sharko's own
// connectivity-check application when ArgoCD reports it as failing. It is a
// third sentence rather than a reuse of the first because it sends an operator
// somewhere else: to the connectivity-check application, not to the addon.
const ArgocdCheckFailureMessage = "Sharko's connectivity-check application is failing in ArgoCD. Sharko does not repeat ArgoCD's own message here, because that message quotes the repository address and the credentials layer's words in full. Open the connectivity-check application in ArgoCD to read the full error."

// ArgocdAppConditionMessage is the fixed sentence that replaces the message on
// an ArgoCD application condition (B10).
//
// A condition's message is the same kind of thing operationState.message is:
// whatever ArgoCD, Helm, the Kubernetes API server or a Git transport said,
// quoted verbatim. A ComparisonError condition in particular is the one that
// says "repository not accessible: authentication required" followed by the
// repository address it was given — token and all. This one reaches the
// Dashboard's needs-attention feed, which the browser renders as an ordinary
// row on an ordinary 200.
//
// It leads with the action rather than the explanation on purpose: the feed's
// row trims the text at 140 characters and puts the rest in a hover, so the
// half that tells an operator what to do has to come first.
const ArgocdAppConditionMessage = "ArgoCD reported a problem with this application — open it in ArgoCD to read the full condition. Sharko does not repeat ArgoCD's own condition text here, because that text quotes the repository address and the credentials layer's words in full."

// ArgocdAppSetConditionMessage is the fixed sentence that replaces the message
// on an ArgoCD ApplicationSet condition, which lands on the addon detail
// response. An ApplicationSet's generator holds the repository it templates
// from, so its ErrorOccurred condition quotes that repository by name.
const ArgocdAppSetConditionMessage = "ArgoCD reported a problem with this addon's ApplicationSet — open it in ArgoCD to read the full condition. Sharko does not repeat ArgoCD's own condition text here, because that text quotes the repository address the generator was reading."

// ArgocdResourceMessage is the fixed sentence that replaces the message ArgoCD
// attaches to ONE managed Kubernetes resource's health assessment, on the
// observability overview response.
//
// That message is written by ArgoCD's health assessment — a Lua script per
// resource kind for the built-in ones, an operator-supplied one otherwise —
// and what it produces is the Kubernetes object's own status text. Nothing has
// to fail for it to travel: the overview lists every resource of every addon
// on every cluster, every time the page is opened.
//
// The resource itself is still fully named: kind, group, namespace and name
// are separate fields on the same object and none of them is free-form text.
const ArgocdResourceMessage = "ArgoCD reported something about this resource — open the application in ArgoCD to read it. Sharko does not repeat the text here, because a health message quotes the Kubernetes object's own words in full."

// ArgocdWriteRefusedMessage is the fixed sentence Sharko says when ArgoCD
// ANSWERED a write call — a sync, a terminate, a cluster registration, a
// repository add — with a status outside 2xx.
//
// # Why a write needed its own sentence
//
// The rest of this file is about text ArgoCD writes into an object Sharko then
// reads. This one is about the reply to a write call, which used to be copied
// into the returned error whole: "unexpected status %d from %s %s: %s", where
// the last %s was ArgoCD's own response payload. That payload quotes whatever
// ArgoCD was working on when it refused, and for a repository that means the
// address with the access token inside it —
// https://x-access-token:<token>@host/org/repo.git. The read path dropped the
// payload and said why; the write path did not.
//
// The facts an operator still needs are appended by the caller and every one
// of them is Sharko's own: which HTTP method, which endpoint Sharko itself
// built, the status code, and a stable code for the class of refusal. None of
// that comes out of the reply.
const ArgocdWriteRefusedMessage = "ArgoCD refused this change. Sharko does not repeat ArgoCD's own reply here, because that reply quotes whatever ArgoCD was working on — which includes the repository address with its access token inside it. Open ArgoCD to read the full error."

// ArgocdWriteUnreachableMessage is the second fixed sentence: Sharko never got
// an answer at all, so there is no status and no endpoint result to report.
//
// It is separate from ArgocdWriteRefusedMessage because the two send an
// operator to different places. "ArgoCD said no" is something to read in
// ArgoCD; "Sharko got no answer" is something to check in Settings. Within
// each of the two the sentence never varies with the cause, which is the rule
// Message and the connection sentences already hold.
const ArgocdWriteUnreachableMessage = "Sharko could not get an answer from ArgoCD for this change, so it does not know whether anything was applied. Check the ArgoCD server address and token in Settings, then try again."

// ArgocdReadUnreachableMessage is the same thing for a READ that never got an
// answer — a list of clusters, a list of applications, a version probe.
//
// # Why a read needed its own sentence rather than borrowing the write one
//
// ArgocdWriteUnreachableMessage ends with "so it does not know whether
// anything was applied". On a read that is simply untrue: a read applies
// nothing, and telling an operator their fleet might have been changed by a
// failed list is a worse answer than saying nothing. Sharko does not say
// things that are not so, so the read gets its own sentence and both stay
// short.
//
// # Why the address is not in it
//
// This is the sentence that replaces a transport error, and a transport error
// is the one place the ArgoCD server address travels verbatim. net/http wraps
// a failed round trip in *url.Error, whose text is the address it dialled.
// Operators write credentials into that address — as the userinfo section, as
// a query parameter, as a fragment — and net/http masks only the password
// half of the userinfo, leaving the other three shapes intact. So the address
// is left out entirely and the operator is pointed at Settings, where they can
// read their own address on their own screen.
const ArgocdReadUnreachableMessage = "Sharko could not get an answer from ArgoCD, so it has nothing to show for this. Check the ArgoCD server address and token in Settings, then try again."

// OperationFacts are the pieces of a failing ArgoCD operation that Sharko is
// allowed to say out loud.
//
// Every field is taken raw from the ArgoCD object; the caller is NOT trusted to
// have cleaned anything. SafeOperationDetail puts each one through its own
// classifier before it appears in the output, which is what makes this a helper
// that cannot be handed arbitrary text — passing raw ArgoCD strings in is the
// expected use, and it is still safe.
//
// There is deliberately no Message field. Adding one would be the whole leak
// again, wearing a helper's name.
type OperationFacts struct {
	// Phase is operationState.phase.
	Phase string
	// SyncStatus is status.sync.status.
	SyncStatus string
	// HealthStatus is status.health.status.
	HealthStatus string
	// RepoURL is spec.source.repoURL. It goes through SafeRepoURL, and when
	// SafeRepoURL cannot take it apart the repository is not mentioned at all.
	RepoURL string
}

// SafeOperationDetail returns the fixed sentence followed by whichever of the
// facts ArgoCD actually reported, in the key=value shape init-status already
// uses so the two read the same way.
//
// sentence must be one of the constants above. It is a parameter rather than a
// hardcoded constant only so the addon case and the connectivity-check case can
// share the fact-formatting; nothing else may be passed, and
// TestSafeOperationDetail_RefusesAnUnknownSentence holds that line.
func SafeOperationDetail(sentence string, f OperationFacts) string {
	if !isKnownSentence(sentence) {
		// A caller that reached here is trying to route arbitrary text
		// through the safe helper. Say the most conservative thing rather
		// than what they asked for.
		sentence = ArgocdSyncFailureMessage
	}
	var parts []string
	if p := SafeOperationPhase(f.Phase); p != "" {
		parts = append(parts, fmt.Sprintf("phase=%s", p))
	}
	if s := SafeSyncStatus(f.SyncStatus); s != "" {
		parts = append(parts, fmt.Sprintf("sync=%s", s))
	}
	if h := SafeHealthStatus(f.HealthStatus); h != "" {
		parts = append(parts, fmt.Sprintf("health=%s", h))
	}
	if r := SafeRepoURL(f.RepoURL); r != "" {
		parts = append(parts, fmt.Sprintf("repo=%s", r))
	}
	if len(parts) == 0 {
		return sentence
	}
	return sentence + " (" + strings.Join(parts, " ") + ")"
}

// SafeReportedDetail is SafeOperationDetail for a field that has to stay empty
// when the provider said nothing at all.
//
// "ArgoCD wrote a message about this and Sharko will not repeat it" and "ArgoCD
// wrote nothing about this" are two different facts, and a reader needs to be
// able to tell them apart — otherwise every healthy resource on the
// observability page grows a sentence about a problem it does not have.
//
// reported is a bool, deliberately. The caller passes whether there WAS a
// message, never the message: words cannot travel through a parameter that is
// not there. That is the same line SafeOperationDetail holds by having no
// Message field on OperationFacts.
func SafeReportedDetail(reported bool, sentence string, f OperationFacts) string {
	if !reported {
		return ""
	}
	return SafeOperationDetail(sentence, f)
}

// isKnownSentence is the type check that keeps SafeOperationDetail from being
// used as a way to print arbitrary text with a safe-sounding function name.
func isKnownSentence(s string) bool {
	switch s {
	case ArgocdSyncFailureMessage,
		ArgocdCheckFailureMessage,
		ArgocdClusterConnectionFailureMessage,
		ArgocdAppConditionMessage,
		ArgocdAppSetConditionMessage,
		ArgocdResourceMessage:
		return true
	}
	return false
}

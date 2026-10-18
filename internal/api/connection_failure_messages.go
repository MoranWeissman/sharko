package api

// connection_failure_messages.go — the ONE place the connection-test failure
// sentence is assembled, and the finite catalog of every sentence it can
// produce.
//
// # What was wrong
//
// This family never emitted a whole sentence. It picked a fragment by
// connection kind ("Sharko can't reach your Git host") and glued a hint chosen
// at runtime onto the end of it:
//
//	return what + " — " + verify.Hint(code)
//
// A catalog of fixed strings cannot hold half a sentence, so this family sat
// outside the message catalog entirely — and the browser did what a browser
// always does when the server will not give it a whole sentence: it typed the
// three fragments in by hand, as fallbacks, WITH A FULL STOP THE SERVER NEVER
// EMITS. Two formatters, already drifted, and nothing in the tree could see it.
//
// # The product owner's ruling, verbatim
//
//	"Choose parameterized catalog entries. Do not leave the sentence family
//	 outside the catalog. […] The browser should normally receive and render
//	 the completed sentence. It must not reproduce the concatenation logic."
//
// So the catalog below carries all four things the ruling names:
//
//	stable message identifier      — connectionFailureIDFor(kind, code)
//	complete rendering function    — renderConnectionFailure(kind, code)
//	typed parameters               — connectionKind, verify.ErrorCode
//	final server-rendered sentence — ConnectionFailureMessages[i].Sentence
//
// # Finite variants, not a template
//
// Both parameters are CLOSED, so the finished sentences are enumerable and the
// catalog holds them finished. That was the choice to make, and it is the safer
// half of it: a template shipped to the browser is a second formatter wearing a
// data structure's clothes, and a second formatter is the exact thing the
// ruling forbids. Enumerating leaves the browser with nothing to assemble.
//
//   - connectionKind is a closed set of five (four real kinds plus the bucket
//     every unrecognised kind renders as).
//   - verify.ErrorCode is a closed const block of ten. Anything outside it
//     renders as ERR_UNKNOWN rather than as a new variant, so an unsupported
//     value can never invent an identifier the browser has not been given.
//
// TestConnectionFailure_EveryDeclaredErrorCodeHasAToken parses
// internal/verify/errors.go and fails BY NAME on any code declared there that
// is missing from connectionFailureCodeTokens below. It is a LIST, never a
// count — a floor would pass while a whole new variant shipped uncatalogued.
//
// # The prose lives HERE and only here
//
// Every fragment, hint and tail this family can say is declared in this file.
// TestConnectionMessages_HoldNoProse pins that connection_messages.go —
// which used to hold the fragments and the join — now holds no prose at all,
// so a re-typed half-sentence cannot come back next door to where it was.

import (
	"errors"
	"fmt"
	"sort"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// ── the first typed parameter: which connection was tested ──────────────

// connectionKind identifies the connection a failed test was run against. It
// is a named type rather than a bare string so a handler cannot pass a kind
// that was never written down — the compiler resolves the constants below.
type connectionKind string

const (
	connectionKindGit    connectionKind = "git"
	connectionKindArgoCD connectionKind = "argocd"
	connectionKindVault  connectionKind = "vault"

	// connectionKindSecrets is the secrets/cluster-credentials backend under
	// its other name. It was already honoured by the ERR_AUTH hint arm while
	// being absent from the fragment switch, so it used to produce a sentence
	// that opened "Sharko can't reach this connection" and then went on to
	// talk about the secrets store. It is a full kind here, worded like
	// connectionKindVault, because those two ARE the same backend.
	connectionKindSecrets connectionKind = "secrets"

	// connectionKindOther is the bucket every unrecognised kind renders as.
	// No handler passes it; it exists so the fallback wording has an
	// identifier of its own instead of being an unnamed default.
	connectionKindOther connectionKind = "other"
)

// connectionKinds is the closed set, in catalog order.
var connectionKinds = []connectionKind{
	connectionKindGit,
	connectionKindArgoCD,
	connectionKindVault,
	connectionKindSecrets,
	connectionKindOther,
}

// connectionKindTokens maps each kind to the token that appears in its
// identifiers. Adding a kind without adding a token is caught by
// TestConnectionFailure_EveryKindHasAToken, by name.
var connectionKindTokens = map[connectionKind]string{
	connectionKindGit:     "Git",
	connectionKindArgoCD:  "ArgoCD",
	connectionKindVault:   "Vault",
	connectionKindSecrets: "Secrets",
	connectionKindOther:   "Other",
}

// connectionFailureSubjects is the opening half of the sentence — WHAT could
// not be reached. Deliberately without terminal punctuation: the tail the
// renderer adds supplies it, and a fragment that already ends in a full stop
// is how the browser's hand-typed copies came to disagree with the server.
var connectionFailureSubjects = map[connectionKind]string{
	connectionKindGit:     "Sharko can't reach your Git host",
	connectionKindArgoCD:  "Sharko can't reach ArgoCD",
	connectionKindVault:   "Sharko can't reach your secrets store",
	connectionKindSecrets: "Sharko can't reach your secrets store",
	connectionKindOther:   "Sharko can't reach this connection",
}

// normalizeConnectionKind maps anything unrecognised onto connectionKindOther,
// so an unsupported parameter value renders a sentence the browser already
// has rather than inventing an identifier it does not.
func normalizeConnectionKind(kind connectionKind) connectionKind {
	if _, ok := connectionKindTokens[kind]; ok {
		return kind
	}
	return connectionKindOther
}

// ── the second typed parameter: why it failed ───────────────────────────

// connectionFailureCodeTokens maps every verify.ErrorCode to the token that
// appears in its identifiers.
//
// This is the completeness list. Declare a new code in internal/verify and
// this map is the thing that has not been updated —
// TestConnectionFailure_EveryDeclaredErrorCodeHasAToken parses that file and
// names the missing constant.
var connectionFailureCodeTokens = map[verify.ErrorCode]string{
	verify.ERR_NETWORK:    "Network",
	verify.ERR_TLS:        "TLS",
	verify.ERR_AUTH:       "Auth",
	verify.ERR_RBAC:       "RBAC",
	verify.ERR_AWS_STS:    "AWSSTS",
	verify.ERR_AWS_ASSUME: "AWSAssume",
	verify.ERR_QUOTA:      "Quota",
	verify.ERR_NAMESPACE:  "Namespace",
	verify.ERR_TIMEOUT:    "Timeout",
	verify.ERR_UNKNOWN:    "Unknown",
}

// connectionFailureCodes is the closed set, in catalog order.
var connectionFailureCodes = []verify.ErrorCode{
	verify.ERR_NETWORK,
	verify.ERR_TLS,
	verify.ERR_AUTH,
	verify.ERR_RBAC,
	verify.ERR_AWS_STS,
	verify.ERR_AWS_ASSUME,
	verify.ERR_QUOTA,
	verify.ERR_NAMESPACE,
	verify.ERR_TIMEOUT,
	verify.ERR_UNKNOWN,
}

// normalizeConnectionFailureCode maps anything outside the declared set onto
// ERR_UNKNOWN. internal/verify/stage2.go already puts a code on a Result that
// is not in the const block ("ERR_NOT_IMPLEMENTED"), so "a code nobody wrote
// down" is a real value, not a hypothetical.
func normalizeConnectionFailureCode(code verify.ErrorCode) verify.ErrorCode {
	if _, ok := connectionFailureCodeTokens[code]; ok {
		return code
	}
	return verify.ERR_UNKNOWN
}

// connectionFailureCodeNeedsHTTPS is a failure code this catalog adds on top
// of verify's closed set. verify.ClassifyError has no code for "this http
// address only sends Sharko on to https" — that is not a cluster check
// result, it is about the address an operator typed for ArgoCD. So it is not
// added to internal/verify, and it is used for ONE kind only (see
// connectionFailureExtraPairs). Any other kind with this code renders as
// ERR_UNKNOWN.
const connectionFailureCodeNeedsHTTPS verify.ErrorCode = "ERR_NEEDS_HTTPS"

// connectionFailureExtraCodeTokens gives the extra codes their identifier
// tokens. Kept apart from connectionFailureCodeTokens, which must match
// internal/verify exactly.
var connectionFailureExtraCodeTokens = map[verify.ErrorCode]string{
	connectionFailureCodeNeedsHTTPS: "NeedsHTTPS",
}

// connectionFailureExtraPairs is every (kind, extra code) pair the catalog
// holds. An extra code is only ever valid together with its kind.
var connectionFailureExtraPairs = []connectionFailureKey{
	{kind: connectionKindArgoCD, code: connectionFailureCodeNeedsHTTPS},
}

// normalizeConnectionFailurePair normalizes both parameters together. An
// extra code is kept only for the kind it was written for; with any other
// kind it becomes ERR_UNKNOWN, like every other code nobody wrote down.
func normalizeConnectionFailurePair(kind connectionKind, code verify.ErrorCode) (connectionKind, verify.ErrorCode) {
	kind = normalizeConnectionKind(kind)
	if _, extra := connectionFailureExtraCodeTokens[code]; extra {
		for _, pair := range connectionFailureExtraPairs {
			if pair.kind == kind && pair.code == code {
				return kind, code
			}
		}
		return kind, verify.ERR_UNKNOWN
	}
	return kind, normalizeConnectionFailureCode(code)
}

// connectionFailureCodeToken returns the identifier token for a code that is
// already normalized.
func connectionFailureCodeToken(code verify.ErrorCode) string {
	if token, ok := connectionFailureExtraCodeTokens[code]; ok {
		return token
	}
	return connectionFailureCodeTokens[code]
}

// ── the hint half ───────────────────────────────────────────────────────

// The kind-aware ERR_AUTH hints. verify.Hint's ERR_AUTH wording is written for
// cluster-registration credentials ("regenerate the kubeconfig/token") — right
// for a cluster, wrong for a Git host or a secrets store, which is a different
// credential entirely (review findings r1, M9).
const (
	hintAuthGit     = "the Git host rejected the credentials (HTTP 401) — the token may be expired or invalid; regenerate the Git token/PAT in Settings → Connections and try again."
	hintAuthSecrets = "the secrets store rejected the credentials (HTTP 401) — check the secrets-provider credentials and try again."
)

// The hints for the codes verify.Hint has nothing to say about. verify.Hint
// returns "" for these, and a connection test is exactly the situation where
// "the host could not be reached" is worth saying out loud.
const (
	hintNetwork = "the host could not be reached. Check the URL and network access."
	hintTLS     = "a certificate error occurred while connecting. Check the server's TLS configuration."
	hintTimeout = "the request timed out. Check the URL and network access."
)

// connectionFailureJoin separates the subject from the hint.
//
// It is a constant so there is one join in the server and none in the browser.
// TestConnectionFailure_JoinPunctuation writes the separator out as a literal
// rather than referring to this constant — a test that compares a constant
// with itself would stay green through any change to it.
const connectionFailureJoin = " — "

// connectionFailureNoHintTail finishes the sentence when there is no hint. It
// opens with a full stop, not the dash: "…this connection — " with nothing
// after it is the trailing-separator bug this family is being rebuilt to make
// impossible.
const connectionFailureNoHintTail = ". Check the connection settings and try again."

// ── the whole-sentence variants ─────────────────────────────────────────

// connectionFailureKey is a (kind, code) pair, so a variant can be looked up
// by both of its typed parameters at once.
type connectionFailureKey struct {
	kind connectionKind
	code verify.ErrorCode
}

// gitPermissionRefusal is the product owner's text, supplied verbatim on
// 2026-08-20 and required character for character.
//
// WHY IT IS A WHOLE SENTENCE AND NOT A HINT. Every other variant in this
// family reads "<subject> — <hint>", and the Git subject is "Sharko can't
// reach your Git host". For a 403 that opening is false: Sharko DID reach the
// Git server, and the Git server answered. The old wording made it worse by
// borrowing verify.Hint's cluster-flavoured text, so a Git operator was told
// "the cluster accepted the credentials" about a refusal that came from a Git
// server. Bolting the ruled sentence onto the end of a subject that says the
// host was unreachable would have kept half the lie.
//
// So this pair renders as one complete sentence of its own. Do not reword it,
// and do not split it across the join: the full stop is part of the ruled
// text.
const gitPermissionRefusal = "The Git server accepted the credentials but denied the requested operation."

// gitPermissionNextStep is the actionable half. A sentence that only says
// "denied" trades one defect for another — the standing rule is that a safe
// sentence still has to name the category of thing to go and look at. It
// names no credential and no provider error detail, and it promises no
// timeframe.
const gitPermissionNextStep = "Grant the token permission for this repository on your Git host, or replace it in Settings → Connections."

// argocdCertificateAdvice is the one piece of advice Sharko gives when it
// does not trust ArgoCD's certificate. Every ArgoCD certificate sentence uses
// it as it is: the connection test (argocdCertificateNotTrusted below) and the
// ArgoCD list paths (argoListFailureSentence). Before L16 the two gave
// different advice: the connection test said to give ArgoCD a trusted
// certificate, install it with server.insecure, or tick the box, and the list
// paths said to add a certificate authority to the connection, which is a
// field the connection does not have.
//
// Pinned by exact text in connection_failure_messages_test.go and
// argocd_certificate_advice_test.go.
const argocdCertificateAdvice = "Give ArgoCD a trusted certificate, or, for a test setup only, " +
	"tick Skip TLS certificate verification."

// argocdCertificateNotTrusted is the whole sentence for an ArgoCD connection
// test that failed on the certificate (VF-1, video finding V2).
//
// WHY IT IS A WHOLE SENTENCE AND NOT A HINT. The generic TLS variant reads
// "Sharko can't reach ArgoCD — a certificate error occurred while connecting.
// Check the server's TLS configuration." That is wrong on both halves for the
// most common case, a default ArgoCD install: Sharko DID reach ArgoCD, and
// "check the TLS configuration" does not say what to do. A default ArgoCD has
// TLS on with a certificate it signed itself, so the sentence says what to do
// in the user's words (argocdCertificateAdvice). Verification is never turned
// off for them.
//
// Pinned by exact text in connection_failure_messages_test.go. Do not reword
// it without changing that test in the same commit.
const argocdCertificateNotTrusted = "ArgoCD uses a certificate Sharko does not trust. " + argocdCertificateAdvice

// argocdNeedsHTTPS is the whole sentence for an http ArgoCD address that only
// answers by sending Sharko on to https (L7, video finding). Before, this
// case showed the general "Sharko can't reach ArgoCD" sentence, which is
// wrong: Sharko did reach it, and the fix is one change to the address.
//
// This is the form the catalog (and so the browser's generated file) holds.
// The server sends it as it is when it does not know the https address — the
// redirect went to another host, or its address could not be used.
//
// Pinned by exact text in connection_failure_messages_test.go.
const argocdNeedsHTTPS = "ArgoCD answers only over https at this address. " +
	"Change the ArgoCD address so it starts with https:// and test again."

// argocdNeedsHTTPSAtFmt is the same sentence when Sharko knows the https
// address: the same host and port it was sent on to, plus the path of the
// ArgoCD address the operator typed (http://host/argocd gives
// https://host/argocd, see argocd.HTTPSAddress). It is never built from the
// user part or the query, which is where a credential can be written. The
// one argument is that address.
//
// The catalog cannot hold this form, because the address is different for
// every setup. It holds argocdNeedsHTTPS under the same identifier instead,
// and the browser shows whatever sentence the server sent.
const argocdNeedsHTTPSAtFmt = "ArgoCD answers only over https at this address. " +
	"Change the ArgoCD address to %s and test again."

// connectionTestStatusNotTested is the "status" of a connection-test result
// for a test that never ran. Every other result is "ok" or "error".
const connectionTestStatusNotTested = "not_tested"

// argocdNotTestedBadGitAddress is the message for an ArgoCD result that was
// not tested because the Git repository address in the same request could not
// be read (the test stops there). Pinned by exact text in
// connections_test_credentials_not_tested_test.go.
const argocdNotTestedBadGitAddress = "ArgoCD was not tested, because Sharko could not read the Git repository address. Fix the Git address and test again."

// connectionFailureWholeSentences holds the (kind, code) pairs whose finished
// sentence is written out here rather than assembled from a subject and a
// hint.
//
// It is deliberately tiny and deliberately keyed by BOTH parameters. A
// per-KIND override would have changed the ArgoCD, Vault and secrets-store
// sentences too, and a per-CODE override would have changed every kind's 403.
// Only the pair that was ruled on moves.
//
// A pair listed here is still an ordinary catalog entry: same identifier, same
// ConnectionFailureMessages slice, same generated browser file. Nothing about
// the contract changes — only where the words come from.
var connectionFailureWholeSentences = map[connectionFailureKey]string{
	{kind: connectionKindGit, code: verify.ERR_RBAC}:                    gitPermissionRefusal + " " + gitPermissionNextStep,
	{kind: connectionKindArgoCD, code: verify.ERR_TLS}:                  argocdCertificateNotTrusted,
	{kind: connectionKindArgoCD, code: connectionFailureCodeNeedsHTTPS}: argocdNeedsHTTPS,
}

// connectionFailureWholeSentence looks a whole-sentence variant up. Both
// parameters are normalized here so a caller cannot miss the pair by passing
// an unsupported value.
func connectionFailureWholeSentence(kind connectionKind, code verify.ErrorCode) (string, bool) {
	kind, code = normalizeConnectionFailurePair(kind, code)
	sentence, ok := connectionFailureWholeSentences[connectionFailureKey{kind: kind, code: code}]
	return sentence, ok
}

// connectionFailureHint returns the hint half for a (kind, code) pair, or ""
// when there is nothing actionable to add. Both arguments must already be
// normalized.
func connectionFailureHint(kind connectionKind, code verify.ErrorCode) string {
	// A whole-sentence variant has no hint half. Returning verify.Hint's
	// answer here would put the exact wording the override exists to replace
	// back on the wire in the `hint` field, one line below the message that
	// no longer says it.
	if _, ok := connectionFailureWholeSentence(kind, code); ok {
		return ""
	}
	if code == verify.ERR_AUTH {
		switch kind {
		case connectionKindGit:
			return hintAuthGit
		case connectionKindVault, connectionKindSecrets:
			return hintAuthSecrets
		}
	}
	if hint := verify.Hint(code); hint != "" {
		return hint
	}
	switch code {
	case verify.ERR_NETWORK:
		return hintNetwork
	case verify.ERR_TLS:
		return hintTLS
	case verify.ERR_TIMEOUT:
		return hintTimeout
	}
	return ""
}

// connectionFailureFamilyOwnsHint reports whether THIS catalog, rather than
// verify.Hint, owns the hint wording for a (kind, code) pair.
//
// verify.Hint is written for a cluster connectivity check. Wherever this
// family has its own words for a pair — the kind-aware 401 hints, and every
// whole-sentence variant — the wire's `hint` field must come from here too,
// or the message and the field beside it disagree about who refused.
func connectionFailureFamilyOwnsHint(kind connectionKind, code verify.ErrorCode) bool {
	if _, ok := connectionFailureWholeSentence(kind, code); ok {
		return true
	}
	_, code = normalizeConnectionFailurePair(kind, code)
	return code == verify.ERR_AUTH && isKindAwareAuthKind(kind)
}

// ── the rendering function ──────────────────────────────────────────────

// renderConnectionFailure returns the FINISHED sentence for a (kind, code)
// pair. This is the only concatenation in the family: the server assembles
// here, the wire carries the result, and the browser is never handed two
// halves to join.
func renderConnectionFailure(kind connectionKind, code verify.ErrorCode) string {
	kind, code = normalizeConnectionFailurePair(kind, code)

	if whole, ok := connectionFailureWholeSentence(kind, code); ok {
		return whole
	}

	subject := connectionFailureSubjects[kind]
	hint := connectionFailureHint(kind, code)
	if hint == "" {
		return subject + connectionFailureNoHintTail
	}
	return subject + connectionFailureJoin + hint
}

// connectionFailureIDFor returns the stable identifier for a (kind, code)
// pair.
//
// THE NAMING RULE: "connectionFailure" + the kind's token + the code's token,
// both read from the tables above. It is mechanical on purpose, for the same
// reason connection_sentences.go's rule is: a rule with no judgement in it
// cannot be applied two different ways. The result opens lowercase, so it is a
// bare JavaScript property name in the generated file and needs no quoting —
// TestConnectionFailure_IdentifiersAreBareJSNames pins that.
//
// THESE IDENTIFIERS ARE A CONTRACT, exactly like the ones in
// connection_sentences.go. The browser depends on them; renaming one is a wire
// change. Change the words freely, change a name only deliberately.
func connectionFailureIDFor(kind connectionKind, code verify.ErrorCode) string {
	kind, code = normalizeConnectionFailurePair(kind, code)
	return "connectionFailure" + connectionKindTokens[kind] + connectionFailureCodeToken(code)
}

// connectionFailureCodeFor picks the failure code for a failed connection
// test. An ArgoCD error that says "this http address only sends Sharko on to
// https" gets its own code, picked by error TYPE (errors.Is), never by its
// words. Everything else goes through verify.ClassifyError as before.
func connectionFailureCodeFor(kind connectionKind, err error) verify.ErrorCode {
	if normalizeConnectionKind(kind) == connectionKindArgoCD && errors.Is(err, argocd.ErrArgocdNeedsHTTPS) {
		return connectionFailureCodeNeedsHTTPS
	}
	return verify.ClassifyError(err)
}

// renderConnectionFailureFor returns the finished sentence for a failed
// connection test, for the code connectionFailureCodeFor picked. It is
// renderConnectionFailure, except that the ArgoCD "needs https" sentence names
// the https address when Sharko knows it.
func renderConnectionFailureFor(kind connectionKind, code verify.ErrorCode, err error) string {
	kind, code = normalizeConnectionFailurePair(kind, code)
	if kind == connectionKindArgoCD && code == connectionFailureCodeNeedsHTTPS {
		if addr := argocd.HTTPSAddress(err); addr != "" {
			return fmt.Sprintf(argocdNeedsHTTPSAtFmt, addr)
		}
	}
	return renderConnectionFailure(kind, code)
}

// ── the catalog ─────────────────────────────────────────────────────────

// ConnectionFailureMessage is one parameterized catalog entry: the stable
// identifier, the two typed parameters it was rendered from, and the final
// server-rendered sentence.
type ConnectionFailureMessage struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Code     string `json:"code"`
	Sentence string `json:"sentence"`
}

// ConnectionFailureMessages is every sentence this family can produce, sorted
// by identifier.
//
// SOURCE OF TRUTH for cmd/gen-connection-sentences, which reads this slice at
// runtime — never a re-typed copy, and never a parse of Go source. Each
// Sentence is what renderConnectionFailure ACTUALLY RETURNS for that pair, so
// a catalog entry cannot describe words the server does not send: it was
// produced by the same function the handler calls.
//
// That also means a test asserting ConnectionFailureMessages[i].Sentence ==
// renderConnectionFailure(kind, code) compares a thing with itself and proves
// nothing. Do not write one. What proves this catalog honest is
// TestConnectionFailure_EveryVariantRendersItsSentence, which writes every
// expected sentence out as a literal.
var ConnectionFailureMessages = buildConnectionFailureMessages()

func buildConnectionFailureMessages() []ConnectionFailureMessage {
	out := make([]ConnectionFailureMessage, 0, len(connectionKinds)*len(connectionFailureCodes)+len(connectionFailureExtraPairs))
	add := func(kind connectionKind, code verify.ErrorCode) {
		out = append(out, ConnectionFailureMessage{
			ID:       connectionFailureIDFor(kind, code),
			Kind:     string(kind),
			Code:     string(code),
			Sentence: renderConnectionFailure(kind, code),
		})
	}
	for _, kind := range connectionKinds {
		for _, code := range connectionFailureCodes {
			add(kind, code)
		}
	}
	for _, pair := range connectionFailureExtraPairs {
		add(pair.kind, pair.code)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

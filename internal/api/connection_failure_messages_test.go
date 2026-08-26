package api

// connection_failure_messages_test.go — GUARD C: the parameterized message
// family says what it claims to say, all the way out to a real handler.
//
// # Every expected sentence below is a LITERAL
//
// Not renderConnectionFailure(...), not connectionFailureSubjects[kind] +
// connectionFailureJoin + hint. A test whose expected value is the same symbol
// as the actual value compares a thing with itself and stays green through any
// change to it — this repository has been bitten by that four separate times.
// So the fifty sentences are written out. It is long on purpose: the length IS
// the proof, and reading the list is how a person saw that
// connectionFailureGitRBAC used to tell a Git operator about a cluster.
// That one now carries the product owner's own sentence, pinned again by name
// and character for character in TestConnectionFailure_GitPermissionSentence.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// ── 1. every supported hint / parameter variant ─────────────────────────

// TestConnectionFailure_EveryVariantRendersItsSentence drives all five kinds
// against all ten failure codes and checks each finished sentence against a
// written-out literal.
func TestConnectionFailure_EveryVariantRendersItsSentence(t *testing.T) {
	cases := []struct {
		kind connectionKind
		code verify.ErrorCode
		want string
	}{
		// ── git ──────────────────────────────────────────────────────────
		{connectionKindGit, verify.ERR_NETWORK, "Sharko can't reach your Git host — the host could not be reached. Check the URL and network access."},
		{connectionKindGit, verify.ERR_TLS, "Sharko can't reach your Git host — a certificate error occurred while connecting. Check the server's TLS configuration."},
		{connectionKindGit, verify.ERR_AUTH, "Sharko can't reach your Git host — the Git host rejected the credentials (HTTP 401) — the token may be expired or invalid; regenerate the Git token/PAT in Settings → Connections and try again."},
		{connectionKindGit, verify.ERR_RBAC, "The Git server accepted the credentials but denied the requested operation. Grant the token permission for this repository on your Git host, or replace it in Settings → Connections."},
		{connectionKindGit, verify.ERR_AWS_STS, "Sharko can't reach your Git host — AWS STS token minting failed — check that Sharko's identity has permission to mint tokens for this cluster and that the cluster region is correctly configured."},
		{connectionKindGit, verify.ERR_AWS_ASSUME, "Sharko can't reach your Git host — the assume-role attempt failed — check the role's trust policy, ensure Sharko's identity has sts:AssumeRole permission on it, and verify sts:TagSession is granted if using EKS Pod Identity."},
		{connectionKindGit, verify.ERR_QUOTA, "Sharko can't reach your Git host. Check the connection settings and try again."},
		{connectionKindGit, verify.ERR_NAMESPACE, "Sharko can't reach your Git host. Check the connection settings and try again."},
		{connectionKindGit, verify.ERR_TIMEOUT, "Sharko can't reach your Git host — the request timed out. Check the URL and network access."},
		{connectionKindGit, verify.ERR_UNKNOWN, "Sharko can't reach your Git host. Check the connection settings and try again."},

		// ── argocd ───────────────────────────────────────────────────────
		{connectionKindArgoCD, verify.ERR_NETWORK, "Sharko can't reach ArgoCD — the host could not be reached. Check the URL and network access."},
		{connectionKindArgoCD, verify.ERR_TLS, "Sharko can't reach ArgoCD — a certificate error occurred while connecting. Check the server's TLS configuration."},
		{connectionKindArgoCD, verify.ERR_AUTH, "Sharko can't reach ArgoCD — the cluster rejected the credentials (HTTP 401) — the token may be expired or invalid; regenerate the kubeconfig/token and try again."},
		{connectionKindArgoCD, verify.ERR_RBAC, "Sharko can't reach ArgoCD — the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."},
		{connectionKindArgoCD, verify.ERR_AWS_STS, "Sharko can't reach ArgoCD — AWS STS token minting failed — check that Sharko's identity has permission to mint tokens for this cluster and that the cluster region is correctly configured."},
		{connectionKindArgoCD, verify.ERR_AWS_ASSUME, "Sharko can't reach ArgoCD — the assume-role attempt failed — check the role's trust policy, ensure Sharko's identity has sts:AssumeRole permission on it, and verify sts:TagSession is granted if using EKS Pod Identity."},
		{connectionKindArgoCD, verify.ERR_QUOTA, "Sharko can't reach ArgoCD. Check the connection settings and try again."},
		{connectionKindArgoCD, verify.ERR_NAMESPACE, "Sharko can't reach ArgoCD. Check the connection settings and try again."},
		{connectionKindArgoCD, verify.ERR_TIMEOUT, "Sharko can't reach ArgoCD — the request timed out. Check the URL and network access."},
		{connectionKindArgoCD, verify.ERR_UNKNOWN, "Sharko can't reach ArgoCD. Check the connection settings and try again."},

		// ── vault ────────────────────────────────────────────────────────
		{connectionKindVault, verify.ERR_NETWORK, "Sharko can't reach your secrets store — the host could not be reached. Check the URL and network access."},
		{connectionKindVault, verify.ERR_TLS, "Sharko can't reach your secrets store — a certificate error occurred while connecting. Check the server's TLS configuration."},
		{connectionKindVault, verify.ERR_AUTH, "Sharko can't reach your secrets store — the secrets store rejected the credentials (HTTP 401) — check the secrets-provider credentials and try again."},
		{connectionKindVault, verify.ERR_RBAC, "Sharko can't reach your secrets store — the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."},
		{connectionKindVault, verify.ERR_AWS_STS, "Sharko can't reach your secrets store — AWS STS token minting failed — check that Sharko's identity has permission to mint tokens for this cluster and that the cluster region is correctly configured."},
		{connectionKindVault, verify.ERR_AWS_ASSUME, "Sharko can't reach your secrets store — the assume-role attempt failed — check the role's trust policy, ensure Sharko's identity has sts:AssumeRole permission on it, and verify sts:TagSession is granted if using EKS Pod Identity."},
		{connectionKindVault, verify.ERR_QUOTA, "Sharko can't reach your secrets store. Check the connection settings and try again."},
		{connectionKindVault, verify.ERR_NAMESPACE, "Sharko can't reach your secrets store. Check the connection settings and try again."},
		{connectionKindVault, verify.ERR_TIMEOUT, "Sharko can't reach your secrets store — the request timed out. Check the URL and network access."},
		{connectionKindVault, verify.ERR_UNKNOWN, "Sharko can't reach your secrets store. Check the connection settings and try again."},

		// ── secrets (the same backend as vault, under its other name) ─────
		{connectionKindSecrets, verify.ERR_NETWORK, "Sharko can't reach your secrets store — the host could not be reached. Check the URL and network access."},
		{connectionKindSecrets, verify.ERR_TLS, "Sharko can't reach your secrets store — a certificate error occurred while connecting. Check the server's TLS configuration."},
		{connectionKindSecrets, verify.ERR_AUTH, "Sharko can't reach your secrets store — the secrets store rejected the credentials (HTTP 401) — check the secrets-provider credentials and try again."},
		{connectionKindSecrets, verify.ERR_RBAC, "Sharko can't reach your secrets store — the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."},
		{connectionKindSecrets, verify.ERR_AWS_STS, "Sharko can't reach your secrets store — AWS STS token minting failed — check that Sharko's identity has permission to mint tokens for this cluster and that the cluster region is correctly configured."},
		{connectionKindSecrets, verify.ERR_AWS_ASSUME, "Sharko can't reach your secrets store — the assume-role attempt failed — check the role's trust policy, ensure Sharko's identity has sts:AssumeRole permission on it, and verify sts:TagSession is granted if using EKS Pod Identity."},
		{connectionKindSecrets, verify.ERR_QUOTA, "Sharko can't reach your secrets store. Check the connection settings and try again."},
		{connectionKindSecrets, verify.ERR_NAMESPACE, "Sharko can't reach your secrets store. Check the connection settings and try again."},
		{connectionKindSecrets, verify.ERR_TIMEOUT, "Sharko can't reach your secrets store — the request timed out. Check the URL and network access."},
		{connectionKindSecrets, verify.ERR_UNKNOWN, "Sharko can't reach your secrets store. Check the connection settings and try again."},

		// ── other (the bucket every unrecognised kind renders as) ─────────
		{connectionKindOther, verify.ERR_NETWORK, "Sharko can't reach this connection — the host could not be reached. Check the URL and network access."},
		{connectionKindOther, verify.ERR_TLS, "Sharko can't reach this connection — a certificate error occurred while connecting. Check the server's TLS configuration."},
		{connectionKindOther, verify.ERR_AUTH, "Sharko can't reach this connection — the cluster rejected the credentials (HTTP 401) — the token may be expired or invalid; regenerate the kubeconfig/token and try again."},
		{connectionKindOther, verify.ERR_RBAC, "Sharko can't reach this connection — the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."},
		{connectionKindOther, verify.ERR_AWS_STS, "Sharko can't reach this connection — AWS STS token minting failed — check that Sharko's identity has permission to mint tokens for this cluster and that the cluster region is correctly configured."},
		{connectionKindOther, verify.ERR_AWS_ASSUME, "Sharko can't reach this connection — the assume-role attempt failed — check the role's trust policy, ensure Sharko's identity has sts:AssumeRole permission on it, and verify sts:TagSession is granted if using EKS Pod Identity."},
		{connectionKindOther, verify.ERR_QUOTA, "Sharko can't reach this connection. Check the connection settings and try again."},
		{connectionKindOther, verify.ERR_NAMESPACE, "Sharko can't reach this connection. Check the connection settings and try again."},
		{connectionKindOther, verify.ERR_TIMEOUT, "Sharko can't reach this connection — the request timed out. Check the URL and network access."},
		{connectionKindOther, verify.ERR_UNKNOWN, "Sharko can't reach this connection. Check the connection settings and try again."},
	}

	// The catalog must hold exactly the pairs this table covers — no pair
	// tested that the catalog forgot, no pair catalogued that this table
	// never looked at. Named both ways round, never counted.
	tested := map[string]bool{}

	for _, tc := range cases {
		id := connectionFailureIDFor(tc.kind, tc.code)
		tested[id] = true
		if got := renderConnectionFailure(tc.kind, tc.code); got != tc.want {
			t.Errorf("%s (kind=%s code=%s):\n got: %q\nwant: %q", id, tc.kind, tc.code, got, tc.want)
		}
		// The catalog entry is what the wire and the browser file carry, so
		// it must agree with the same literal.
		entry, ok := connectionFailureEntry(id)
		if !ok {
			t.Errorf("%s is not in ConnectionFailureMessages at all", id)
			continue
		}
		if entry.Sentence != tc.want {
			t.Errorf("ConnectionFailureMessages[%s].Sentence:\n got: %q\nwant: %q", id, entry.Sentence, tc.want)
		}
	}

	for _, entry := range ConnectionFailureMessages {
		if !tested[entry.ID] {
			t.Errorf("the catalog holds %q, which no case in this table checks — add its literal", entry.ID)
		}
	}
}

// connectionFailureEntry looks a catalog entry up by identifier.
func connectionFailureEntry(id string) (ConnectionFailureMessage, bool) {
	for _, entry := range ConnectionFailureMessages {
		if entry.ID == id {
			return entry, true
		}
	}
	return ConnectionFailureMessage{}, false
}

// ── 2. the empty / absent optional hint ─────────────────────────────────

// TestConnectionFailure_AbsentHintUsesTheTailNotTheSeparator covers the three
// codes verify.Hint says nothing about and this family adds nothing for:
// ERR_QUOTA, ERR_NAMESPACE and ERR_UNKNOWN. This is the case a second
// formatter gets wrong by leaving the dash dangling with nothing after it.
func TestConnectionFailure_AbsentHintUsesTheTailNotTheSeparator(t *testing.T) {
	for _, code := range []verify.ErrorCode{verify.ERR_QUOTA, verify.ERR_NAMESPACE, verify.ERR_UNKNOWN} {
		if hint := connectionFailureHint(connectionKindGit, code); hint != "" {
			t.Fatalf("this test is aimed at the no-hint case, but %s now has the hint %q — "+
				"either the case moved or the test did", code, hint)
		}
		got := renderConnectionFailure(connectionKindGit, code)
		want := "Sharko can't reach your Git host. Check the connection settings and try again."
		if got != want {
			t.Errorf("%s with no hint:\n got: %q\nwant: %q", code, got, want)
		}
		if strings.Contains(got, " — ") {
			t.Errorf("%s has no hint, so the sentence must not carry the separator at all: %q", code, got)
		}
		if strings.HasSuffix(got, "—") || strings.HasSuffix(got, "— ") || strings.HasSuffix(got, " ") {
			t.Errorf("%s left a dangling separator or trailing space: %q", code, got)
		}
	}
}

// ── 3. punctuation and whitespace at the join ───────────────────────────

// TestConnectionFailure_JoinPunctuation is the deliberate one: it pins the
// exact bytes where the subject meets the hint, for every catalogued sentence.
//
// The separator and the subjects are written out as LITERALS here rather than
// read from connectionFailureJoin / connectionFailureSubjects. That is the
// whole point — change the constant and this test must fail, which it cannot
// do if it takes its expectation from the same constant.
//
// Note what is NOT asserted: "the sentence contains one em dash". Several
// hints carry an em dash of their own (verify.Hint's ERR_AUTH text does), so a
// count would be wrong on real data. What matters is the JOIN.
//
// THE WHOLE-SENTENCE VARIANTS ARE EXCLUDED FROM THE SUBJECT/JOIN HALF, and
// they are excluded BY NAME below rather than by asking the production map,
// which would let a new variant switch this test off for itself. The
// whitespace and punctuation hygiene at the bottom still applies to every
// variant, whole-sentence ones included.
func TestConnectionFailure_JoinPunctuation(t *testing.T) {
	const separator = " — "
	const tail = ". Check the connection settings and try again."

	// The pairs that render as one complete sentence, written out here. This
	// list ratchets both ways: a pair that gains a whole sentence without
	// being listed fails the subject check, and a pair listed here that no
	// longer has one is named as stale.
	wholeSentenceVariants := []connectionFailureKey{
		{kind: connectionKindGit, code: verify.ERR_RBAC},
	}
	isWholeSentence := map[connectionFailureKey]bool{}
	for _, key := range wholeSentenceVariants {
		if _, ok := connectionFailureWholeSentence(key.kind, key.code); !ok {
			t.Errorf("kind=%s code=%s is listed here as a whole-sentence variant but no longer has one — "+
				"remove the stale entry, or the subject/join rule is being skipped for a pair that now obeys it",
				key.kind, key.code)
		}
		isWholeSentence[key] = true
	}

	subjects := map[connectionKind]string{
		connectionKindGit:     "Sharko can't reach your Git host",
		connectionKindArgoCD:  "Sharko can't reach ArgoCD",
		connectionKindVault:   "Sharko can't reach your secrets store",
		connectionKindSecrets: "Sharko can't reach your secrets store",
		connectionKindOther:   "Sharko can't reach this connection",
	}

	for _, kind := range connectionKinds {
		subject, ok := subjects[kind]
		if !ok {
			t.Fatalf("kind %q has no subject written out in this test — add its literal", kind)
		}
		// A subject that already ends in punctuation is how the browser's
		// hand-typed copies came to disagree with the server.
		if strings.HasSuffix(subject, ".") || strings.HasSuffix(subject, " ") {
			t.Errorf("the subject for %q ends in punctuation or a space: %q", kind, subject)
		}

		for _, code := range connectionFailureCodes {
			got := renderConnectionFailure(kind, code)
			hint := connectionFailureHint(kind, code)

			key := connectionFailureKey{kind: kind, code: code}
			if _, hasWhole := connectionFailureWholeSentence(kind, code); hasWhole && !isWholeSentence[key] {
				t.Errorf("kind=%s code=%s has a whole-sentence variant that this test does not list — "+
					"add it to wholeSentenceVariants above and write its literal out in "+
					"TestConnectionFailure_EveryVariantRendersItsSentence",
					kind, code)
			}

			if isWholeSentence[key] {
				// A whole-sentence variant has no subject and no join, so
				// there is nothing here to check — but it must not have
				// silently gone back to being assembled either.
				if strings.HasPrefix(got, subject) {
					t.Errorf("%s is listed as a whole-sentence variant but still opens with the assembled subject %q: %q",
						connectionFailureIDFor(kind, code), subject, got)
				}
				if hint != "" {
					t.Errorf("%s is a whole-sentence variant, so it must have no hint half — it has %q",
						connectionFailureIDFor(kind, code), hint)
				}
			} else if !strings.HasPrefix(got, subject) {
				t.Errorf("%s does not open with its subject.\n got: %q\nwant prefix: %q",
					connectionFailureIDFor(kind, code), got, subject)
			} else {
				rest := got[len(subject):]

				if hint == "" {
					if rest != tail {
						t.Errorf("%s has no hint, so what follows the subject must be exactly the tail.\n got: %q\nwant: %q",
							connectionFailureIDFor(kind, code), rest, tail)
					}
				} else if !strings.HasPrefix(rest, separator) {
					t.Errorf("%s does not join with exactly %q.\n got after the subject: %q",
						connectionFailureIDFor(kind, code), separator, rest)
				} else {
					if rest[len(separator):] != hint {
						t.Errorf("%s carries something other than the hint after the join.\n got: %q\nwant: %q",
							connectionFailureIDFor(kind, code), rest[len(separator):], hint)
					}
					// A doubled separator at the join — " —  — " or " — — " —
					// is what happens when two formatters both add one.
					if strings.HasPrefix(rest, separator+"—") || strings.HasPrefix(rest, separator+" ") {
						t.Errorf("%s has a doubled separator at the join: %q",
							connectionFailureIDFor(kind, code), got)
					}
				}
			}

			// Whole-sentence hygiene, for every variant.
			if strings.Contains(got, "  ") {
				t.Errorf("%s contains a double space: %q", connectionFailureIDFor(kind, code), got)
			}
			if strings.Contains(got, " ..") || strings.Contains(got, "..") {
				t.Errorf("%s contains a doubled full stop: %q", connectionFailureIDFor(kind, code), got)
			}
			if got != strings.TrimSpace(got) {
				t.Errorf("%s has leading or trailing whitespace: %q", connectionFailureIDFor(kind, code), got)
			}
			if strings.HasSuffix(got, "—") || strings.HasSuffix(strings.TrimSpace(got), "—") {
				t.Errorf("%s ends on a dangling separator: %q", connectionFailureIDFor(kind, code), got)
			}
		}
	}
}

// ── 3b. the Git permission sentence, and its five ruled pins ────────────

// The absence pin is checked on EVERY place a Git connection's words reach a
// person: the rendered sentence, the catalog entry the wire and the generated
// browser file are built from, and both prose fields of the JSON body.
// Checking only the rendered sentence is how the old wording would have
// survived in the `hint` field after the message stopped saying it.

// TestConnectionFailure_GitPermissionSentence is the ruling's pin, and it
// carries all five things the ruling asked for:
//
//  1. the exact sentence, character for character
//  2. Git capitalised
//  3. the correct Git reason code
//  4. the ABSENCE of "cluster accepted" on every Git connection surface
//  5. no credential or provider error detail anywhere in it
//
// The expected text is written out here as a literal. It is NOT
// gitPermissionRefusal, and it must never become gitPermissionRefusal: a test
// that reads the same constant the production code reads compares a thing with
// itself and would stay green through any rewording of it.
func TestConnectionFailure_GitPermissionSentence(t *testing.T) {
	// PIN 1 — the product owner's text, supplied verbatim on 2026-08-20.
	const ruled = "The Git server accepted the credentials but denied the requested operation."

	got := renderConnectionFailure(connectionKindGit, verify.ERR_RBAC)

	if !strings.HasPrefix(got, ruled) {
		t.Errorf("the Git permission sentence does not open with the ruled text.\n got: %q\nwant prefix: %q", got, ruled)
	}
	if !strings.Contains(got, ruled) {
		t.Errorf("the ruled sentence is not in the Git permission message at all:\n %q", got)
	}

	// PIN 2 — Git is a proper noun. "git server" would satisfy pin 1 nowhere,
	// but a later reword could lowercase it, so it is pinned on its own.
	if strings.Contains(got, "git ") || strings.Contains(got, " git") {
		t.Errorf("the Git permission sentence writes Git in lowercase: %q", got)
	}
	if !strings.Contains(got, "Git server") {
		t.Errorf("the Git permission sentence no longer names the Git server: %q", got)
	}

	// PIN 3 — the correct Git reason code, on both the identifier the browser
	// routes on and the wire's own code field. Written out, not derived.
	if id := connectionFailureIDFor(connectionKindGit, verify.ERR_RBAC); id != "connectionFailureGitRBAC" {
		t.Errorf("the Git permission variant's identifier is %q, want %q", id, "connectionFailureGitRBAC")
	}
	if string(verify.ERR_RBAC) != "ERR_RBAC" {
		t.Errorf("the Git permission variant's reason code is %q, want %q", verify.ERR_RBAC, "ERR_RBAC")
	}

	// PIN 4 — the absence, on every Git surface. This pin did not exist
	// before 2026-08-20, which is how a cluster-flavoured sentence sat in
	// front of Git operators through five review rounds.
	entry, ok := connectionFailureEntry("connectionFailureGitRBAC")
	if !ok {
		t.Fatal("connectionFailureGitRBAC is not in the catalog at all")
	}
	surfaces := map[string]string{
		"the rendered sentence":                                got,
		"the catalog entry the browser file is generated from": entry.Sentence,
		"the wire body's message": func() string {
			body := connectionErrorFields(connectionKindGit, errors.New("403 Forbidden"))
			s, _ := body["message"].(string)
			return s
		}(),
		"the wire body's hint": func() string {
			body := connectionErrorFields(connectionKindGit, errors.New("403 Forbidden"))
			s, _ := body["hint"].(string)
			return s
		}(),
	}
	for where, text := range surfaces {
		lower := strings.ToLower(text)
		for _, banned := range []string{"cluster accepted", "this cluster", "the cluster", "service account", "kubeconfig"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s says %q about a Git connection — it was the Git server that refused, not a cluster:\n %q",
					where, banned, text)
			}
		}
	}

	// PIN 5 — no credential or provider error detail. The classifier is fed a
	// raw provider error carrying a sentinel; none of it may come back out.
	const sentinel = "ghp_SENTINELtokenVALUE1234567890"
	body := connectionErrorFields(connectionKindGit, errors.New("GET https://git.example.com/api/v1/repos: 403 Forbidden (token "+sentinel+")"))
	rendered, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the failure body: %v", err)
	}
	if strings.Contains(string(rendered), sentinel) {
		t.Errorf("the Git failure body leaked the raw provider error text: %s", rendered)
	}
	if strings.Contains(string(rendered), "git.example.com") {
		t.Errorf("the Git failure body leaked the provider's own error detail: %s", rendered)
	}
	if msg, _ := body["message"].(string); msg != got {
		t.Errorf("the wire message is not the catalogued sentence.\n got: %q\nwant: %q", msg, got)
	}
	if id, _ := body["message_id"].(string); id != "connectionFailureGitRBAC" {
		t.Errorf("the wire message_id is %q, want %q", id, "connectionFailureGitRBAC")
	}
	if code, _ := body["code"].(string); code != "ERR_RBAC" {
		t.Errorf("the wire code is %q, want %q", code, "ERR_RBAC")
	}
}

// TestConnectionFailure_OtherKindsKeepTheirPermissionWording is the
// counterweight: the Git fix must not have reworded anybody else's 403.
//
// Every expected sentence is a literal, and the sentences are checked BY KIND
// so a kind that silently started rendering the Git text is named.
func TestConnectionFailure_OtherKindsKeepTheirPermissionWording(t *testing.T) {
	unchanged := []struct {
		kind connectionKind
		want string
	}{
		{connectionKindArgoCD, "Sharko can't reach ArgoCD — the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."},
		{connectionKindVault, "Sharko can't reach your secrets store — the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."},
		{connectionKindSecrets, "Sharko can't reach your secrets store — the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."},
		{connectionKindOther, "Sharko can't reach this connection — the cluster accepted the credentials but denied the action (HTTP 403) — grant the service account the required RBAC permissions and try again."},
	}
	for _, tc := range unchanged {
		if got := renderConnectionFailure(tc.kind, verify.ERR_RBAC); got != tc.want {
			t.Errorf("the %s 403 sentence changed:\n got: %q\nwant: %q", tc.kind, got, tc.want)
		}
		if strings.Contains(renderConnectionFailure(tc.kind, verify.ERR_RBAC), "The Git server") {
			t.Errorf("the %s 403 sentence now talks about a Git server", tc.kind)
		}
		// And their 401s, which share the kind-aware-hint machinery the Git
		// fix widened.
		if got := renderConnectionFailure(tc.kind, verify.ERR_AUTH); strings.Contains(got, "The Git server") {
			t.Errorf("the %s 401 sentence now talks about a Git server: %q", tc.kind, got)
		}
	}

	// The one place a whole-sentence variant exists must be the only one.
	if len(connectionFailureWholeSentences) != 1 {
		var names []string
		for key := range connectionFailureWholeSentences {
			names = append(names, string(key.kind)+"/"+string(key.code))
		}
		sort.Strings(names)
		t.Errorf("whole-sentence variants: %v — only git/ERR_RBAC was ruled on. "+
			"A new one is a product decision, not a refactor: name it in TestConnectionFailure_JoinPunctuation "+
			"and write its literal out in TestConnectionFailure_EveryVariantRendersItsSentence.",
			names)
	}
}

// ── 4. an unsupported parameter value ───────────────────────────────────

// TestConnectionFailure_UnsupportedParameterValues checks that a kind or a
// code nobody wrote down renders the generic sentence under an identifier the
// browser has ALREADY been given — never a new one it has never seen.
//
// This is not hypothetical for the code half: internal/verify/stage2.go puts
// "ERR_NOT_IMPLEMENTED" on a Result, and that string is in no const block.
func TestConnectionFailure_UnsupportedParameterValues(t *testing.T) {
	cases := []struct {
		name   string
		kind   connectionKind
		code   verify.ErrorCode
		wantID string
		want   string
	}{
		{
			name:   "unknown kind falls back to the generic subject",
			kind:   connectionKind("mystery"),
			code:   verify.ERR_NETWORK,
			wantID: "connectionFailureOtherNetwork",
			want:   "Sharko can't reach this connection — the host could not be reached. Check the URL and network access.",
		},
		{
			name:   "empty kind falls back to the generic subject",
			kind:   connectionKind(""),
			code:   verify.ERR_TLS,
			wantID: "connectionFailureOtherTLS",
			want:   "Sharko can't reach this connection — a certificate error occurred while connecting. Check the server's TLS configuration.",
		},
		{
			name:   "a code that is in no const block renders as unknown",
			kind:   connectionKindGit,
			code:   verify.ErrorCode("ERR_NOT_IMPLEMENTED"),
			wantID: "connectionFailureGitUnknown",
			want:   "Sharko can't reach your Git host. Check the connection settings and try again.",
		},
		{
			name:   "both parameters unsupported",
			kind:   connectionKind("nonsense"),
			code:   verify.ErrorCode("ERR_MADE_UP"),
			wantID: "connectionFailureOtherUnknown",
			want:   "Sharko can't reach this connection. Check the connection settings and try again.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderConnectionFailure(tc.kind, tc.code); got != tc.want {
				t.Errorf("sentence:\n got: %q\nwant: %q", got, tc.want)
			}
			gotID := connectionFailureIDFor(tc.kind, tc.code)
			if gotID != tc.wantID {
				t.Errorf("identifier: got %q, want %q", gotID, tc.wantID)
			}
			if _, ok := connectionFailureEntry(gotID); !ok {
				t.Errorf("an unsupported value produced %q, which is in no catalog — the browser has never been given it", gotID)
			}
		})
	}
}

// ── 5. completeness, identifiers and generated-output equality ──────────

// TestConnectionFailure_EveryDeclaredErrorCodeHasAToken is the completeness
// guard, and it is a LIST, never a count.
//
// It parses internal/verify/errors.go — the source of truth — and names every
// ErrorCode declared there that connectionFailureCodeTokens has no token for.
// Add a code to internal/verify and nothing else, and this fails saying which
// one. A "there are at least N codes" floor would pass while the new code
// silently rendered as ERR_UNKNOWN under someone else's identifier.
func TestConnectionFailure_EveryDeclaredErrorCodeHasAToken(t *testing.T) {
	declared := declaredVerifyErrorCodes(t)
	if len(declared) == 0 {
		t.Fatal("no ErrorCode constants were parsed out of internal/verify/errors.go at all — this guard is blind")
	}

	var missing []string
	for name, value := range declared {
		if _, ok := connectionFailureCodeTokens[verify.ErrorCode(value)]; !ok {
			missing = append(missing, "  "+name+" ("+value+")")
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("these verify.ErrorCode constants have no entry in connectionFailureCodeTokens:\n%s\n"+
			"Every one of them would render as the generic ERR_UNKNOWN sentence under somebody else's\n"+
			"identifier, so the browser would be told the wrong thing and never know.\n"+
			"Add a token to connectionFailureCodeTokens and the code to connectionFailureCodes in\n"+
			"internal/api/connection_failure_messages.go.", strings.Join(missing, "\n"))
	}

	// And the other direction: a token for a code that no longer exists is a
	// stale entry that puts dead sentences in the browser's file.
	values := map[string]bool{}
	for _, v := range declared {
		values[v] = true
	}
	var stale []string
	for code := range connectionFailureCodeTokens {
		if !values[string(code)] {
			stale = append(stale, "  "+string(code))
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("connectionFailureCodeTokens has token(s) for code(s) that internal/verify no longer declares:\n%s",
			strings.Join(stale, "\n"))
	}

	// The two lists in the production file must agree with each other too —
	// a code with a token but not in connectionFailureCodes never reaches the
	// catalog at all.
	inCatalogOrder := map[verify.ErrorCode]bool{}
	for _, code := range connectionFailureCodes {
		inCatalogOrder[code] = true
	}
	for code := range connectionFailureCodeTokens {
		if !inCatalogOrder[code] {
			t.Errorf("%s has a token but is not in connectionFailureCodes, so no sentence for it is ever catalogued", code)
		}
	}
	for code := range inCatalogOrder {
		if _, ok := connectionFailureCodeTokens[code]; !ok {
			t.Errorf("%s is in connectionFailureCodes but has no token, so its identifier would collide with another", code)
		}
	}
}

// declaredVerifyErrorCodes parses internal/verify/errors.go and returns every
// constant of type ErrorCode as name → value.
func declaredVerifyErrorCodes(t *testing.T) map[string]string {
	t.Helper()
	path := filepath.Join(repoRootForSweep(t), "internal", "verify", "errors.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "ErrorCode" {
				continue
			}
			for i, val := range vs.Values {
				lit, ok := val.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || i >= len(vs.Names) {
					continue
				}
				text, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil {
					continue
				}
				out[vs.Names[i].Name] = text
			}
		}
	}
	return out
}

// TestConnectionFailure_EveryKindHasATokenAndASubject is the same guard for
// the other parameter, by name.
func TestConnectionFailure_EveryKindHasATokenAndASubject(t *testing.T) {
	for _, kind := range connectionKinds {
		if _, ok := connectionKindTokens[kind]; !ok {
			t.Errorf("kind %q is in connectionKinds but has no identifier token", kind)
		}
		if _, ok := connectionFailureSubjects[kind]; !ok {
			t.Errorf("kind %q is in connectionKinds but has no subject — it would render an empty opening", kind)
		}
	}
	for kind := range connectionKindTokens {
		if _, ok := connectionFailureSubjects[kind]; !ok {
			t.Errorf("kind %q has a token but no subject", kind)
		}
	}
	for kind := range connectionFailureSubjects {
		if _, ok := connectionKindTokens[kind]; !ok {
			t.Errorf("kind %q has a subject but no token", kind)
		}
	}
}

// TestConnectionFailure_CatalogCoversEveryPair names any (kind, code) pair the
// catalog is missing, and any entry it holds that no pair produces.
func TestConnectionFailure_CatalogCoversEveryPair(t *testing.T) {
	inCatalog := map[string]bool{}
	for _, entry := range ConnectionFailureMessages {
		if inCatalog[entry.ID] {
			t.Errorf("the catalog holds %q twice — one entry silently shadows the other", entry.ID)
		}
		inCatalog[entry.ID] = true
	}

	expected := map[string]bool{}
	for _, kind := range connectionKinds {
		for _, code := range connectionFailureCodes {
			id := connectionFailureIDFor(kind, code)
			expected[id] = true
			if !inCatalog[id] {
				t.Errorf("kind=%s code=%s renders a sentence but %q is not in the catalog", kind, code, id)
			}
		}
	}
	for id := range inCatalog {
		if !expected[id] {
			t.Errorf("the catalog holds %q, which no (kind, code) pair produces", id)
		}
	}
}

// TestConnectionFailure_IdentifiersAreBareJSNames pins the property the
// generated TypeScript depends on: every identifier is a bare JavaScript
// property name, so the generator needs no quoting and no per-key decision.
func TestConnectionFailure_IdentifiersAreBareJSNames(t *testing.T) {
	bare := regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	for _, entry := range ConnectionFailureMessages {
		if !bare.MatchString(entry.ID) {
			t.Errorf("%q is not a bare JavaScript property name — the generated file would need quoting", entry.ID)
		}
		if !strings.HasPrefix(entry.ID, "connectionFailure") {
			t.Errorf("%q does not follow the naming rule (connectionFailure + kind token + code token)", entry.ID)
		}
	}

	// A handful of identifiers written out, so the naming rule is pinned by
	// something other than the rule itself.
	for _, want := range []string{
		"connectionFailureGitAuth",
		"connectionFailureArgoCDNetwork",
		"connectionFailureVaultAWSAssume",
		"connectionFailureSecretsRBAC",
		"connectionFailureOtherUnknown",
	} {
		if _, ok := connectionFailureEntry(want); !ok {
			t.Errorf("the catalog has no entry %q", want)
		}
	}
}

// TestConnectionFailure_CatalogParametersMatchTheIdentifier checks the two
// typed parameters recorded on each entry really are the ones its identifier
// names — the entry is what the generator and any API consumer reads.
func TestConnectionFailure_CatalogParametersMatchTheIdentifier(t *testing.T) {
	for _, entry := range ConnectionFailureMessages {
		id := connectionFailureIDFor(connectionKind(entry.Kind), verify.ErrorCode(entry.Code))
		if id != entry.ID {
			t.Errorf("entry %q records kind=%q code=%q, which name the identifier %q instead",
				entry.ID, entry.Kind, entry.Code, id)
		}
		if entry.Sentence == "" {
			t.Errorf("entry %q has an empty sentence", entry.ID)
		}
	}
}

// ── 6. the complete sentence, as a real handler returns it ──────────────

// failingCredProvider is a cluster-credentials backend whose ListClusters
// always fails with a chosen error, so a real handler can be driven into the
// failure body with a controlled classification.
type failingCredProvider struct{ err error }

func (p failingCredProvider) GetCredentials(string) (*providers.Kubeconfig, error) { return nil, p.err }
func (p failingCredProvider) ListClusters() ([]providers.ClusterInfo, error)       { return nil, p.err }
func (p failingCredProvider) SearchSecrets(string) ([]string, error)               { return nil, nil }
func (p failingCredProvider) HealthCheck(context.Context) error                    { return p.err }

// TestConnectionFailure_HandlerReturnsTheCompletedSentence drives
// POST /api/v1/providers/test through the real router and asserts the body the
// browser actually receives.
//
// Testing renderConnectionFailure alone would pass while the handler sent
// something else entirely — the handler is where the wire contract lives, so
// this is the test that decides whether the ruling is met. Every expected
// value is a literal.
func TestConnectionFailure_HandlerReturnsTheCompletedSentence(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		wantMessage string
		wantID      string
	}{
		{
			name:        "network failure",
			err:         errors.New("dial tcp 10.0.0.1:443: connect: connection refused"),
			wantMessage: "Sharko can't reach your secrets store — the host could not be reached. Check the URL and network access.",
			wantID:      "connectionFailureVaultNetwork",
		},
		{
			name:        "credentials rejected",
			err:         errors.New("401 unauthorized"),
			wantMessage: "Sharko can't reach your secrets store — the secrets store rejected the credentials (HTTP 401) — check the secrets-provider credentials and try again.",
			wantID:      "connectionFailureVaultAuth",
		},
		{
			name:        "nothing classifiable, so the tail rather than a dangling dash",
			err:         errors.New("some AWS SDK internal error XYZ123"),
			wantMessage: "Sharko can't reach your secrets store. Check the connection settings and try again.",
			wantID:      "connectionFailureVaultUnknown",
		},
		{
			name:        "certificate failure",
			err:         errors.New("x509: certificate signed by unknown authority"),
			wantMessage: "Sharko can't reach your secrets store — a certificate error occurred while connecting. Check the server's TLS configuration.",
			wantID:      "connectionFailureVaultTLS",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newIsolatedTestServer(t)
			installCredProvider(srv, failingCredProvider{err: tc.err}, nil, nil)
			router := NewRouter(srv, nil)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/providers/test", nil)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200. body: %s", w.Code, w.Body.String())
			}
			var body map[string]interface{}
			if err := json.NewDecoder(bytes.NewReader(w.Body.Bytes())).Decode(&body); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}

			if body["status"] != "error" {
				t.Errorf("status field = %v, want \"error\"", body["status"])
			}
			// THE WIRE CARRIES THE COMPLETED SENTENCE. Not a fragment, not a
			// template, not a pair of halves for the browser to join.
			if got, _ := body["message"].(string); got != tc.wantMessage {
				t.Errorf("message:\n got: %q\nwant: %q", got, tc.wantMessage)
			}
			// …under the stable identifier, so the browser can route on the
			// message without ever matching on its text.
			gotID, _ := body["message_id"].(string)
			if gotID != tc.wantID {
				t.Errorf("message_id: got %q, want %q", gotID, tc.wantID)
			}
			entry, ok := connectionFailureEntry(gotID)
			if !ok {
				t.Fatalf("the handler sent message_id %q, which is in no catalog — the browser has never been given it", gotID)
			}
			if entry.Sentence != tc.wantMessage {
				t.Errorf("the catalog entry for %q holds %q, but the handler sent %q — the generated browser file and the wire disagree",
					gotID, entry.Sentence, tc.wantMessage)
			}
			// No raw backend text ever reaches the operator.
			if strings.Contains(w.Body.String(), tc.err.Error()) {
				t.Errorf("the response leaked the raw error text: %s", w.Body.String())
			}
		})
	}
}

// ── the server holds no hand-written half any more ──────────────────────

// TestConnectionMessages_HoldNoProse pins that connection_messages.go — which
// used to hold the three fragments, the join and the tail — now holds no prose
// at all. Every word of this family lives in connection_failure_messages.go.
//
// Without this, the fragments could simply be re-typed next door to where they
// were and every other test in the tree would stay green.
func TestConnectionMessages_HoldNoProse(t *testing.T) {
	path := filepath.Join(repoRootForSweep(t), "internal", "api", "connection_messages.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, unquoteErr := strconv.Unquote(lit.Value)
		if unquoteErr != nil {
			return true
		}
		// Wire keys ("status", "message_id") have no spaces. Anything with a
		// space in this file is a sentence or a piece of one.
		if strings.Contains(text, " ") {
			found = append(found, "  line "+strconv.Itoa(fset.Position(lit.Pos()).Line)+": "+strconv.Quote(text))
		}
		return true
	})
	if len(found) > 0 {
		sort.Strings(found)
		t.Errorf("connection_messages.go holds string literal(s) that read like prose:\n%s\n"+
			"This file assembles nothing and says nothing. Every fragment, hint, join and tail of the\n"+
			"connection-failure family belongs in internal/api/connection_failure_messages.go, where it\n"+
			"is catalogued and shipped to the browser finished.", strings.Join(found, "\n"))
	}
}

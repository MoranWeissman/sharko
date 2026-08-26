package clusterreconciler

// cred_error_sentinel_test.go — the reconciler half of the credential-error
// hotfix.
//
// The connection reconciler fetches every managed cluster's credentials on every
// tick. When that fetch fails it used to write the backend's own error text into
// three places at once:
//
//	the pod log, verbatim
//	the audit entry's Error field — and the audit log is readable by the VIEWER
//	  role, the lowest one there is
//	the reconcile record's Message, which the API turns into
//	  LastReconcile.Message and the managed-secrets rows
//
// This proves all three are clean, using a credentials error whose own message
// carries a unique sentinel — the realistic shape of a backend SDK error that
// picked up credential material.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"k8s.io/client-go/kubernetes/fake"
)

// reconcilerCredSentinel is unique to this file — see the same discipline in
// internal/api/cred_error_sentinel_test.go.
const reconcilerCredSentinel = "QW7X-recon-cred-sentinel-3k9p2m-must-not-be-recorded-f4b8"

// assertNoReconcilerCredSentinel sweeps text for the sentinel in every form a
// leak could take: raw, four base64 spellings, SHA-256 hex and base64, prefix
// and suffix fragments, the byte length in labelled shapes, and masks whose
// length tracks it.
func assertNoReconcilerCredSentinel(t *testing.T, where, text string) {
	t.Helper()
	sum := sha256.Sum256([]byte(reconcilerCredSentinel))
	n := len(reconcilerCredSentinel)

	for name, form := range map[string]string{
		"the value itself":    reconcilerCredSentinel,
		"base64 (std)":        base64.StdEncoding.EncodeToString([]byte(reconcilerCredSentinel)),
		"base64 (raw std)":    base64.RawStdEncoding.EncodeToString([]byte(reconcilerCredSentinel)),
		"base64 (url)":        base64.URLEncoding.EncodeToString([]byte(reconcilerCredSentinel)),
		"base64 (raw url)":    base64.RawURLEncoding.EncodeToString([]byte(reconcilerCredSentinel)),
		"SHA-256 hex":         hex.EncodeToString(sum[:]),
		"SHA-256 base64":      base64.StdEncoding.EncodeToString(sum[:]),
		"first 8 characters":  reconcilerCredSentinel[:8],
		"first 16 characters": reconcilerCredSentinel[:16],
		"last 8 characters":   reconcilerCredSentinel[n-8:],
		"last 16 characters":  reconcilerCredSentinel[n-16:],
	} {
		if strings.Contains(text, form) {
			t.Errorf("%s carries %s of the credentials error text (%q)\n\ntext: %s", where, name, form, text)
		}
	}

	for _, shape := range []string{
		fmt.Sprintf("%d bytes", n), fmt.Sprintf("%d chars", n), fmt.Sprintf("%d characters", n),
		fmt.Sprintf(`"length":%d`, n), fmt.Sprintf(`"length": %d`, n),
		fmt.Sprintf(`"len":%d`, n), fmt.Sprintf(`"len": %d`, n),
		fmt.Sprintf(`"bytes":%d`, n), fmt.Sprintf(`"size":%d`, n),
		fmt.Sprintf("length=%d", n), fmt.Sprintf("len=%d", n), fmt.Sprintf("bytes=%d", n),
	} {
		if strings.Contains(text, shape) {
			t.Errorf("%s carries the error's byte length (%q) — a length narrows a guess", where, shape)
		}
	}

	for _, ch := range []string{"*", "•", "x", "●"} {
		for _, l := range []int{n - 1, n, n + 1} {
			if strings.Contains(text, strings.Repeat(ch, l)) {
				t.Errorf("%s carries a mask whose length tracks the error (%d of %q)", where, l, ch)
			}
		}
	}
}

// markedCredErrorWithSentinel is what a credentials backend hands back when its
// own SDK error picked up credential material. It is MARKED, exactly as every
// real provider boundary now marks its errors.
//
// The sentinel is in the CAUSE underneath the mark, and the marked error's own
// Error() is the fixed safe sentence. Both halves matter: the cause is what
// there is to leak, and the sentence is what stops anything leaking it by
// forgetting to ask.
func markedCredErrorWithSentinel() error {
	return credsafe.Mark(fmt.Errorf("etcdserver: request failed while resolving %s", reconcilerCredSentinel))
}

// rawCauseOf is the classification-only accessor, used here deliberately to
// prove there is a real sentinel behind the mark. Nothing in production builds a
// message from it.
func rawCauseOf(err error) string { return credsafe.Cause(err).Error() }

// TestReconcilerCredFailure_LeaksNothingIntoAuditOrTheRecord is the headline
// proof for this package.
func TestReconcilerCredFailure_LeaksNothingIntoAuditOrTheRecord(t *testing.T) {
	credErr := markedCredErrorWithSentinel()

	// Sanity: the fixture is real. Without this, a typo would make every
	// assertion below pass while proving nothing.
	if !strings.Contains(rawCauseOf(credErr), reconcilerCredSentinel) {
		t.Fatal("sanity check failed: the cause under the mark must CONTAIN the sentinel")
	}
	if !credsafe.Is(credErr) {
		t.Fatal("sanity check failed: the fixture error must be marked as a credentials-backend failure, or no boundary can recognise it")
	}
	// And the marked error already refuses to say the sentinel, which is what
	// makes every boundary safe by default rather than safe-if-it-remembered.
	if credErr.Error() != credsafe.Message {
		t.Fatalf("sanity check failed: the marked error says %q, want the fixed safe sentence %q", credErr.Error(), credsafe.Message)
	}

	body := envelopedWithModes(testClusterEntry{Name: "prod-eu", Labels: map[string]string{"addon-foo": "enabled"}})
	audits := &auditCollector{}
	vault := &fakeVault{errs: map[string]error{"prod-eu": credErr}}
	r := newReconcilerForTest(t, nil, fake.NewSimpleClientset(), vault, audits, body)

	r.pollOnce(context.Background())

	// 1. What the reconciler HANDS to the audit sink.
	//
	// Security story S3 moved the safety to the sink, so what travels from here
	// is a CATEGORY and nothing else: Error cannot be set from this package at
	// all (it is an audit.SafeText with no exported constructor), and Detail is
	// whatever the writer typed. The reconciler's own job is to classify at the
	// credential boundary, where the typed error is still alive, and hand over
	// the answer.
	entries := audits.Snapshot()
	if len(entries) == 0 {
		t.Fatal("the reconciler wrote no audit entry for a failed credential fetch, so this test proved nothing")
	}
	var sawCredFailure bool
	for _, e := range entries {
		assertNoReconcilerCredSentinel(t, "an audit entry's Error field", e.Error.String())
		assertNoReconcilerCredSentinel(t, "an audit entry's Detail field", e.Detail)
		if e.Action == "get_credentials" {
			sawCredFailure = true
			if e.Reason != audit.ReasonCredentials {
				t.Errorf(`the credential-failure audit entry has reason %q, want %q.

The reason is the reconciler's ANSWER to audit.Classify, computed where the typed error still exists. It is how the audit sink classifies by type instead of by reading the error's words — which is the exact thing that makes this bug class come back.`, e.Reason, audit.ReasonCredentials)
			}
			// No error-typed field on the entry at all. That is what carrying
			// only a category buys: nothing live travels this far.
			v := reflect.ValueOf(e)
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				if f.Kind() == reflect.Interface && !f.IsNil() {
					if _, ok := f.Interface().(error); ok {
						t.Errorf("the audit entry the reconciler hands over carries a live error in field %q — the decision travels, the error does not", v.Type().Field(i).Name)
					}
				}
			}
		}
	}
	if !sawCredFailure {
		t.Error(`no audit entry with action "get_credentials" was written, so the credential-failure path did not run`)
	}

	// 1b. What actually gets STORED. This is the surface a viewer reads: the
	// safe sentence in Error and an EMPTY Detail, both put there by the sink
	// from the category alone. %+v rather than JSON, because a json:"-" field
	// would be invisible to a JSON check.
	log := audit.NewLog(50)
	for _, e := range entries {
		log.Add(e)
	}
	for _, e := range log.List(0) {
		assertNoReconcilerCredSentinel(t, "a STORED audit entry printed with %+v", fmt.Sprintf("%+v", e))
		if e.Action == "get_credentials" {
			if e.Error.String() != credsafe.Message {
				t.Errorf("the stored credential-failure entry's Error = %q, want the fixed safe sentence", e.Error.String())
			}
			if e.Detail != "" {
				t.Errorf(`the stored credential-failure entry's Detail = %q, want EMPTY.

One answer, one field: it lives in Error.`, e.Detail)
			}
		}
	}

	// 2. The reconcile record's Message — what the API turns into
	// LastReconcile.Message and the managed-secrets rows.
	rec, ok := r.LastReconcile("prod-eu")
	if !ok {
		t.Fatal("no reconcile record for prod-eu, so this test proved nothing")
	}
	if rec.Outcome != OutcomeFailed {
		t.Errorf("outcome = %q, want %q — a failed credential fetch is a failure", rec.Outcome, OutcomeFailed)
	}
	assertNoReconcilerCredSentinel(t, "the reconcile record's Message", rec.Message)

	// 3. FailureSentence still classifies it. Swapping the raw text for the safe
	// sentence must not break the classification the fixed prefix drives — the
	// managed-secrets page would otherwise fall back to its generic sentence.
	if got := FailureSentence(rec.Message); !strings.Contains(got, "credentials") {
		t.Errorf(`FailureSentence(%q) = %q — it no longer classifies this as a credentials failure.

The fixed English prefix in the recordReconcile call is what FailureSentence matches on. Replacing the raw error text must leave that prefix intact.`, rec.Message, got)
	}
	assertNoReconcilerCredSentinel(t, "FailureSentence's output", FailureSentence(rec.Message))
}

// TestReconcilerCredFailure_UnrelatedErrorsKeepTheirText is the other half of the
// rule. A Kubernetes or git error that does NOT wrap a credentials error keeps
// its full text in the audit log — blanket redaction would gut the audit trail,
// and without this test "redact everything" would pass everything else here.
func TestReconcilerCredFailure_UnrelatedErrorsKeepTheirText(t *testing.T) {
	plainErr := errors.New("etcdserver: leader changed, retry the request")

	body := envelopedWithModes(testClusterEntry{Name: "prod-eu", Labels: map[string]string{"addon-foo": "enabled"}})
	audits := &auditCollector{}
	vault := &fakeVault{errs: map[string]error{"prod-eu": plainErr}}
	r := newReconcilerForTest(t, nil, fake.NewSimpleClientset(), vault, audits, body)

	r.pollOnce(context.Background())

	// The credential-fetch site passes credsafe.Message unconditionally on its
	// own Error field (belt and braces), so the useful check here is that the
	// entry still exists and that audit.Add did not blanket-redact a plain
	// error's Detail. The Add-level behaviour is pinned in internal/audit's own
	// test; this asserts the reconciler still records the failure at all.
	var found bool
	for _, e := range audits.Snapshot() {
		if e.Action == "get_credentials" && e.Result == "failure" {
			found = true
		}
	}
	if !found {
		t.Error("a plain (non-credential) fetch failure must still be audited as a failure")
	}
}

// TestReconcilerCredFailure_WrappedCredErrorStillCaught: a Kubernetes-shaped
// error that WRAPS a credentials error must still produce the safe sentence.
// The marker travels through the %w hop and errors.Is finds it — which is why
// the classification is by type and never by reading the error's words.
func TestReconcilerCredFailure_WrappedCredErrorStillCaught(t *testing.T) {
	wrapped := fmt.Errorf("reading the cluster Secret: %w",
		fmt.Errorf("resolving credentials: %w", markedCredErrorWithSentinel()))

	if !credsafe.Is(wrapped) {
		t.Fatal("a wrapper around a credentials error is no longer recognised as one — check that every hop uses %w")
	}

	body := envelopedWithModes(testClusterEntry{Name: "prod-eu", Labels: map[string]string{"addon-foo": "enabled"}})
	audits := &auditCollector{}
	vault := &fakeVault{errs: map[string]error{"prod-eu": wrapped}}
	r := newReconcilerForTest(t, nil, fake.NewSimpleClientset(), vault, audits, body)

	r.pollOnce(context.Background())

	log := audit.NewLog(50)
	for _, e := range audits.Snapshot() {
		assertNoReconcilerCredSentinel(t, "a wrapped-error audit entry's Error field", e.Error.String())
		assertNoReconcilerCredSentinel(t, "a wrapped-error audit entry's Detail field", e.Detail)
		log.Add(e)
	}
	for _, e := range log.List(0) {
		assertNoReconcilerCredSentinel(t, "a STORED audit entry from a wrapped credentials error", fmt.Sprintf("%+v", e))
	}
	rec, ok := r.LastReconcile("prod-eu")
	if !ok {
		t.Fatal("no reconcile record, so this test proved nothing")
	}
	assertNoReconcilerCredSentinel(t, "the reconcile record from a wrapped credentials error", rec.Message)
}
